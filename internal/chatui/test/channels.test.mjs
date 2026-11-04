import test from 'node:test';
import assert from 'node:assert/strict';
import { channelConfig, channelSections, telegramAccessSection } from '../src/channels.mjs';
test('channel forms preserve explicit false, scope keys and escape platform text', () => {
 const status = {config:{lark:{enabled:false,key:'k',app_id:'cli_1'}},lark:{config:{enabled:true},last_error:'<secret>',limitations:['<limit>']}};
 assert.equal(channelConfig(status,'lark').enabled,false);
 const html=channelSections(status,[{id:'k',protocol:'lark',name:'Saved app'},{id:'model',protocol:'openai-compatible',name:'Must not appear'}]);
 assert.match(html,/Saved app/);assert.doesNotMatch(html,/Must not appear|<secret>|<limit>/);assert.match(html,/&lt;secret&gt;/);assert.match(html,/\/qq\/events/);assert.match(html,/channel-qq-listen/);
});

test('Telegram replaces bearer pairing with identity-bound admin approval and separate tool limits', () => {
 const html=channelSections({telegram:{running:true},telegram_access:{requests:[{id:'r',code:'ABCD2345',display_name:'<admin>',target:{application_id:'1',chat_id:'-2'},person_id:'telegram:user:9',kind:'group',expires_at:'2026-11-04'}]}});
 const telegram=html.split('data-channel="lark"')[0];
 assert.doesNotMatch(telegram,/channel-code|\/pair|<admin>/);
 assert.match(telegram,/ABCD2345|telegram-access-approve/);assert.match(telegram,/&lt;admin&gt;/);
 assert.match(telegram,/tg-request-whole-0/);assert.doesNotMatch(telegram,/tg-request-whole-0[^>]*checked/);
 const allowed=telegramAccessSection({access:[{target:{chat_id:'90'},kind:'private',enabled:true,members:['telegram:user:9'],space_permission:'full',people:[{id:'telegram:user:9',mode:'none'}],effective_permissions:{'telegram:user:9':'none'}}]});
 assert.match(allowed,/空间权限 full/);assert.match(allowed,/人员 none → 最终 none/);assert.match(allowed,/停用访问/);
});
