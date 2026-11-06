const MAX_DIFF_LINES = 2000;

function lines(value) {
  const text = String(value ?? "");
  return text ? text.split(/\r\n|\r|\n/) : [];
}

export function diffLines(oldText, newText) {
  const before = lines(oldText);
  const after = lines(newText);
  if (before.length > MAX_DIFF_LINES || after.length > MAX_DIFF_LINES) {
    return {
      bounded: true,
      old: String(oldText ?? ""),
      new: String(newText ?? ""),
    };
  }
  const width = after.length + 1;
  const table = Array.from(
    { length: before.length + 1 },
    () => new Uint32Array(width),
  );
  for (let left = before.length - 1; left >= 0; left--) {
    for (let right = after.length - 1; right >= 0; right--) {
      table[left][right] =
        before[left] === after[right]
          ? table[left + 1][right + 1] + 1
          : Math.max(table[left + 1][right], table[left][right + 1]);
    }
  }
  const result = [];
  let left = 0,
    right = 0;
  while (left < before.length && right < after.length) {
    if (before[left] === after[right]) {
      result.push({ kind: "same", text: before[left++] });
      right++;
    } else if (table[left + 1][right] >= table[left][right + 1]) {
      result.push({ kind: "remove", text: before[left++] });
    } else {
      result.push({ kind: "add", text: after[right++] });
    }
  }
  while (left < before.length)
    result.push({ kind: "remove", text: before[left++] });
  while (right < after.length)
    result.push({ kind: "add", text: after[right++] });
  return { bounded: false, lines: result };
}
