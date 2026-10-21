import { readFile } from 'node:fs/promises';
import http from 'node:http';

const DEFAULT_REPLY = '# Mock provider\n\nThis is a local response from the FreeAgent test provider.';
const MAX_BODY_BYTES = 2 * 1024 * 1024;

function usage(promptText, completionText) {
  const promptTokens = Math.max(1, Math.ceil(Buffer.byteLength(promptText, 'utf8') / 4));
  const completionTokens = Math.max(1, Math.ceil(Buffer.byteLength(completionText, 'utf8') / 4));
  return {
    prompt_tokens: promptTokens,
    completion_tokens: completionTokens,
    total_tokens: promptTokens + completionTokens,
  };
}

function parseArgs(argv) {
  const options = {
    reply: '',
    chunk: 64,
    delay: 20,
    models: ['mock-chat'],
    script: '',
  };
  const flags = new Set(['--reply', '--chunk', '--delay', '--models', '--script']);
  for (let index = 0; index < argv.length; index += 1) {
    const token = argv[index];
    if (token === '--help' || token === '-h') {
      console.log('Usage: node mock-provider.mjs [--reply FILE] [--chunk BYTES] [--delay MS] [--models NAME,...] [--script FILE]');
      process.exit(0);
    }
    const equal = token.indexOf('=');
    const name = equal >= 0 ? token.slice(0, equal) : token;
    if (!flags.has(name)) throw new Error(`unknown option: ${token}`);
    const value = equal >= 0 ? token.slice(equal + 1) : argv[++index];
    if (value === undefined) throw new Error(`missing value for ${name}`);
    if (name === '--reply' || name === '--script') options[name.slice(2)] = value;
    if (name === '--models') options.models = value.split(',').map((item) => item.trim()).filter(Boolean);
    if (name === '--chunk') options.chunk = Number.parseInt(value, 10);
    if (name === '--delay') options.delay = Number.parseInt(value, 10);
  }
  if (!Number.isInteger(options.chunk) || options.chunk < 1) throw new Error('--chunk must be a positive integer');
  if (!Number.isInteger(options.delay) || options.delay < 0) throw new Error('--delay must be a non-negative integer');
  if (options.models.length === 0) throw new Error('--models must contain at least one name');
  return options;
}

async function loadOptions(argv) {
  const options = parseArgs(argv);
  if (options.reply) options.replyText = await readFile(options.reply, 'utf8');
  else options.replyText = DEFAULT_REPLY;
  options.scriptItems = [];
  if (options.script) {
    const raw = await readFile(options.script, 'utf8');
    options.scriptItems = JSON.parse(raw);
    if (!Array.isArray(options.scriptItems)) throw new Error('--script must contain a JSON array');
  }
  return options;
}

function readBody(request) {
  return new Promise((resolve, reject) => {
    const chunks = [];
    let size = 0;
    request.on('data', (chunk) => {
      size += chunk.length;
      if (size > MAX_BODY_BYTES) {
        reject(new Error('request body is too large'));
        request.destroy();
        return;
      }
      chunks.push(chunk);
    });
    request.on('end', () => resolve(Buffer.concat(chunks).toString('utf8')));
    request.on('error', reject);
  });
}

function json(response, status, value) {
  const body = JSON.stringify(value);
  response.writeHead(status, { 'content-type': 'application/json; charset=utf-8' });
  response.end(body);
}

function writeError(response, status, message) {
  json(response, status, {
    error: { message, type: 'mock_error', code: 'mock_status' },
  });
}

function utf8Chunks(text, maxBytes) {
  const bytes = Buffer.from(text, 'utf8');
  const chunks = [];
  let offset = 0;
  while (offset < bytes.length) {
    let end = Math.min(offset + maxBytes, bytes.length);
    while (end > offset && end < bytes.length && (bytes[end] & 0xc0) === 0x80) end -= 1;
    if (end === offset) end = Math.min(offset + maxBytes, bytes.length);
    chunks.push(bytes.subarray(offset, end).toString('utf8'));
    offset = end;
  }
  return chunks;
}

function delay(ms) {
  return ms === 0 ? Promise.resolve() : new Promise((resolve) => setTimeout(resolve, ms));
}

function requestItem(options) {
  const item = options.scriptItems[options.scriptIndex] || { content: options.replyText };
  options.scriptIndex += 1;
  return item;
}

function toolCalls(item) {
  if (!Array.isArray(item.tool_calls)) return [];
  return item.tool_calls.map((call, index) => ({
    id: `call_${index + 1}`,
    type: 'function',
    function: {
      name: String(call?.name || 'mock_tool'),
      arguments: JSON.stringify(call?.arguments ?? {}),
    },
  }));
}

function promptText(body) {
  return Array.isArray(body.messages)
    ? body.messages.map((message) => String(message?.content || '')).join('\n')
    : '';
}

async function sendStream(response, body, item, options, requestNumber) {
  response.writeHead(200, {
    'content-type': 'text/event-stream; charset=utf-8',
    'cache-control': 'no-cache',
    connection: 'keep-alive',
  });
  const calls = toolCalls(item);
  const model = String(body.model || options.models[0]);
  const id = `mock-${requestNumber}`;
  const created = Math.floor(Date.now() / 1000);
  const send = async (value) => {
    response.write(`data: ${JSON.stringify(value)}\n\n`);
    await delay(options.delay);
  };
  if (calls.length) {
    for (const [index, call] of calls.entries()) {
      await send({
        id,
        model,
        created,
        choices: [{
          index: 0,
          delta: {
            ...(index === 0 ? { role: 'assistant' } : {}),
            tool_calls: [{ index, ...call }],
          },
          finish_reason: null,
        }],
      });
    }
    await send({
      id,
      model,
      created,
      choices: [{ index: 0, delta: {}, finish_reason: 'tool_calls' }],
      usage: usage(promptText(body), ''),
    });
  } else {
    const reply = String(item.content ?? options.replyText);
    for (const [index, part] of utf8Chunks(reply, options.chunk).entries()) {
      await send({
        id,
        model,
        created,
        choices: [{
          index: 0,
          delta: { ...(index === 0 ? { role: 'assistant' } : {}), content: part },
          finish_reason: null,
        }],
      });
    }
    await send({
      id,
      model,
      created,
      choices: [{ index: 0, delta: {}, finish_reason: 'stop' }],
      usage: usage(promptText(body), reply),
    });
  }
  response.end('data: [DONE]\n\n');
}

function sendJSON(response, body, item, options, requestNumber) {
  const calls = toolCalls(item);
  const content = calls.length ? '' : String(item.content ?? options.replyText);
  json(response, 200, {
    id: `mock-${requestNumber}`,
    object: 'chat.completion',
    created: Math.floor(Date.now() / 1000),
    model: String(body.model || options.models[0]),
    choices: [{
      index: 0,
      message: { role: 'assistant', content, ...(calls.length ? { tool_calls: calls } : {}) },
      finish_reason: calls.length ? 'tool_calls' : 'stop',
    }],
    usage: usage(promptText(body), content),
  });
}

async function main() {
  const options = await loadOptions(process.argv.slice(2));
  options.scriptIndex = 0;
  options.requestNumber = 0;
  const server = http.createServer(async (request, response) => {
    if (request.method === 'GET' && request.url === '/models') {
      process.stderr.write(`GET /models stream=false messages=0\n`);
      json(response, 200, {
        object: 'list',
        data: options.models.map((id) => ({ id, object: 'model' })),
      });
      return;
    }
    if (request.method !== 'POST' || request.url !== '/chat/completions') {
      writeError(response, 404, 'mock route not found');
      return;
    }
    let body;
    try {
      body = JSON.parse(await readBody(request));
    } catch {
      writeError(response, 400, 'mock request must be valid JSON');
      return;
    }
    const stream = body.stream === true;
    const messages = Array.isArray(body.messages) ? body.messages.length : 0;
    process.stderr.write(`POST /chat/completions stream=${stream} messages=${messages}\n`);
    if (request.headers.authorization) process.stderr.write('authorization header present\n');
    const item = requestItem(options);
    if (Number.isInteger(item?.status)) {
      writeError(response, item.status, `mock response status ${item.status}`);
      return;
    }
    options.requestNumber += 1;
    if (stream) await sendStream(response, body, item, options, options.requestNumber);
    else sendJSON(response, body, item, options, options.requestNumber);
  });
  server.on('clientError', (error) => process.stderr.write(`client error: ${error.message}\n`));
  server.listen(0, '127.0.0.1', () => {
    const address = server.address();
    process.stdout.write(`http://127.0.0.1:${address.port}\n`);
  });
  const close = () => server.close(() => process.exit(0));
  process.once('SIGINT', close);
  process.once('SIGTERM', close);
}

main().catch((error) => {
  process.stderr.write(`${error.message}\n`);
  process.exitCode = 2;
});
