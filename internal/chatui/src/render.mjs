import MarkdownIt from 'markdown-it';
import katex from 'katex';
import hljs from 'highlight.js/lib/core';
import go from 'highlight.js/lib/languages/go';
import python from 'highlight.js/lib/languages/python';
import javascript from 'highlight.js/lib/languages/javascript';
import typescript from 'highlight.js/lib/languages/typescript';
import json from 'highlight.js/lib/languages/json';
import bash from 'highlight.js/lib/languages/bash';
import powershell from 'highlight.js/lib/languages/powershell';
import sql from 'highlight.js/lib/languages/sql';
import yaml from 'highlight.js/lib/languages/yaml';
import xml from 'highlight.js/lib/languages/xml';
import css from 'highlight.js/lib/languages/css';
import diff from 'highlight.js/lib/languages/diff';

const languages = {
  go, python, javascript, typescript, json, bash, powershell, sql, yaml, xml, css, diff,
};
for (const [name, grammar] of Object.entries(languages)) hljs.registerLanguage(name, grammar);
const languageAliases = {
  js: 'javascript', jsx: 'javascript', ts: 'typescript', tsx: 'typescript',
  sh: 'bash', shell: 'bash', ps: 'powershell', ps1: 'powershell',
  yml: 'yaml', html: 'xml', htm: 'xml',
};

export const escapeHTML = (value) => String(value).replace(/[&<>"']/g, (ch) => ({
  '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
}[ch]));
const copyButton = (raw, label) => `<button class="icon copy" type="button" data-copy="${escapeHTML(encodeURIComponent(raw))}" title="${label}" aria-label="${label}"><i data-lucide="copy"></i></button>`;

function boundedTeX(tex) {
  if (tex.length > 8192) return false;
  let depth = 0;
  for (let i = 0; i < tex.length; i++) {
    if (tex[i] === '\\') { i++; continue; }
    if (tex[i] === '{' && ++depth > 64) return false;
    if (tex[i] === '}') depth--;
  }
  return depth === 0;
}

function mathHTML(tex, raw, display) {
  let content;
  try {
    if (!boundedTeX(tex)) throw new Error('bounded');
    content = katex.renderToString(tex, {
      displayMode: display, output: 'htmlAndMathml', trust: false,
      strict: 'error', throwOnError: true, maxExpand: 200, maxSize: 20,
      macros: Object.create(null), globalGroup: false,
    });
  } catch {
    content = `<code>${escapeHTML(raw)}</code><span class="math-error" title="公式未渲染，原文已保留">公式未渲染</span>`;
  }
  const tag = display ? 'div' : 'span';
  return `<${tag} class="formula ${display ? 'display' : 'inline'}">${content}${copyButton(tex, '复制 LaTeX')}</${tag}>`;
}

function isEscaped(source, pos) {
  let n = 0;
  while (pos > 0 && source[--pos] === '\\') n++;
  return n % 2 === 1;
}

function findClose(source, marker, start) {
  for (let pos = start; pos < source.length; pos++) {
    if (source.startsWith(marker, pos) && !isEscaped(source, pos)) return pos;
  }
  return -1;
}

function mathPlugin(md) {
  md.inline.ruler.before('escape', 'math', (state, silent) => {
    const start = state.pos;
    const source = state.src;
    let open, close, display = false;
    if (source.startsWith('\\(', start)) { open = '\\('; close = '\\)'; }
    else if (source.startsWith('\\[', start)) { open = '\\['; close = '\\]'; display = true; }
    else if (source.startsWith('$$', start)) { open = close = '$$'; display = true; }
    else if (source[start] === '$' && !isEscaped(source, start)) {
      // Single dollars are deliberately conservative: numeric prices are literal.
      if (/[0-9\s$]/.test(source[start + 1] || '') || /[\w$]/.test(source[start - 1] || '')) return false;
      open = close = '$';
    } else return false;
    const end = findClose(source, close, start + open.length);
    if (end < 0) {
      if (open === '$') return false;
      if (!silent) state.push('text', '', 0).content = source.slice(start);
      state.pos = source.length;
      return true;
    }
    const tex = source.slice(start + open.length, end);
    if (open === '$' && (!tex.trim() || /\n/.test(tex) || /[0-9\w$]/.test(source[end + 1] || '') || !/[a-zA-Z\\^_={}]/.test(tex))) return false;
    if (!silent) {
      const token = state.push('math', '', 0);
      token.content = tex;
      token.meta = { raw: source.slice(start, end + close.length), display };
    }
    state.pos = end + close.length;
    return true;
  });
  md.block.ruler.before('fence', 'math_block', (state, start, end, silent) => {
    const line = state.src.slice(state.bMarks[start] + state.tShift[start], state.eMarks[start]);
    const open = line.startsWith('$$') ? '$$' : line.startsWith('\\[') ? '\\[' : '';
    if (!open || state.sCount[start] >= 4) return false;
    const close = open === '$$' ? '$$' : '\\]';
    let content = line.slice(open.length), last = start, found = findClose(content, close, 0);
    while (found < 0 && last + 1 < end) {
      last++;
      content += '\n' + state.src.slice(state.bMarks[last] + state.tShift[last], state.eMarks[last]);
      if (content.length > 16384) return false;
      found = findClose(content, close, 0);
    }
    if (found < 0 || content.slice(found + close.length).trim()) return false;
    if (!silent) {
      const token = state.push('math', '', 0);
      token.block = true;
      token.content = content.slice(0, found);
      token.meta = { raw: open + content, display: true };
      token.map = [start, last + 1];
    }
    state.line = last + 1;
    return true;
  });
  md.renderer.rules.math = (tokens, idx) => mathHTML(tokens[idx].content, tokens[idx].meta.raw, tokens[idx].meta.display);
}

const markdown = new MarkdownIt({ html: false, linkify: false, breaks: false, maxNesting: 64 }).use(mathPlugin);
markdown.validateLink = (link) => {
  try { return ['http:', 'https:'].includes(new URL(link).protocol); } catch { return false; }
};
markdown.renderer.rules.image = (tokens, index) => `<span class="image-label">${escapeHTML(tokens[index].content)}</span>`;
markdown.renderer.rules.link_open = (tokens, idx, options, env, self) => {
  tokens[idx].attrSet('target', '_blank');
  tokens[idx].attrSet('rel', 'noopener noreferrer');
  return self.renderToken(tokens, idx, options);
};
markdown.renderer.rules.fence = (tokens, idx, options, env) => {
  const token = tokens[idx], language = token.info.trim().split(/\s/)[0].slice(0, 64);
  if (language === 'math') return mathHTML(token.content.trimEnd(), token.content, true);
  const normalized = languageAliases[language.toLowerCase()] || language.toLowerCase();
  const highlight = env.highlight !== false && languages[normalized] &&
    new TextEncoder().encode(token.content).length <= 100 * 1024;
  const highlighted = highlight
    ? hljs.highlight(token.content, { language: normalized, ignoreIllegals: true }).value
    : escapeHTML(token.content);
  const className = highlight ? ' class="hljs"' : '';
  return `<div class="code-block"><div class="code-title"><span>${escapeHTML(language || 'text')}</span>${copyButton(token.content, '复制代码')}</div><pre tabindex="0"><code${className}>${highlighted}</code></pre></div>`;
};
const tableOpen = markdown.renderer.rules.table_open;
markdown.renderer.rules.table_open = (tokens, idx, options, env, self) => `<div class="table-scroll" tabindex="0">${tableOpen ? tableOpen(tokens, idx, options, env, self) : self.renderToken(tokens, idx, options)}`;
markdown.renderer.rules.table_close = () => '</table></div>';
markdown.core.ruler.after('inline', 'tasks', (state) => {
  for (let idx = 0; idx < state.tokens.length; idx++) {
    const token = state.tokens[idx];
    if (token.type !== 'inline' || state.tokens[idx - 2]?.type !== 'list_item_open' || token.children?.[0]?.type !== 'text') continue;
    const first = token.children[0], match = /^\[([ xX])\] /.exec(first.content);
    if (!match) continue;
    first.content = first.content.slice(4);
    const checkbox = new state.Token('html_inline', '', 0);
    checkbox.content = `<input type="checkbox" disabled aria-label="任务状态"${match[1] !== ' ' ? ' checked' : ''}> `;
    token.children.unshift(checkbox);
  }
});

export function renderMarkdown(raw, { highlight = true } = {}) {
  // Oversized presentation must never edit or silently truncate the stored answer.
  if (raw.length > (1 << 20)) return `<pre class="raw-large">${escapeHTML(raw)}</pre>`;
  try { return markdown.render(raw, { highlight }); }
  catch { return `<pre>${escapeHTML(raw)}</pre><span class="math-error">格式未渲染</span>`; }
}

// Keep complete Markdown blocks in the stable stream pane. A blank line inside
// a fenced code or display-math block is not a safe render boundary.
export function splitStable(raw) {
  const source = String(raw ?? '');
  let fence = null;
  let displayMath = '';
  let stableEnd = 0;
  let lineStart = 0;
  while (lineStart < source.length) {
    const lineEnd = source.indexOf('\n', lineStart);
    const end = lineEnd < 0 ? source.length : lineEnd + 1;
    const line = source.slice(lineStart, lineEnd < 0 ? source.length : lineEnd);
    const trimmed = line.trim();
    const marker = /^ {0,3}(`{3,}|~{3,})(.*)$/.exec(line);
    if (fence) {
      if (marker && marker[1][0] === fence.char && marker[1].length >= fence.length &&
        !marker[2].trim()) fence = null;
    } else if (displayMath) {
      const close = findClose(line, displayMath, 0);
      if (close >= 0 && !line.slice(close + displayMath.length).trim()) displayMath = '';
    } else if (marker) {
      const token = marker[1];
      if (token[0] !== '`' || !marker[2].includes('`')) fence = { char: token[0], length: token.length };
    } else if (/^ {0,3}(?:\$\$|\\\[)/.test(line)) {
      const close = trimmed.startsWith('$$') ? '$$' : '\\]';
      const at = findClose(trimmed, close, 2);
      if (at < 0 || trimmed.slice(at + close.length).trim()) displayMath = close;
    }
    if (!fence && !displayMath && lineEnd >= 0 && /^\s*$/.test(line)) stableEnd = end;
    lineStart = end;
  }
  return { stable: source.slice(0, stableEnd), tail: source.slice(stableEnd) };
}
