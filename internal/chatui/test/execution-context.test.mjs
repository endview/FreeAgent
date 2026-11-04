import test from 'node:test';
import assert from 'node:assert/strict';
import { activityTime, windowText, inputDisposition, inputLabel } from '../src/execution-context.mjs';
import { apiPath } from '../src/public-auth.mjs';
import { turnSignature } from '../src/state.mjs';

test('measured tool duration, active elapsed and missing historical timing remain distinct',()=>{
  assert.equal(activityTime({duration_ms:0}), '0.00s');
  assert.equal(activityTime({duration_ms:1234}), '1.23s');
  assert.equal(activityTime({status:'dispatched',started_at:'2026-11-04T00:00:00Z'},Date.parse('2026-11-04T00:00:03Z')), '运行 3s');
  assert.equal(activityTime({status:'finished'}),'耗时未记录');
});
test('window declaration, estimated bytes/tokens and reported usage stay separately labelled',()=>{
  const text=windowText({window:{tokens:0,byte_limit:131072,automatic_percent:80},last_request:{bytes:1000,estimated_input_tokens:350,occupancy_percent:30,newer_history_available:true,window:{output_reserve_tokens:512}},last_reported_usage:{input_tokens:222,output_tokens:9}});
  assert.match(text,/窗口未知/);assert.match(text,/估算 350/);assert.match(text,/1000 字节/);assert.match(text,/实际返回：输入 222/);assert.match(text,/已有更新消息/);
});
test('busy input chooses steering or FIFO queue, and public transport only exposes typed input/branch routes',()=>{
  assert.equal(inputDisposition(true),'queue');assert.equal(inputDisposition(true,'steer'),'steer');assert.equal(inputDisposition(false),'send');
  assert.equal(apiPath('attach/inputs?conversation_id=c',true),'/api/remote/inputs?conversation_id=c');
  assert.equal(apiPath('attach/branch',true),'/api/remote/branch');
  assert.throws(()=>apiPath('attach/backup',true));
});

test('received guidance is distinct from adopted guidance and cancelled messages',()=>{
  assert.equal(inputLabel({action:'queue',status:'pending'}),'等待发送');
  assert.equal(inputLabel({action:'steer',status:'pending'}),'引导已收到，等待处理');
  assert.equal(inputLabel({action:'steer',status:'adopted'}),'已用于当前任务');
  assert.equal(inputLabel({action:'queue',status:'cancelled'}),'已取消');
});
test('timing arrival updates the same saved activity row',()=>{
  const turn={key:'r',activity:[{key:'a',status:'finished'}]};const after=structuredClone(turn);after.activity[0].duration_ms=40;
  assert.notEqual(turnSignature(turn),turnSignature(after));
});
