import test from 'node:test';
import assert from 'node:assert/strict';
import { SETTINGS,settingsRoute,settingsNavigation,SettingsDrafts } from '../src/settings.mjs';

test('all settings pages belong to one group and keep current route visible',()=>{
  const ids=SETTINGS.flatMap(([, ,pages])=>pages.map(([id])=>id));
  assert.equal(ids.length,new Set(ids).size);
  for(const id of ids){const route=settingsRoute(id);assert.equal(route.id,id);assert.match(settingsNavigation(id),new RegExp(`data-page="${id}" aria-current="page"`));}
  assert.equal(settingsRoute('unknown').id,'providers');
});
test('failed save and navigation retain isolated volatile drafts; successful save discards',()=>{
  const drafts=new SettingsDrafts();
  drafts.remember('channels',[{id:'secret',value:'fixture-secret',type:'password'},{id:'on',value:'on',type:'checkbox',checked:false},{id:'file',type:'file',value:'ignored'}]);
  const restored={secret:{value:''},on:{checked:true}};
  drafts.restore('channels',id=>restored[id]);assert.equal(restored.secret.value,'fixture-secret');assert.equal(restored.on.checked,false);
  const other={secret:{value:'untouched'}};drafts.restore('providers',id=>other[id]);assert.equal(other.secret.value,'untouched');
  assert.equal(drafts.has('channels'),true);drafts.discard('channels');assert.equal(drafts.has('channels'),false);
});
