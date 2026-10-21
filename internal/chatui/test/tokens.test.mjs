import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const css = readFileSync(new URL('../src/style.css', import.meta.url), 'utf8');
const blocks = [...css.matchAll(/:root\s*\{([^}]*)\}/g)].map((match) => match[1]);
const root = blocks[0] ?? '';
const darkRoot = blocks[1] ?? '';
const parseTokens = (block) => Object.fromEntries([...block.matchAll(/(--[\w-]+)\s*:\s*([^;]+);/g)]
  .map(([, name, value]) => [name, value.trim()]));
const tokens = parseTokens(root);
const darkTokens = parseTokens(darkRoot);
const colorTokenNames = (values) => Object.keys(values).filter((name) => /^#[0-9a-f]{3,8}$/i.test(values[name]));

// WCAG 2.x relative luminance and contrast ratio.
function luminance(hex) {
  const digits = hex.slice(1).length === 3 ? [...hex.slice(1)].map((c) => c + c).join('') : hex.slice(1);
  const [r, g, b] = [0, 2, 4].map((at) => parseInt(digits.slice(at, at + 2), 16) / 255)
    .map((c) => (c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}
function contrast(a, b) {
  const [light, dark] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (light + 0.05) / (dark + 0.05);
}
function color(name) {
  const value = tokens[name];
  assert.match(value ?? '', /^#[0-9a-f]{6}$/i, `${name} must be a six-digit hex color`);
  return value;
}
function atLeast(foreground, background, minimum) {
  const ratio = contrast(color(foreground), color(background));
  assert.ok(ratio >= minimum, `${foreground} on ${background} is ${ratio.toFixed(2)}:1, needs ${minimum}:1`);
}

const TEXT = ['--text', '--text-2', '--muted', '--accent'];
const STATES = ['--state-run', '--state-wait', '--state-stop', '--state-warn', '--state-done', '--state-fail', '--state-unknown'];

test('contrast formula matches WCAG reference values', () => {
  assert.equal(contrast('#000000', '#ffffff'), 21);
  assert.equal(contrast('#ffffff', '#ffffff'), 1);
  assert.equal(contrast('#fff', '#ffffff'), 1);
  assert.equal(contrast('#167b68', '#ffffff').toFixed(2), '5.16');
});
test('design tokens use the planned values', () => {
  assert.deepEqual({
    '--bg': tokens['--bg'], '--bg-side': tokens['--bg-side'], '--border': tokens['--border'],
    '--text': tokens['--text'], '--text-2': tokens['--text-2'], '--muted': tokens['--muted'],
    '--accent': tokens['--accent'], '--state-run': tokens['--state-run'], '--state-wait': tokens['--state-wait'],
    '--state-stop': tokens['--state-stop'], '--state-warn': tokens['--state-warn'], '--state-done': tokens['--state-done'],
    '--state-fail': tokens['--state-fail'], '--state-unknown': tokens['--state-unknown'],
    '--radius': tokens['--radius'], '--radius-card': tokens['--radius-card'],
  }, {
    '--bg': '#ffffff', '--bg-side': '#f7f8fa', '--border': '#e4e7eb',
    '--text': '#1f2328', '--text-2': '#424a53', '--muted': '#656d76',
    '--accent': '#167b68', '--state-run': '#167b68', '--state-wait': '#1f5fbf',
    '--state-stop': '#57606a', '--state-warn': '#9a5b00', '--state-done': '#1a7f37',
    '--state-fail': '#c0362c', '--state-unknown': '#7a3fb8',
    '--radius': '6px', '--radius-card': '8px',
  });
  assert.match(tokens['--font-ui'], /^"Segoe UI", "Microsoft YaHei UI"/);
  assert.match(tokens['--font-mono'], /^"Cascadia Mono"/);
});
test('text and state colors reach 4.5:1 on the reading background', () => {
  for (const name of [...TEXT, ...STATES]) atLeast(name, '--bg', 4.5);
});
test('sidebar text reaches 4.5:1 and state icons 3:1 on the side background', () => {
  for (const name of TEXT) atLeast(name, '--bg-side', 4.5);
  for (const name of STATES) atLeast(name, '--bg-side', 3);
});
test('dark tokens reach the planned contrast thresholds', () => {
  const darkColor = (name) => {
    const value = darkTokens[name];
    assert.match(value ?? '', /^#[0-9a-f]{6}$/i, `${name} must be a six-digit dark hex color`);
    return value;
  };
  const darkAtLeast = (foreground, background, minimum) => {
    const ratio = contrast(darkColor(foreground), darkColor(background));
    assert.ok(ratio >= minimum, `${foreground} on ${background} is ${ratio.toFixed(2)}:1, needs ${minimum}:1`);
  };
  for (const name of [...TEXT, ...STATES]) darkAtLeast(name, '--bg', 4.5);
  for (const name of TEXT) darkAtLeast(name, '--bg-side', 4.5);
  for (const name of STATES) darkAtLeast(name, '--bg-side', 3);
  assert.ok(contrast(darkColor('--on-accent'), darkColor('--accent')) >= 4.5);
});
test('light and dark token blocks have the same token names and no literal colors leak out', () => {
  assert.ok(blocks.length >= 2, 'expected light and dark token blocks');
  assert.deepEqual(colorTokenNames(darkTokens).sort(), colorTokenNames(tokens).sort());
  const outsideTokenBlocks = css.replace(/:root\s*\{[^}]*\}/g, '');
  assert.doesNotMatch(outsideTokenBlocks, /#[0-9a-f]{3,8}\b/i);
  assert.match(root, /color-scheme:\s*light dark/);
});
test('stylesheet keeps the type, weight and radius scale', () => {
  for (const [, size] of css.matchAll(/font(?:-size)?:\s*(\d+(?:\.\d+)?)px/g)) {
    assert.ok(Number(size) >= 12, `font size ${size}px is below the 12px minimum`);
  }
  for (const [, weight] of css.matchAll(/font-weight:\s*(\d+)/g)) {
    assert.ok(['400', '600'].includes(weight), `font weight ${weight} is outside 400/600`);
  }
  assert.doesNotMatch(css, /border-radius:[^;]*\b[57]px/);
  assert.doesNotMatch(css.replace(/:root\s*\{[^}]*\}/g, ''), /Consolas/);
});
