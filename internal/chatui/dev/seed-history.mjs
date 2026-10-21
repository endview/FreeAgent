// Dev-only: token is read from the process environment, never printed.
import { randomUUID } from 'node:crypto';
import { setTimeout as delay } from 'node:timers/promises';

const origin = new URL(process.env.FREEAGENT_SEED_ORIGIN || 'http://127.0.0.1:0');
if (!['127.0.0.1', '[::1]'].includes(origin.hostname) || origin.protocol !== 'http:') {
  throw new Error('A numeric loopback HTTP origin is required');
}
const token = process.env.FREEAGENT_SEED_TOKEN;
if (!token) throw new Error('FREEAGENT_SEED_TOKEN is required');
async function api(path, body) {
  const response = await fetch(new URL(`/api/${path}`, origin), {
    method: body === undefined ? 'GET' : 'POST',
    headers: { 'X-FreeAgent-Key': token, 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(30000),
  });
  if (!response.ok) throw new Error(`Seed request failed: HTTP ${response.status}`);
  return response.json();
}
const env = await api('environment');
const echo = Object.keys(env.models).find((id) => env.models[id].provider === 'echo');
if (!echo) throw new Error('Configure a local Echo model before seeding');
const chat = await api('chats', { title: 'History performance fixture (200 turns)' });
await api(`chats/${chat.id}/model`, { model: echo });
const block = 'Performance fixture.\n\n```javascript\nconst value = 42;\n```\n\n| A | B |\n|---|---|\n| one | two |\n\n$$x^2 + y^2 = 1$$\n\n';
for (let index = 0; index < 200; index++) {
  await api(`chats/${chat.id}/submit`, {
    request_key: randomUUID(), prompt: `Turn ${index + 1}\n\n${block.repeat(8)}`,
  });
  const deadline = Date.now() + 30000;
  for (;;) {
    const snapshot = await api(`chats/${chat.id}`);
    if (!snapshot.busy) {
      if (snapshot.view.turns.at(-1)?.state !== 'completed') throw new Error('Echo run did not complete');
      break;
    }
    if (Date.now() > deadline) throw new Error('Seed run timed out');
    await delay(20);
  }
}
console.log(JSON.stringify({ chat: chat.id, turns: 200 }));
