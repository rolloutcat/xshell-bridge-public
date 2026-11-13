'use strict';
// Production inline code, snapshot-success packets and user events only. Clock
// control models missed polling; no UI eligibility/state variables are forced.
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const id=n=>n.toString(16).padStart(48,'0');
const fallback={id:1,ok:true,data:{bridge_version:'fixture-bridge',desktop_version:'fixture-desktop',supports_labels:true,supports_session_removal:true,read_only:false,jobs:[],grants:[],rules:[],history_sessions:[],sessions:['blocked','detached','expired','active','pending','blocked'].map((state,i)=>({id:id(i+1),generation:id(i+101),name:'fixture <'+state+'>',environment:'synthetic',fingerprint:'fixture-'+i,state,last_seen:'2026-11-06T01:00:00Z',created_at:'2026-11-05T23:59:59Z'}))}};
const packet=process.env.XSH_REMOVAL_SNAPSHOT_FIXTURE?JSON.parse(fs.readFileSync(process.env.XSH_REMOVAL_SNAPSHOT_FIXTURE,'utf8')):fallback;
assert.equal(packet.ok,true);assert.equal(packet.data.sessions.length,6);assert.equal(packet.data.supports_session_removal,true);
let source=fs.readFileSync(process.env.XSH_REMOVAL_APP_HTML||'desktop/app.html','utf8').match(/<script>([\s\S]*?)<\/script>/)[1];
for(const [marker,file] of [['/*__ATTACHMENT_STATE__*/','desktop/attachment-state.js'],['/*__UPDATES_UI__*/','desktop/updates-ui.js']])source=source.replace(marker,()=>fs.readFileSync(file,'utf8'));
const clone=x=>JSON.parse(JSON.stringify(x)),elements=new Map(),handlers={},receivers=[],sent=[],pending=new Map(),intervals=[];
let now=Date.now();class Clock extends Date{constructor(...args){super(...(args.length?args:[now]));}static now(){return now;}}
function el(id){if(!elements.has(id))elements.set(id,{id,dataset:{},value:'',innerHTML:'',textContent:'',disabled:false,open:false,classList:{toggle(){}},listeners:{},addEventListener(t,f){this.listeners[t]=f;},showModal(){this.open=true;},close(){this.open=false;},closest(){return this;},hasAttribute(k){return k==='data-close'&&this.dataset.close!==undefined;},before(){},remove(){},insertAdjacentHTML(_,s){this.innerHTML=s+this.innerHTML;},scrollIntoView(){}});return elements.get(id);}
const context=vm.createContext({console,Date:Clock,document:{getElementById:el,createElement(){return el('created');},querySelectorAll(){return [];},addEventListener(t,f){(handlers[t]??=[]).push(f);}},window:{chrome:{webview:{addEventListener(_,f){receivers.push(f);},postMessage(raw){const q=JSON.parse(raw);sent.push(q);pending.set(q.id,q);}}}},setTimeout(){return 1;},clearTimeout(){},setInterval(fn,ms){intervals.push({fn,ms});return intervals.length;}});
const tick=async()=>{for(let i=0;i<40;i++)await Promise.resolve();};
const request=op=>[...pending.values()].findLast(q=>q.op===op);
async function reply(q,data,ok=true){assert.ok(q,'missing request');assert.ok(pending.delete(q.id),'duplicate response');receivers.forEach(f=>f({data:{id:q.id,ok,...(ok?{data:clone(data)}:{error:data})}}));await tick();}
const content=()=>el('content').innerHTML;
function button(key,value,body=content()){const buttons=[...body.matchAll(/<button\b([^>]*)>([\s\S]*?)<\/button>/g)];const match=buttons.find(m=>m[1].includes(`${key}="${value}"`));assert.ok(match,`missing button ${key}=${value}`);return {disabled:/\bdisabled\b/.test(match[1]),text:match[2]};}
function enabled(value,expected=true){assert.equal(button('data-remove-session',value).disabled,!expected);}
function cleanupEnabled(expected=true){assert.equal(button('id','session-cleanup').disabled,!expected);}
async function click(id,dataset={},disabled=false){const b=el(id);b.dataset=dataset;b.disabled=disabled;for(const fn of handlers.click||[])fn({target:b,preventDefault(){}});await tick();}
async function refresh(data=packet.data,ok=true){await click('refresh');await reply(request('snapshot'),data,ok);}
async function close(){await click('close',{close:''});await reply(request('cancelReview'),{cancelled:true});}
function mutations(){return sent.filter(q=>['sessionRemovalPrepare','sessionRemovalConfirm'].includes(q.op)).length;}
async function prepare(data,items,stop=false){
 const action=stop?'remove':'cleanup';await click(stop?'remove':'session-cleanup',stop?{removeSession:items[0].id}:{});
 const q=request('sessionRemovalPrepare');assert.equal(q.action,action);
 const preview=items.map(s=>({...s,jobs:data.jobs.filter(j=>j.session_id===s.id).map(({id,status})=>({id,status}))}));
 await reply(q,{ticket:'single-use',items:preview,count:preview.length,stop_active:stop,expires:'2099-01-01',notice:'保留任务历史及输出'});
 return request('sessionRemovalConfirm');
}
(async()=>{
 vm.runInContext(source,context);
 await reply(request('snapshot'),packet.data);
 await click('sessions',{page:'sessions'});
 cleanupEnabled();packet.data.sessions.forEach(s=>enabled(s.id));
 const active=packet.data.sessions.find(s=>s.state==='active');assert.ok(button('data-edit-label',active.id));
 // Poll using the production callback and the actual native snapshot-success data.
 intervals.find(i=>i.ms===2000).fn();await reply(request('snapshot'),packet.data);cleanupEnabled();
 await click('edit',{editLabel:active.id});cleanupEnabled(false);
 el('label-name').value=active.name;el('label-environment').value=active.environment;
 await click('save-label');await reply(request('labelSession'),{recorded:true});
 await reply(request('snapshot'),packet.data);assert.equal(el('modal').open,false);
 cleanupEnabled();packet.data.sessions.forEach(s=>enabled(s.id));
 await click('edit',{editLabel:active.id});await close();cleanupEnabled();
 // One active member can stop-and-remove; a duplicate click cannot replay it.
 await prepare(packet.data,[active],true);assert.match(el('modal-body').innerHTML,/停止 Bridge 接入/);
 await click('session-removal-confirm');await click('session-removal-confirm');
 assert.equal(sent.filter(q=>q.op==='sessionRemovalConfirm').length,1);
 const archived=clone(packet.data);archived.sessions=archived.sessions.filter(s=>s.id!==active.id);archived.history_sessions=[{...active,archived:true}];
 await reply(request('sessionRemovalConfirm'),{recorded:true,ids:[active.id]});await reply(request('snapshot'),archived);
 assert.ok(!content().includes(`data-remove-session="${active.id}"`));await close();
 // Busy jobs block individual and all-or-none batch cleanup, unknown does not.
 for(const status of ['pending','queued','dispatched','running','unknown']){
  const data=clone(packet.data),target=data.sessions.find(s=>s.state==='blocked');data.jobs=[{id:id(200),session_id:target.id,status}];
  await refresh(data);enabled(target.id,status==='unknown');cleanupEnabled(status==='unknown');
  if(status!=='unknown'){assert.match(content(),/待审批|排队|派发|执行/);const count=mutations();await click('remove',{removeSession:target.id},button('data-remove-session',target.id).disabled);await click('session-cleanup',{},button('id','session-cleanup').disabled);assert.equal(mutations(),count);}
  else{const inactive=data.sessions.filter(s=>['detached','blocked','expired'].includes(s.state));await prepare(data,inactive);assert.ok(!el('modal-body').innerHTML.includes(active.id));await click('session-removal-confirm');await reply(request('sessionRemovalConfirm'),'synthetic uncertain result',false);await reply(request('snapshot'),data);const confirms=sent.filter(q=>q.op==='sessionRemovalConfirm').length;await click('session-removal-confirm');assert.equal(sent.filter(q=>q.op==='sessionRemovalConfirm').length,confirms);await close();}
 }
 // State/generation/history changes invalidate a frozen preview on polling.
 for(const field of ['state','generation','jobs','read_only','supports_session_removal']){
  const data=clone(packet.data),target=data.sessions.find(s=>s.state==='detached');await refresh(data);await prepare(data,[target]);
  if(field==='jobs')data.jobs=[{id:id(201),session_id:target.id,status:'unknown'}];else if(field==='read_only')data.read_only=true;else if(field==='supports_session_removal')data.supports_session_removal='true';else data.sessions.find(s=>s.id===target.id)[field]=field==='state'?'active':id(999);
  await refresh(data);assert.equal(el('session-removal-confirm').disabled,true);const count=mutations();await click('session-removal-confirm',{},true);assert.equal(mutations(),count);assert.match(el('modal-error').textContent,/失效/);await close();
 }
 for(const [extra,explanation] of [[{read_only:true},/只读|操作员/],[{supports_session_removal:false},/不支持.*清理/],[{supports_session_removal:'true'},/不支持.*清理/]]){
  await refresh({...packet.data,...extra});cleanupEnabled(false);packet.data.sessions.forEach(s=>enabled(s.id,false));assert.match(content(),explanation);
  if(extra.supports_session_removal!==undefined){assert.ok(content().includes(packet.data.bridge_version));assert.ok(content().includes(packet.data.desktop_version));assert.match(content(),/升级/);}
 }
 await refresh();await prepare(packet.data,packet.data.sessions.filter(s=>['blocked','detached','expired'].includes(s.state)));
 now+=11000;intervals.find(i=>i.ms===2000).fn();cleanupEnabled(false);assert.match(content(),/快照.*过期|快照.*超过/);assert.equal(el('session-removal-confirm').disabled,true);
 await reply(request('snapshot'),packet.data);cleanupEnabled();assert.equal(el('session-removal-confirm').disabled,true,'fresh polling must not revive invalidated confirmation');await close();
 await refresh('isolated offline',false);cleanupEnabled(false);assert.match(content(),/连接不可用|离线/);await refresh();cleanupEnabled();
 // Date-aware descending attachment order, stable ID ties, unknown last.
 const sorted=clone(packet.data),[a,b,c,d,e,f]=sorted.sessions;
 Object.assign(a,{created_at:'2026-11-05T23:59:59Z',last_seen:'2026-11-07T00:00:00Z'});
 Object.assign(b,{created_at:'2026-11-06T00:00:01Z'});
 Object.assign(c,{created_at:'2026-11-06T00:00:01Z'});
 Object.assign(d,{last_seen:'2026-11-05T23:59:58Z'});delete d.created_at;
 Object.assign(e,{created_at:'',last_seen:''});Object.assign(f,{created_at:'0001-01-01T00:00:00Z',last_seen:'invalid'});
 const byId=(a,b)=>a.id<b.id?-1:a.id>b.id?1:0;
 const order=[b,c].sort(byId).concat([a,d],[e,f].sort(byId)).map(s=>s.id);
 const rows=()=>[...content().matchAll(/data-remove-session="([^"]+)"/g)].map(m=>m[1]);
 await refresh(sorted);assert.deepEqual(rows(),order);assert.match(content(),/2026/);assert.match(content(),/旧记录|历史记录/);assert.match(content(),/时间未知/);
 const originalIds=sorted.sessions.map(s=>s.id);a.last_seen='2026-11-08T00:00:00Z';a.name='renamed';await refresh(sorted);assert.deepEqual(rows(),order);assert.deepEqual(sorted.sessions.map(s=>s.id),originalIds);
 // Go emits nanoseconds: distinct attachments in one millisecond are not ties.
 const [older,newer]=[b,c].sort(byId);older.created_at='2026-11-06T00:00:01.123000001Z';newer.created_at='2026-11-06T00:00:01.123000002Z';
 await refresh(sorted);assert.deepEqual(rows(),[newer.id,older.id,...order.slice(2)]);
 console.log('PASS: six-terminal native snapshot/poll, unchanged label close recovery, removal guards, visible diagnostics, date-aware stable attachment ordering');
})().catch(e=>{console.error(e);process.exitCode=1;});
