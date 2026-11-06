import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const css = readFileSync(new URL('../frontend/styles/tokens.css', import.meta.url), 'utf8');
const blocks = [...css.matchAll(/(?::root|\.dark)\s*\{([^}]*)\}/g)].map((match) => match[1]);
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

const TEXT = ['--foreground', '--muted-foreground', '--primary', '--accent-foreground'];
const STATES = ['--state-running','--state-queued','--state-waiting','--state-unknown','--state-failed','--state-cancelled','--state-completed'];

test('contrast formula matches WCAG reference values', () => {
  assert.equal(contrast('#000000', '#ffffff'), 21);
  assert.equal(contrast('#ffffff', '#ffffff'), 1);
  assert.equal(contrast('#fff', '#ffffff'), 1);
  assert.equal(contrast('#167b68', '#ffffff').toFixed(2), '5.16');
});
test('the approved workbench palette keeps UNKNOWN distinct from failure',()=>{
  assert.equal(tokens['--sidebar'],'#f5f6f8');assert.equal(tokens['--primary'],'#2958d4');
  assert.notEqual(tokens['--state-unknown'],tokens['--state-failed']);
});
test('reading and navigation text and state colors meet contrast thresholds',()=>{
  for(const values of [tokens,darkTokens]){
    for(const foreground of [...TEXT,...STATES])for(const background of ['--background','--sidebar']){
      const ratio=contrast(values[foreground],values[background]);
      assert.ok(ratio>=4.5,foreground+' on '+background+' contrast '+ratio.toFixed(2));
    }
    assert.ok(contrast(values['--primary'],values['--primary-foreground'])>=4.5);
  }
});
test('both themes define all semantic colors; literal colors remain in token blocks',()=>{
  assert.deepEqual(colorTokenNames(tokens).sort(),colorTokenNames(darkTokens).sort());
  assert.doesNotMatch(css.replace(/(?::root|\.dark)\s*\{[^}]*\}/g,''),/#[0-9a-f]{3,8}\b/i);
});
test('body text has a readable minimum and narrow input avoids mobile zoom',()=>{
  for(const [,size] of css.matchAll(/font-size:\s*(\d+(?:\.\d+)?)px/g))assert.ok(Number(size)>=12,'font size '+size);
  assert.match(css,/font-size:\s*16px/);assert.doesNotMatch(css,/letter-spacing:\s*-/);
});
