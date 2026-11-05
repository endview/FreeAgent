import test from 'node:test';
import assert from 'node:assert/strict';
import { channelConfig, channelSections, telegramAccessSection } from '../src/channels.mjs';
test('optional connections preserve explicit false and isolate channel credentials',()=>{
 const status={config:{connections:[{id:'lark-one',source:'lark',enabled:false,credential:'k',application_id:'app:tenant'}]},connections:[]};
 assert.equal(channelConfig(status,'lark').enabled,false);
 const html=channelSections(status,[{id:'k',protocol:'lark',name:'Saved app'}]);
 assert.match(html,/Saved app/);assert.match(html,/channel-connection-delete/);assert.match(html,/\/qq\/events/);
 assert.doesNotMatch(channelSections(),/channel-telegram-enabled|channel-lark-enabled|channel-qq-enabled|Telegram 准入/);
 const escaped=channelSections({config:{connections:[{id:'<id>',source:'telegram',application_id:'1',enabled:true}]}});
 assert.doesNotMatch(escaped,/<id>/);assert.match(escaped,/&lt;id&gt;/);
});

test('Telegram replaces bearer pairing with identity-bound admin approval and separate tool limits', () => {
 const html=channelSections({config:{connections:[{id:'bot',source:'telegram',enabled:true,application_id:'1'}]},telegram_access:{requests:[{id:'r',code:'ABCD2345',display_name:'<admin>',target:{application_id:'1',chat_id:'-2'},person_id:'telegram:user:9',kind:'group',expires_at:'2026-11-04'}]}});
 const telegram=html.split('data-channel="lark"')[0];
 assert.doesNotMatch(telegram,/channel-code|\/pair|<admin>/);
 assert.match(telegram,/ABCD2345|telegram-access-approve/);assert.match(telegram,/&lt;admin&gt;/);
 assert.match(telegram,/tg-request-whole-0/);assert.doesNotMatch(telegram,/tg-request-whole-0[^>]*checked/);
 const allowed=telegramAccessSection({access:[{target:{chat_id:'90'},kind:'private',enabled:true,members:['telegram:user:9'],space_permission:'full',people:[{id:'telegram:user:9',mode:'none'}],effective_permissions:{'telegram:user:9':'none'}}]});
 assert.match(allowed,/空间权限 full/);assert.match(allowed,/人员 none → 最终 none/);assert.match(allowed,/停用访问/);
});
