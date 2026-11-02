import test from 'node:test';
import assert from 'node:assert/strict';
import { spaceChatGroups, spaceSelection, publicationMessage } from '../src/spaces.mjs';

test('global pins appear once while local chats stay at the top level', () => {
  const spaces = [{id:'local',source:'local'},{id:'group',source:'telegram'},{id:'empty',source:'local'}];
  const chats = [{id:'a',space_id:'local'},{id:'b',space_id:'group',pinned:true},{id:'c',space_id:'group'}];
  const grouped = spaceChatGroups(spaces,chats);
  assert.deepEqual(grouped.pinned.map(x=>x.id),['b']);
  assert.deepEqual(grouped.local.map(x=>x.id),['a']);
  assert.deepEqual(grouped.groups[0].chats.map(x=>x.id),['c']);
  assert.equal(grouped.groups[1].chats.length,0);
  assert.equal(chats.length,3);
});

test('deleted selection returns to Local and external movement describes publication', () => {
  const spaces=[{id:'local',source:'local'},{id:'g',source:'telegram'}];
  assert.equal(spaceSelection(spaces,'missing').id,'local');
  assert.match(publicationMessage(spaces[1]),/公开文本/);
  assert.doesNotMatch(publicationMessage(spaces[0]),/公开文本/);
});
