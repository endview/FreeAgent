import test from 'node:test';
import assert from 'node:assert/strict';
import { commandRows, commandOutput, commandState, remainingSeconds, directCommandText } from '../src/command-sessions.mjs';

test('command panel escapes untrusted executable/output and distinguishes UNKNOWN', () => {
  const record = {id:'id"',state:'unknown',mode:'background',receipt:{command:'<script>',cleanup:'unknown'},output:[{text:'<raw>'}],output_gap:true};
  const html = commandRows([record], () => 'icon');
  assert.ok(html.includes('&lt;script&gt;'));
  assert.ok(!html.includes('data-action="command-stop"'));
  assert.equal(commandState(record),'结果未知');
  assert.ok(commandOutput(record).includes('[前段输出已丢弃或不完整]'));
});
test('service rows expose finite extension and readiness; stopped rows cannot extend',()=>{
  const r={id:'svc',state:'running',mode:'service',revision:2,expires_at:'2026-10-25T12:00:00Z',receipt:{command:'["C:\\\\bin\\\\node.exe","server.js"]'},ready:true};
  const html=commandRows([r],()=> 'icon');
  assert.ok(html.includes('command-extend'));assert.ok(html.includes('data-revision="2"'));assert.ok(html.includes('已观察到就绪文本'));
  assert.ok(!commandRows([{...r,state:'stopped'}],()=> '').includes('command-extend'));
  assert.equal(remainingSeconds('2026-10-25T12:00:03Z',Date.parse('2026-10-25T12:00:00Z')),3);
  assert.equal(remainingSeconds('bad'),0);
});
test('only live commands expose Stop; empty list remains legible', () => {
  for (const state of ['running','stopping']) assert.ok(commandRows([{id:'a',state,receipt:{}}],()=> '').includes('command-stop'));
  assert.ok(commandRows([],()=> '').includes('没有后台命令'));
});
test('direct user command presentation keeps process output literal and omits protocol JSON',()=>{
  assert.equal(directCommandText('Explicit command request: {"command":"/bin/node","cwd":".","mode":"service"}','user'),'启动服务：node\n工作目录：.');
  assert.equal(directCommandText('{"command":{"exit_code":0},"output":"<script>literal</script>"}','assistant'),'<script>literal</script>');
  assert.ok(directCommandText('{"id":"a","expires_at":"deadline","mode":"service","receipt":{"status":"started"}}','assistant').startsWith('服务已启动'));
  assert.equal(directCommandText('plain error','assistant'),'plain error');
});
