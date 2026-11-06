import {readFileSync} from 'node:fs';
import assert from 'node:assert/strict';
import {test} from 'node:test';
import {eventPolicy,policyFor} from '../frontend/lib/events/policy.ts';
const sample=JSON.parse(readFileSync(new URL('./fixtures/core-contract.json',import.meta.url),'utf8'));
test('every Core event has an explicit frontend policy',()=>{
  assert.deepEqual(Object.keys(eventPolicy).sort(),[...sample.event_kinds].sort());
  assert.equal(policyFor('unknown-future-type'),'unknown');
});
test('real Core snapshot and event samples retain current DTO fields',()=>{
  const {snapshot,event}=sample;
  assert.equal(snapshot.snapshot_stable,true);assert.equal(snapshot.snapshot_seq,7);
  assert.equal(typeof snapshot.generation,'string');assert.equal(snapshot.busy,false);
  assert.equal(snapshot.view.conversation.space_id,'local');
  assert.equal(snapshot.view.permission.mode,'ask');
  assert.equal(snapshot.view.turns[0].messages[1].raw,'合成输出');
  assert.equal(event.kind,'model_text');assert.equal(event.seq,8);assert.equal(event.generation,snapshot.generation);
});
