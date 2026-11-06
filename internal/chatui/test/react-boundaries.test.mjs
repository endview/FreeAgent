import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readFileSync} from 'node:fs';
import {decodeSnapshot,decodeEvent,decodeControlOperation} from '../frontend/lib/api/decode.ts';
import {configureDrafts,formDraft,saveFormDraft,saveMessageDraft,messageDraft,savePendingSend,pendingSend,clearDrafts} from '../frontend/lib/drafts.ts';

const sample=JSON.parse(readFileSync(new URL('./fixtures/core-contract.json',import.meta.url),'utf8'));
test('snapshot decoder validates actual Core fields and rejects malformed nested data',()=>{
  assert.equal(decodeSnapshot(sample.snapshot).view.conversation.id,'synthetic-chat');
  for(const mutate of [value=>value.view.turns=null,value=>value.view.permission=null,value=>value.streams={},value=>value.view.turns[0].messages[0].raw={},value=>value.snapshot_seq=-1]){
    const value=structuredClone(sample.snapshot);mutate(value);assert.throws(()=>decodeSnapshot(value));
  }
  assert.equal(decodeEvent({...sample.event,kind:'future-event'}).kind,'future-event');
  assert.throws(()=>decodeEvent({kind:'model_text',message:{unexpected:'object'}}));
});
test('message and request drafts persist independently of volatile secrets and clear on logout',()=>{
  const data=new Map();const storage={getItem:key=>data.get(key)||null,setItem:(key,value)=>data.set(key,value),removeItem:key=>data.delete(key)};
  configureDrafts('instance-a',storage);saveFormDraft('provider:a',{key:'synthetic-secret'});saveMessageDraft('chat-a','用户草稿');
  savePendingSend('chat-a',{request_key:'original-key',prompt:'用户草稿',attachments:[],kind:'submit',permission:'ask',confirm_full_access:false,capabilities:{},model:'echo'});
  assert.equal([...data.values()].some(value=>value.includes('synthetic-secret')),false);
  configureDrafts('instance-b',storage);assert.equal(messageDraft('chat-a'),'');assert.equal(formDraft('provider:a',{}).key,undefined);
  configureDrafts('instance-a',storage);assert.equal(messageDraft('chat-a'),'用户草稿');assert.equal(pendingSend('chat-a').request_key,'original-key');
  clearDrafts();assert.equal(data.has('freeagent.drafts.v81:instance-a'),false);assert.equal(messageDraft('chat-a'),'');
});

test("control previews use the actual operation_id field from Go",()=>{assert.equal(decodeControlOperation(sample.control_operation).operation_id,"synthetic-operation");assert.throws(()=>decodeControlOperation({id:"wrong-field",kind:"auto.set_policy",preview:"synthetic",status:"pending"}));});
