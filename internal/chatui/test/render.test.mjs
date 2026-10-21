import test from 'node:test';
import assert from 'node:assert/strict';
import { renderMarkdown, splitStable } from '../src/render.mjs';

for (const raw of [String.raw`\(x^2\)`, String.raw`\[x^2\]`, '$$x^2$$', '$x^2$', '```math\nx^2\n```', '$$\nx^2\n$$']) {
  test(`formula ${raw}`, () => {
    const html = renderMarkdown(raw);
    assert.match(html, /class="katex"/);
    assert.match(html, /复制 LaTeX/);
  });
}
test('currency, escaped dollars and code are literal', () => {
  const html = renderMarkdown('价格 $5 和 $10，预算 $100$。\n\n`$x$`\n\n```tex\n\\(x\\)\n$$x$$\n```\n\n\\$x\\$');
  assert.doesNotMatch(html, /class="katex"/);
  assert.match(html, /价格 \$5 和 \$10/);
  assert.match(html, /复制代码/);
});
test('incomplete streamed formulas preserve exact delimiters', () => {
  for (const raw of [String.raw`\(x+`, String.raw`\[x+`, '$$x+', '$x+']) {
    assert.match(renderMarkdown(raw), new RegExp(raw.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')));
    assert.doesNotMatch(renderMarkdown(raw), /class="katex"/);
  }
});
test('invalid, recursive, deep and huge math have bounded literal fallback', () => {
  for (const raw of [String.raw`\(\frac{\)`, String.raw`\(\def\a{\a}\a\)`, `\\(${'{'.repeat(100)}x${'}'.repeat(100)}\\)`, `\\(${'x'.repeat(10000)}\\)`]) {
    assert.match(renderMarkdown(raw), /公式未渲染/);
  }
});
test('html and dangerous resources never execute or load', () => {
  const html = renderMarkdown('<script>alert(1)</script>\n<img src=x onerror=alert(1)>\n\n![source](https://evil.example/a)\n\n[x](javascript:alert(1))\n\n\\(\\includegraphics{https://evil.example/a}\\)\n\n\\(\\htmlStyle{color:red}{x}\\)');
  assert.doesNotMatch(html, /<script|<img|<[^>]+\s(?:src=|href="javascript:|style="color:red")/);
  assert.match(html, /&lt;script&gt;/);
});
test('markdown tables, tasks and links use structured tokens', () => {
  const html = renderMarkdown('# Heading\n\n|A|B|\n|-|-|\n|one|two|\n\n- [x] done\n- [ ] pending\n\n> Quote\n\n[official](https://example.com)\n\n**bold** and ~~strike~~');
  for (const pattern of [/table-scroll/, /<table>/, /disabled/, / checked/, /blockquote/, /noopener noreferrer/, /<strong>/, /<s>/]) assert.match(html, pattern);
});
test('macro definitions do not cross message boundaries', () => {
  renderMarkdown(String.raw`\(\gdef\custom{x}\custom\)`);
  assert.match(renderMarkdown(String.raw`\(\custom\)`), /公式未渲染/);
});
test('splitStable preserves source and only commits complete blocks', () => {
  for (const raw of [
    'one\n\ntwo',
    '```js\nconst value = 1;\n```\n\nnext',
    '~~~text\nbody\n~~~\n\nnext',
    '$$\nx^2\n$$\n\nnext',
    'before\n\n```js\nunfinished',
    'no blank line',
  ]) {
    const parts = splitStable(raw);
    assert.equal(parts.stable + parts.tail, raw);
    assert.equal(parts.stable.includes('```js\nunfinished'), false);
  }
  assert.deepEqual(splitStable('no blank line'), { stable: '', tail: 'no blank line' });
  assert.deepEqual(splitStable('one\n\ntwo'), { stable: 'one\n\n', tail: 'two' });
  assert.deepEqual(splitStable('$$\nx^2\n$$\n\nnext'), { stable: '$$\nx^2\n$$\n\n', tail: 'next' });
});
test('fenced code highlights only fixed languages within the size bound', () => {
  const html = renderMarkdown('```javascript\nconst value = "<script>";\n```');
  assert.match(html, /class="hljs"/);
  assert.match(html, /hljs-keyword/);
  assert.doesNotMatch(html, /<script>/);
  assert.doesNotMatch(renderMarkdown('```ruby\nputs "x"\n```'), /hljs-/);
  const huge = `\`\`\`javascript\n${'x'.repeat(100 * 1024 + 1)}\n\`\`\``;
  assert.doesNotMatch(renderMarkdown(huge), /hljs-/);
  assert.doesNotMatch(renderMarkdown('```js\nconst x = 1;\n```', { highlight: false }), /hljs/);
  assert.doesNotMatch(renderMarkdown(`\`\`\`js\n// ${'字'.repeat(35000)}\n\`\`\``), /hljs/);
});
test('stream boundaries respect incomplete lines, fence closers and math contents', () => {
  for (const raw of [
    '```js\nx\n```not-a-close\n\nnext',
    '````js\nx\n```\n\nnext',
    '~~~\n$$\n\nnext',
    '$$\n```\n\nx',
    '\\[\nx\n\n',
  ]) assert.deepEqual(splitStable(raw), { stable: '', tail: raw });
  assert.deepEqual(splitStable('one\n '), { stable: '', tail: 'one\n ' });
  assert.deepEqual(splitStable('~~~\nx\n~~~~\n\nnext'), { stable: '~~~\nx\n~~~~\n\n', tail: 'next' });
  assert.deepEqual(splitStable('\\[\nx\n\\]\n\nnext'), { stable: '\\[\nx\n\\]\n\n', tail: 'next' });
});
