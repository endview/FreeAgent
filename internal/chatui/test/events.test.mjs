import test from 'node:test';
import assert from 'node:assert/strict';
import { eventStream } from '../frontend/lib/presentation/events.mjs';

test('header event stream decodes fragmented UTF-8 and closes without reconnect', async () => {
 const saved=globalThis.fetch;
 let cancel,seenHeaders;
 globalThis.fetch=async (url,options)=>{
  seenHeaders=options.headers;
  return new Response(new ReadableStream({start(controller){
   const bytes=new TextEncoder().encode('data: {"text":"中文"}\n\n');
   for(const byte of bytes) controller.enqueue(new Uint8Array([byte]));
  },cancel(){cancel=true;}}));
 };
 try {
  let client;
  const received=new Promise(resolve=>{client=eventStream('/api/events',{'X-FreeAgent-Key':'fixture'},{open(){},error(){assert.fail('unexpected reconnect')},message({data}){resolve(JSON.parse(data));}});});
  assert.deepEqual(await received,{text:'中文'});
  client.close();
  await new Promise(resolve=>setTimeout(resolve,10));
  assert.equal(seenHeaders['X-FreeAgent-Key'],'fixture');
  // In browsers AbortController interrupts fetch; close is also idempotent.
  client.close();
 } finally {globalThis.fetch=saved;}
});
