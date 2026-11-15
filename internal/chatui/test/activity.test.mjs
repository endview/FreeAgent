import {test} from 'node:test';
import assert from 'node:assert/strict';
import {activityState,attentionState,recentActivity,trackActivityUpdates} from '../frontend/lib/presentation/activity.mjs';

test('three-row overview prioritizes unresolved states before recent success; recovered attempts keep evidence without stale attention',()=>{
  const rows=Array.from({length:1000},(_,i)=>({key:String(i),sequence:i+1,status:'finished'}));
  rows[3].status='unknown';rows[4].is_error=true;rows[8].status='pending';
  rows[999].status='unknown';rows[999].replacement='retry';
  const tracked=trackActivityUpdates(rows);
  assert.deepEqual(recentActivity(rows,'awaiting_approval',tracked).map(x=>x.key),['8','4','3']);
  assert.equal(activityState(rows[999]),'retried');assert.equal(attentionState(activityState(rows[999])),false);
  assert.equal(activityState({status:'unknown',resolution:'verified_effect'}),'resolved');
  assert.equal(activityState({status:'unknown',resolution:'handed_off'}),'unknown');
  assert.equal(activityState({status:'unknown',resolution:'ignored'}),'ignored');
  assert.equal(activityState({status:'unknown',resolution:'user_confirmed_applied'}),'user_confirmed');
});
test('status changes promote the same operation without duplication; loading older pages does not fake recent updates',()=>{
  let rows=[{key:'a',sequence:40,status:'dispatched'},{key:'b',sequence:41,status:'unknown'},{key:'c',sequence:42,status:'finished'},{key:'d',sequence:43,status:'unknown'}];
  let tracked=trackActivityUpdates(rows);
  rows=rows.map(item=>item.key==='a'?{...item,status:'unknown'}:item);
  tracked=trackActivityUpdates(rows,tracked);
  assert.deepEqual(recentActivity([...rows,rows[0]],'generating',tracked).map(x=>x.key),['a','d','b']);
  rows=[{key:'old',sequence:1,status:'unknown'},...rows];
  tracked=trackActivityUpdates(rows,tracked);
  assert.deepEqual(recentActivity(rows,'generating',tracked).map(x=>x.key),['a','d','b']);
});
