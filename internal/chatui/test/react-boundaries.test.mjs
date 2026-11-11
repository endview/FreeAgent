import assert from 'node:assert/strict';
import {test} from 'node:test';
import {readFileSync} from 'node:fs';
import {decodeSnapshot,decodeEvent,decodeControlOperation} from '../frontend/lib/api/decode.ts';
import {configureDrafts,formDraft,saveFormDraft,saveMessageDraft,messageDraft,savePendingSend,pendingSend,clearDrafts,saveAttachmentDraft,attachmentDraft,consumeSentDraft,branchRequestKey,clearBranchRequest} from '../frontend/lib/drafts.ts';

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


test('receipt consumes only its submitted draft and attachments, preserving another chat and later edits',()=>{
  clearDrafts();configureDrafts('receipt-fixture');
  const request={request_key:'same',prompt:'sent A',attachments:['first'],kind:'submit'};
  saveMessageDraft('a','sent A');saveMessageDraft('b','unfinished B');
  saveAttachmentDraft('a',[{id:'first',name:'1',bytes:1},{id:'later',name:'2',bytes:1}]);
  savePendingSend('a',request);consumeSentDraft('a',request);
  assert.equal(messageDraft('a'),'');assert.equal(messageDraft('b'),'unfinished B');
  assert.deepEqual(attachmentDraft('a').map(x=>x.id),['later']);assert.equal(pendingSend('a'),undefined);
  saveMessageDraft('a','new edit');consumeSentDraft('a',request);assert.equal(messageDraft('a'),'new edit');
  clearDrafts();
});

test('branch retries keep their key across remount and reject different unresolved operations',()=>{
  const data=new Map();const storage={getItem:k=>data.get(k)||null,setItem:(k,v)=>data.set(k,v),removeItem:k=>data.delete(k)};
  configureDrafts('branch-fixture',storage);const key=branchRequestKey('a','message:1:rerun');
  configureDrafts('temporary',storage);configureDrafts('branch-fixture',storage);
  assert.equal(branchRequestKey('a','message:1:rerun'),key);
  assert.throws(()=>branchRequestKey('a','message:2:rerun'));
  clearBranchRequest('a');assert.notEqual(branchRequestKey('a','message:2:rerun'),key);
  clearDrafts();
});
