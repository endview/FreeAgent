import test from 'node:test';
import assert from 'node:assert/strict';
import { channelConfig, channelSections } from '../src/channels.mjs';
test('channel forms preserve explicit false, scope keys and escape platform text', () => {
 const status = {config:{lark:{enabled:false,key:'k',app_id:'cli_1'}},lark:{config:{enabled:true},last_error:'<secret>',limitations:['<limit>']}};
 assert.equal(channelConfig(status,'lark').enabled,false);
 const html=channelSections(status,[{id:'k',protocol:'lark',name:'Saved app'},{id:'model',protocol:'openai-compatible',name:'Must not appear'}]);
 assert.match(html,/Saved app/);assert.doesNotMatch(html,/Must not appear|<secret>|<limit>/);assert.match(html,/&lt;secret&gt;/);assert.match(html,/\/qq\/events/);assert.match(html,/channel-qq-listen/);
});
