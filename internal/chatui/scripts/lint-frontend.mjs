import {readdir, readFile} from 'node:fs/promises';
import {join, relative} from 'node:path';
const root = new URL('../frontend/', import.meta.url);
let failures = 0;
for await (const path of walk(root)) {
  const text = await readFile(path, 'utf8');
  const name = relative(root.pathname, path.pathname).replaceAll('\\', '/');
  if (name.endsWith('.css')) continue;
  if (/dangerouslySetInnerHTML/.test(text) && !name.endsWith('features/messages/Markdown.tsx')) fail(name, 'Only the reviewed Markdown renderer can insert HTML.');
  if (/(?:bg|text|border|ring|fill|stroke)-(?:black|white|(?:slate|gray|zinc|neutral|stone|red|orange|amber|yellow|lime|green|emerald|teal|cyan|sky|blue|indigo|violet|purple|fuchsia|pink|rose)-\d{2,3})(?:[\s/"'`:]|$)/.test(text)) fail(name, 'Use a semantic color token.');
}
if (failures) process.exitCode = 1;
else console.log('Frontend token and HTML rules passed.');
function fail(name, message) { failures++; console.error(`${name}: ${message}`); }
async function* walk(dir) {
  for (const entry of await readdir(dir, {withFileTypes:true})) {
    const path = new URL(entry.name + (entry.isDirectory() ? '/' : ''), dir);
    if (entry.isDirectory()) yield* walk(path);
    else if (/\.(?:tsx?|css)$/.test(entry.name)) yield path;
  }
}
