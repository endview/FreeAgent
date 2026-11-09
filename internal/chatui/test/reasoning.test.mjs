import test from 'node:test';
import assert from 'node:assert/strict';
import {reasoningOptions,reasoningSelection,reasoningValue} from '../frontend/lib/reasoning.ts';

const opus={control:'anthropic',required:true,efforts:'low,medium,high,xhigh,max',default_mode:'enabled',default_effort:'medium'};
test('required thinking has no off choice and provider default stays distinct from an effort',()=>{
 const options=reasoningOptions({control:'anthropic',efforts:opus.efforts},'chat',opus);
 assert.equal(options.some(([id])=>id==='disabled'),false);
 assert.match(options.find(([id])=>id==='default')[1],/开启.*medium/);
 assert.deepEqual(reasoningSelection('default'),{mode:'default'});
 assert.deepEqual(reasoningSelection('effort:medium'),{effort:'medium'});
 assert.equal(reasoningSelection('inherit'),undefined);
});
test('Sonnet default off and its effort default are independently represented',()=>{
 const info={...opus,required:false,default_mode:'disabled',default_effort:'high',efforts:'low,medium,high,max'};
 const options=reasoningOptions({control:'anthropic',efforts:info.efforts},'chat',info,false);
 assert.match(options.find(([id])=>id==='default')[1],/关闭/);
 assert.equal(options.some(([id])=>id==='disabled'),true);
 assert.equal(options.some(([id])=>id==='effort:xhigh'),false);
});
test('custom efforts are selectable and unverified; known efforts do not inherit that label',()=>{
 const options=reasoningOptions({control:'anthropic',efforts:'low,ultra'},'chat',opus);
 assert.match(options.find(([id])=>id==='effort:ultra')[1],/未经官方核对/);
 assert.doesNotMatch(options.find(([id])=>id==='effort:low')[1],/未经官方核对/);
 assert.deepEqual(reasoningSelection(reasoningValue({effort:'ultra'})),{effort:'ultra'});
 assert.match(reasoningOptions({control:'openai',efforts:'low'},'chat')[2][1],/未经官方核对/);
});
test('DeepSeek Responses off uses the off selection and does not offer Chat-only enable',()=>{
 const options=reasoningOptions({control:'deepseek',efforts:'low,high,max'},'responses',undefined,false);
 assert.equal(options.some(([id])=>id==='disabled'),true);
 assert.equal(options.some(([id])=>id==='enabled'),false);
});
