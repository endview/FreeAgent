import test from 'node:test';
import assert from 'node:assert/strict';
import { commandRows, commandOutput, commandState } from '../src/command-sessions.mjs';

test('command panel escapes untrusted executable/output and distinguishes UNKNOWN', () => {
  const record = {id:'id"',state:'unknown',mode:'background',receipt:{command:'<script>',cleanup:'unknown'},output:[{text:'<raw>'}],output_gap:true};
  const html = commandRows([record], () => 'icon');
  assert.ok(html.includes('&lt;script&gt;'));
  assert.ok(!html.includes('data-action="command-stop"'));
  assert.equal(commandState(record),'结果未知');
  assert.ok(commandOutput(record).includes('[前段输出已丢弃或不完整]'));
});
test('only live commands expose Stop; empty list remains legible', () => {
  for (const state of ['running','stopping']) assert.ok(commandRows([{id:'a',state,receipt:{}}],()=> '').includes('command-stop'));
  assert.ok(commandRows([],()=> '').includes('没有后台命令'));
});
