import test from 'node:test';
import assert from 'node:assert/strict';
import { extensionStatus, characterSelection } from '../frontend/lib/presentation/capabilities.mjs';

test('enabled idle extension is not reported as disabled; missing storage is explicit',()=>{
  const item={available:true,enabled:true,state:'stopped',storage:'postgresql',storage_ready:true};
  assert.equal(extensionStatus(item).label,'已启用 · 按需启动');
  assert.equal(extensionStatus({...item,enabled:false}).label,'未启用');
  assert.equal(extensionStatus({...item,storage_ready:false}).label,'待初始化存储');
  assert.equal(extensionStatus({...item,error:'initialization failed'}).tone,'error');
});
test('an empty or unauthorized default cannot claim an active character',()=>{
  const choices=[{id:'maya',label:'Maya'}];
  assert.equal(characterSelection(choices,'','').ready,false);
  assert.equal(characterSelection(choices,'','hidden-role').ready,false);
  assert.deepEqual(characterSelection(choices,'','maya'),{id:'maya',label:'Maya',source:'default',ready:true});
  assert.equal(characterSelection(choices,'missing','maya').ready,false);
  assert.equal(characterSelection(choices,'maya','').source,'conversation');
});
