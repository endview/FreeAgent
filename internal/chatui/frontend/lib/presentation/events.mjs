// Stream with a header credential. Cookies are shared across loopback ports
// and must never carry the local Owner capability.
export function eventStream(url, headers, callbacks) {
  const controller = new AbortController();
  let closed = false,
    retry,
    activeReader;
  async function connect() {
    try {
      const response = await fetch(url, {
        headers,
        signal: controller.signal,
        cache: "no-store",
      });
      if (response.status === 401 && callbacks.unauthorized) {
        closed = true;
        await response.body?.cancel();
        callbacks.unauthorized();
        return;
      }
      if (!response.ok || !response.body)
        throw new Error("Event connection failed");
      callbacks.open();
      const reader = response.body.getReader();
      activeReader = reader;
      const decoder = new TextDecoder();
      let buffer = "";
      try {
        while (!closed) {
          const { value, done } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          if (buffer.length > 2 * 1024 * 1024)
            throw new Error("Event exceeds limit");
          let boundary;
          while ((boundary = buffer.indexOf("\n\n")) >= 0) {
            const frame = buffer.slice(0, boundary);
            buffer = buffer.slice(boundary + 2);
            const data = frame
              .split("\n")
              .filter((line) => line.startsWith("data:"))
              .map((line) => line.slice(5).trimStart())
              .join("\n");
            if (data) callbacks.message({ data });
          }
        }
      } finally {
        await reader.cancel().catch(() => {});
        activeReader = null;
      }
    } catch (error) {
      if (closed) return;
    }
    if (!closed) {
      callbacks.error();
      retry = setTimeout(connect, 1500);
    }
  }
  connect();
  return {
    close() {
      closed = true;
      clearTimeout(retry);
      controller.abort();
      activeReader?.cancel().catch(() => {});
    },
  };
}
