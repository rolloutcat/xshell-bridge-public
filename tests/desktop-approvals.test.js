'use strict';
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
let source=fs.readFileSync('desktop/app.html','utf8').match(/<script>([\s\S]*?)<\/script>/)[1];
for(const [marker,file] of [['/*__ATTACHMENT_STATE__*/','desktop/attachment-state.js'],['/*__UPDATES_UI__*/','desktop/updates-ui.js']])source=source.replace(marker,()=>fs.readFileSync(file,'utf8'));
const elements=new Map(),handlers={},captures={},sent=[],receivers=[],timers=[];
function el(id){if(!elements.has(id))elements.set(id,{id,value:'',dataset:{},innerHTML:'',textContent:'',checked:false,disabled:false,classList:{toggle(){}},listeners:{},addEventListener(t,f){this.listeners[t]=f;},showModal(){this.open=true;},close(){this.open=false;},closest(){return this;},hasAttribute(k){return k==='data-close'&&this.dataset.close!==undefined;},insertAdjacentHTML(_,s){this.innerHTML=s+this.innerHTML;},before(){},remove(){},scrollIntoView(){}});return elements.get(id);}
const context=vm.createContext({console,TextEncoder,document:{getElementById:el,createElement(){return el('created');},querySelectorAll(){return [];},addEventListener(t,f,capture){((capture?captures:handlers)[t]??=[]).push(f);}},window:{chrome:{webview:{addEventListener(_,f){receivers.push(f);},postMessage(s){sent.push(JSON.parse(s));}}}},setTimeout(fn,ms){timers.push({fn,ms});return timers.length;},clearTimeout(){},setInterval(){return 1;}});
const run=s=>vm.runInContext(s,context),tick=async()=>{for(let i=0;i<30;i++)await Promise.resolve();};
function reply(q,data,ok=true){receivers.forEach(f=>f({data:ok?{id:q.id,ok,data}:{id:q.id,ok,error:data}}));}
async function click(id,dataset={}){const b=el(id);b.dataset=dataset;let stopped=false;const e={target:b,preventDefault(){},stopImmediatePropagation(){stopped=true;}};for(const f of captures.click||[])f(e);if(!stopped)for(const f of handlers.click||[])f(e);await tick();}
const id=n=>n.toString(16).padStart(48,'0'),s={id:id(100),generation:id(101),state:'active',name:'<target>',environment:'A&B',fingerprint:'{"host":"<host>"}'},jobs=Array.from({length:22},(_,i)=>({id:id(i+1),session_id:s.id,generation:s.generation,status:'pending',command:'ps -eo pid,comm,pcpu,pmem --sort=-pcpu',cwd:'/',timeout_ms:15000}));
const snapshot=extra=>({sessions:[s],jobs,grants:[],rules:[],read_only:false,supports_rules:true,supports_batch:true,supports_grants:true,supports_explain:true,supports_displayed_approval:true,approval_context:"fixture-context",supports_builtin_policy:true,builtin_policy:{id:"builtin-readonly-v5",version:5,revision:0,enabled:true},...extra});
async function refresh(data,ok=true){run('refresh()');reply(sent.findLast(q=>q.op==='snapshot'),data,ok);await tick();}
(async()=>{run(source);reply(sent[0],snapshot());await tick();
// A script remains approvable once, while its non-reusable scope is explicit.
await refresh(snapshot({jobs:[{...jobs[0],command:'python3 -c "print(1)"',requires_exact_approval:true}]}));
assert.match(el('content').innerHTML,/此脚本或动态命令须逐次人工审批/);
assert.match(el('content').innerHTML,/data-decide="remember"[^>]*disabled/);
assert.doesNotMatch(el('content').innerHTML,/data-decide="allow"[^>]*disabled/);
await refresh(snapshot());
// Filtering must not select or approve tasks, including across pages.
assert.equal(el('status-filter-label').hidden,true,'approvals do not offer unrelated statuses');
await click('nav-jobs',{page:'jobs'});
assert.match(el('status-filter').innerHTML,/value="queued"/);assert.match(el('status-filter').innerHTML,/value="dispatched"/);
assert.doesNotMatch(el('status-filter').innerHTML,/value="active"/);
await refresh(snapshot({jobs:[{...jobs[0],status:'queued'},{...jobs[1],status:'dispatched'},jobs[2]]}));
el('status-filter').value='queued';run("viewVersion='';render()");assert.match(el('content').innerHTML,/当前显示 1 \/ 3 个任务/);assert.match(el('content').innerHTML,/排队中/);assert.doesNotMatch(el('content').innerHTML,/已派发/);
el('search').value='not-present';run("viewVersion='';render()");assert.match(el('content').innerHTML,/当前显示 0 \/ 3 个任务/);assert.match(el('content').innerHTML,/data-clear-filters/);await click('clear-jobs',{clearFilters:''});assert.match(el('content').innerHTML,/当前显示 3 \/ 3 个任务/);
await click('nav-sessions',{page:'sessions'});
assert.match(el('status-filter').innerHTML,/value="active"/);assert.match(el('status-filter').innerHTML,/value="blocked"/);
assert.doesNotMatch(el('status-filter').innerHTML,/value="queued"/);
el('status-filter').value='blocked';run("viewVersion='';render()");assert.match(el('content').innerHTML,/当前显示 0 \/ 1 个终端/);assert.match(el('content').innerHTML,/data-clear-filters/);
await click('nav-approvals',{page:'approvals'});
assert.equal(el('status-filter').value,'');await refresh(snapshot());
const longText=('中文😀\n').repeat(100);await refresh(snapshot({jobs:[{...jobs[0],command:longText}]}));
assert.match(el('content').innerHTML,new RegExp(Buffer.byteLength(longText,'utf8')+' 字节 · 101 行'));
assert.match(el('content').innerHTML,/data-command-expand/);assert.ok(el('content').innerHTML.includes(longText));
assert.equal(run(`utf8Bytes(${JSON.stringify('😀中\ud800')})`),Buffer.byteLength('😀中\ud800','utf8'));
const exactText='\n中文😀\r\nnext\rfinal',fakePre={textContent:'parser-normalized',closest(){return {dataset:{jobId:jobs[0].id}}}};
await refresh(snapshot({jobs:[{...jobs[0],command:exactText}]}));
el('content').querySelectorAll=selector=>selector==='pre.approval-command'?[fakePre]:[];run("restoreApprovalText($('content'))");
assert.equal(fakePre.textContent,exactText,'HTML pre parsing must not alter the reviewed command');el('content').querySelectorAll=()=>[];
await refresh(snapshot({jobs:[{...jobs[0],command:longText}]}));
run(`batchSelection.add(${JSON.stringify(jobs[0].id)})`);
await refresh(snapshot({jobs:[{...jobs[0],command:longText+'changed'}]}));assert.equal(run('batchSelection.size'),0,'changed command clears the old selection');
run(`batchSelection.add(${JSON.stringify(jobs[0].id)})`);
await refresh(snapshot({jobs:[{...jobs[0],command:longText+'changed',request_id:'changed-request'}]}));assert.equal(run('batchSelection.size'),0,'changed submission clears the old selection');
await refresh(snapshot());
const boundCheckbox={dataset:{batchId:jobs[0].id},checked:true};for(const f of handlers.change||[])f({target:boundCheckbox});assert.equal(run('batchSelection.size'),1);
run("setPage('jobs')");await refresh(snapshot({jobs:[{...jobs[0],request_id:'changed-away-from-approval'}]}));assert.equal(run('batchSelection.size'),0,'changed submission clears selection even while on another page');
run("setPage('approvals')");await refresh(snapshot());
assert.equal(run('batchSelection.size'),0);assert.equal(sent.filter(q=>q.op==='displayedDecide').length,0);
assert.match(el('content').innerHTML,/默认基础只读检查/);assert.match(el('content').innerHTML,/批准一次/);assert.match(el('content').innerHTML,/批准并记住 30 分钟/);assert.match(el('content').innerHTML,/&lt;host&gt;/);
assert.match(el('content').innerHTML,/v5/);assert.match(el('content').innerHTML,/ip route show/);assert.match(el('content').innerHTML,/<details[^>]*>/);
// The actual identity is visible before any details are expanded; labels alone are insufficient.
assert.match(el('content').innerHTML, /class="approval-identity"[\s\S]*?&lt;host&gt;[\s\S]*?<details/);
assert.match(el('content').innerHTML, /当前接入代次[\s\S]*?完全相同的命令、目录和超时/);
el('search').value='not-present';run("viewVersion='';render()");assert.match(el('content').innerHTML,/没有符合筛选条件/);assert.match(el('content').innerHTML,/data-clear-filters/);await click('clear-empty',{clearFilters:''});assert.equal(el('search').value,'');
// A refresh in flight must prevent approving an older displayed snapshot.
run('refresh()');const pendingRefresh=sent.findLast(q=>q.op==='snapshot');const beforeRefreshDecision=sent.length;await click('single',{decide:'allow',id:jobs[0].id});assert.equal(sent.length,beforeRefreshDecision);reply(pendingRefresh,snapshot());await tick();
// A slow poll must preserve selected command text, while stale cards cannot authorize.
let removed=false;const selected={isCollapsed:false,anchorNode:{},removeAllRanges(){removed=true;this.isCollapsed=true;}};context.window.getSelection=()=>selected;el('content').contains=()=>true;run('refresh()');const slowRefresh=sent.findLast(q=>q.op==='snapshot');timers.findLast(t=>t.ms===250).fn();assert.equal(removed,false);reply(slowRefresh,snapshot({jobs:jobs.map((j,i)=>i?j:{...j,command:'changed'})}));await tick();const whileReading=sent.length;await click('single',{decide:'allow',id:jobs[0].id});assert.equal(sent.length,whileReading);selected.isCollapsed=true;for(const f of handlers.selectionchange||[])f({});assert.match(el('content').innerHTML,/changed/);context.window.getSelection=undefined;el('content').contains=()=>false;await refresh(snapshot());
// An offline refresh must retain selected text, while all controls in the old view
// (including activation and other non-approval actions) are unavailable.
const oldControl={disabled:false};el('content').querySelectorAll=selector=>selector==='button,input,select,textarea'?[oldControl]:[];
selected.isCollapsed=false;removed=false;context.window.getSelection=()=>selected;el('content').contains=()=>true;
await refresh('offline while reading',false);assert.equal(removed,false);assert.equal(oldControl.disabled,true);assert.equal(run('deferredContent'),true);const staleActions=sent.length;await click('activation',{review:'activate',id:s.id});assert.equal(sent.length,staleActions);
selected.isCollapsed=true;for(const f of handlers.selectionchange||[])f({});assert.equal(run('deferredContent'),false);
context.window.getSelection=undefined;el('content').contains=()=>false;el('content').querySelectorAll=()=>[];await refresh(snapshot());
// The real Xshell fingerprint is compact but every field remains visible; unknown
// formats fall back to the complete original identity rather than guessing.
const realFingerprint=JSON.stringify({Path:'C:\\sessions\\one.xsh',RemoteAddress:'host-a',RemotePort:'2222',SessionName:'one',TabText:'tab-a',UserName:'alice',SourceID:'source-a'});
const compact=run(`approvalIdentity(${JSON.stringify({fingerprint:realFingerprint})})`);
for(const part of ['C:\\sessions\\one.xsh','host-a','2222','one','tab-a','alice','source-a'])assert.ok(compact.includes(JSON.stringify(part)));
const agentQuote=v=>'"'+v.replace(/["\\\x00-\x1f\x7f-\uffff]/g,c=>'\\u'+c.charCodeAt(0).toString(16).padStart(4,'0'))+'"';
const agentFields={Path:'C:\\终端.xsh',RemoteAddress:'host-a',RemotePort:'22',SessionName:'one',TabText:'一',UserName:'alice'};
const agentFingerprint='{'+Object.keys(agentFields).map(k=>agentQuote(k)+':'+agentQuote(agentFields[k])).join(',')+'}';
assert.match(run(`approvalIdentity(${JSON.stringify({fingerprint:agentFingerprint})})`),/终端/);
assert.ok(!compact.includes('\n'));assert.equal(run(`approvalIdentity({fingerprint:'opaque'})`),'opaque');
assert.match(run(`approvalIdentity({fingerprint:'{"RemoteAddress":"host-a","Extra":"must-see"}'})`),/must-see/);
const duplicateFingerprint='{"RemoteAddress":"first","RemoteAddress":"second","RemotePort":"22","UserName":"u","SessionName":"s","TabText":"t","Path":"p"}';
assert.equal(run(`approvalIdentity(${JSON.stringify({fingerprint:duplicateFingerprint})})`),duplicateFingerprint);
assert.ok(run(`target(${JSON.stringify({...s,fingerprint:duplicateFingerprint})})`).includes('first'));
assert.match(el('content').innerHTML,/data-batch-return/);
// Rebuilding the long list and batch bar must not lose the page scroll position.
context.window.scrollX=0;context.window.scrollY=600;context.window.scrollTo=(x,y)=>{context.window.scrollX=x;context.window.scrollY=y;};
const insert=el('content').insertAdjacentHTML;el('content').insertAdjacentHTML=function(_,html){this.innerHTML=html+this.innerHTML;context.window.scrollY=0;};
run("viewVersion='';render()");assert.equal(context.window.scrollY,600);el('content').insertAdjacentHTML=insert;
// A newer session with the same ID must never be presented as the old task's target.
await refresh(snapshot({sessions:[{...s,generation:id(102)}]}));assert.match(el('content').innerHTML,/接入代次不匹配/);const beforeMismatch=sent.length;await click('single',{decide:'allow',id:jobs[0].id});await click('batch-select');assert.equal(run('batchSelection.size'),0);assert.equal(sent.length,beforeMismatch);await refresh(snapshot());
await click('single',{decide:'allow',id:jobs[0].id});await click('single',{decide:'allow',id:jobs[0].id});const q=sent.at(-1);assert.equal(q.op,'displayedDecide');assert.deepEqual(q.items,[{job:jobs[0],session:s}]);assert.equal(q.remember,false);assert.equal(sent.filter(x=>x.op==='prepare').length,0);
reply(q,'lost acknowledgement',false);await tick();assert.ok(!el('modal').open);assert.match(el('decision-warning').textContent,/未确认.*原任务/);reply(sent.findLast(q=>q.op==='snapshot'),snapshot());await tick();await click('single',{decide:'allow',id:jobs[0].id});assert.equal(sent.filter(x=>x.op==='displayedDecide').length,1);
await click('batch-select');assert.equal(run('batchSelection.size'),20);await refresh(snapshot({jobs:[...jobs,{...jobs[0],id:id(23)}]}));assert.equal(run('batchSelection.size'),20);assert.equal(run(`batchSelection.has('${id(23)}')`),false);const capped={dataset:{batchId:id(23)},checked:true};for(const f of handlers.change||[])f({target:capped});assert.equal(capped.checked,false);el('search').value='missing';run("viewVersion='';render()");assert.match(el('content').innerHTML,/筛选隐藏 20/);const before=sent.length;await click('batch',{batchAction:'allow'});assert.equal(sent.length,before);el('status-filter').value='completed';await click('nav-jobs',{page:'jobs'});assert.equal(el('search').value,'');assert.equal(el('status-filter').value,'');assert.equal(run('batchSelection.size'),0);await click('nav-approvals',{page:'approvals'});assert.equal(run('batchSelection.size'),0);assert.equal(el('search').value,'missing');assert.equal(sent.length,before);
await click('clear-approval',{clearFilters:''});await click('batch-clear');for(const j of jobs.slice(1,4))for(const f of handlers.change||[])f({target:{dataset:{batchId:j.id},checked:true}});await tick();
await click('batch',{batchAction:'deny'});const batch=sent.at(-1);assert.equal(batch.op,'displayedDecide');assert.equal(batch.items.length,3);assert.equal(batch.action,'deny');assert.equal(batch.remember,false);assert.deepEqual(batch.items.map(x=>x.job.id),jobs.slice(1,4).map(x=>x.id));
reply(batch,{recorded:true,ids:batch.items.map(x=>x.job.id)});await tick();assert.ok(!el('modal').open);assert.match(el('feedback').textContent,/决定已记录/);assert.equal(run('batchSelection.size'),0);assert.match(el('decision-warning').textContent,/未确认/);assert.equal(context.window.scrollY,600);reply(sent.findLast(q=>q.op==='snapshot'),snapshot({jobs:jobs.map((j,i)=>i>0&&i<4?{...j,status:'denied'}:j)}));await tick();assert.doesNotMatch(el('content').innerHTML,new RegExp('data-job-id="'+jobs[1].id+'"'));
await refresh(snapshot());
await click('single',{decide:'remember',id:jobs[4].id});const remember=sent.at(-1);assert.equal(remember.op,'displayedDecide');assert.equal(remember.remember,true);reply(remember,{recorded:true,ids:[jobs[4].id]});await tick();assert.ok(!el('modal').open);assert.match(el('feedback').textContent,/决定已记录/);reply(sent.findLast(q=>q.op==='snapshot'),snapshot());await tick();
await click('policy-toggle');const policy=sent.at(-1);assert.equal(policy.op,'builtinPolicySet');assert.equal(policy.enabled,false);assert.deepEqual(policy.expected,snapshot().builtin_policy);await click('policy-toggle');assert.equal(sent.filter(x=>x.op==='builtinPolicySet').length,1);reply(policy,{recorded:true,message:'已停用'});await tick();reply(sent.findLast(q=>q.op==='snapshot'),snapshot({builtin_policy:{...snapshot().builtin_policy,enabled:false,revision:1}}));await tick();assert.match(el('content').innerHTML,/已停用/);
await click('policy-toggle');const enable=sent.at(-1);assert.equal(enable.enabled,true);reply(enable,{recorded:true,message:'已启用'});await tick();reply(sent.findLast(q=>q.op==='snapshot'),snapshot());await tick();
// A text selection can defer DOM repaint while a snapshot updates state. Toggle
// must still mean what the visible button says, using its original revision.
run('state.builtin_policy={...state.builtin_policy,enabled:false,revision:99}');await click('policy-toggle');const visiblePolicy=sent.at(-1);assert.equal(visiblePolicy.enabled,false);assert.equal(visiblePolicy.expected.revision,0);reply(visiblePolicy,'stale policy; no mutation',false);await tick();reply(sent.findLast(q=>q.op==='snapshot'),snapshot());await tick();
await refresh(snapshot({read_only:true}));const n=sent.length;await click('single',{decide:'allow',id:jobs[5].id});await click('policy-toggle');await click('batch',{batchAction:'deny'});assert.equal(sent.length,n);
await click('explain',{explain:jobs[0].id});const explain=sent.at(-1);reply(explain,{job:{...jobs[0],builtin_rule_id:'builtin-readonly-v1/ps',approval_source:'builtin'},explanation:{decision:'builtin',checks:[{kind:'builtin',name:'<rule>',matched:false,message:'A&B'}]},notice:'notice'});await tick();assert.match(el('modal-body').innerHTML,/&lt;rule&gt;/);assert.match(el('modal-body').innerHTML,/原默认规则 ID/);
await click('explain',{explain:jobs[0].id});reply(sent.at(-1),{job:jobs[0],explanation:{decision:'blocked',reason_code:'command_denied',message:'禁止删除',checks:[]},notice:'notice'});await tick();assert.match(el('modal-body').innerHTML,/危险策略拒绝/);assert.doesNotMatch(el('modal-body').innerHTML,/目标不可用/);
await click('explain',{explain:jobs[0].id});reply(sent.at(-1),{job:jobs[0],explanation:{decision:'manual',reason_code:'exact_approval_required',message:'需要核对脚本',checks:[]},notice:'notice'});await tick();assert.match(el('modal-body').innerHTML,/需要人工审批/);assert.doesNotMatch(el('modal-body').innerHTML,/危险策略拒绝/);
run("$('modal').close();modalKind=''");
await refresh(snapshot({builtin_policy:{id:'builtin-readonly-v1',version:1,revision:0,enabled:true}}));assert.match(el('content').innerHTML,/后台需更新/);const oldCount=sent.length;await click('policy-toggle');assert.equal(sent.length,oldCount);
await refresh(snapshot({builtin_policy:{id:'builtin-readonly-v4',version:4,revision:0,enabled:true}}));assert.match(el('content').innerHTML,/后台需更新/);const v4Count=sent.length;await click('policy-toggle');assert.equal(sent.length,v4Count);
await refresh(snapshot({builtin_policy:{id:'builtin-readonly-v3',version:3,revision:0,enabled:true}}));assert.match(el('content').innerHTML,/后台需更新/);const v3Count=sent.length;await click('policy-toggle');assert.equal(sent.length,v3Count);
await refresh(snapshot({supports_displayed_approval:false,supports_builtin_policy:false}));const legacy=sent.length;await click('single',{decide:'allow',id:jobs[6].id});await click('policy-toggle');assert.equal(sent.length,legacy);assert.match(el('content').innerHTML,/更新后台/);
await refresh(snapshot());run('snapshotTime=Date.now()-20000');const stale=sent.length;await click('single',{decide:'allow',id:jobs[7].id});await click('policy-toggle');assert.equal(sent.length,stale);
await refresh(snapshot());await click('single',{decide:'allow',id:jobs[8].id});const certified=sent.at(-1);assert.equal(certified.op,'displayedDecide');receivers.forEach(f=>f({data:{id:certified.id,ok:false,code:'DECISION_NOT_SENT',error:'display changed; no write'}}));await tick();assert.equal(run(`spentDecisions.has('${jobs[8].id}')`),false);assert.match(el('feedback').textContent,/没有发送审批决定/);reply(sent.findLast(q=>q.op==='snapshot'),snapshot());await tick();
// No ACK at all: the local timeout and even a late ACK must not cause replay.
await click('single',{decide:'allow',id:jobs[9].id});const timedOut=sent.at(-1),decisionsBeforeTimeout=sent.filter(q=>q.op==='displayedDecide').length;timers.findLast(t=>t.ms===45000).fn();await tick();assert.match(el('decision-warning').textContent,/未确认.*原任务/);reply(sent.findLast(q=>q.op==='snapshot'),snapshot());await tick();reply(timedOut,{recorded:true,ids:[jobs[9].id]});await click('single',{decide:'allow',id:jobs[9].id});assert.equal(sent.filter(q=>q.op==='displayedDecide').length,decisionsBeforeTimeout);
await click('single',{decide:'allow',id:jobs[10].id});const single=sent.at(-1);reply(single,{recorded:true,ids:[jobs[10].id]});await tick();assert.ok(!el('modal').open);assert.match(el('feedback').textContent,/决定已记录/);assert.match(el('decision-warning').textContent,/未确认/);reply(sent.findLast(q=>q.op==='snapshot'),snapshot());await tick();
await click('single',{decide:'deny',id:jobs[11].id});const lateDecision=sent.at(-1);run("openModal('new','sentinel-decision','','details')");reply(lateDecision,{recorded:true,ids:[jobs[11].id]});await tick();assert.equal(el('modal-body').innerHTML,'sentinel-decision');assert.equal(el('modal').open,true);reply(sent.findLast(q=>q.op==='snapshot'),snapshot());await tick();run("$('modal').close();modalKind=''");
await click('single',{decide:'allow',id:jobs[12].id});const staleDecision=sent.at(-1),staleCount=sent.filter(q=>q.op==='displayedDecide').length;run("connectionGeneration++;mutating=false;openModal('new context','sentinel-context','','details')");reply(staleDecision,{recorded:true,ids:[jobs[12].id]});await tick();assert.equal(el('modal-body').innerHTML,'sentinel-context');assert.equal(el('modal').open,true);run("$('modal').close();modalKind=''");await refresh(snapshot());await click('single',{decide:'allow',id:jobs[12].id});assert.equal(sent.filter(q=>q.op==='displayedDecide').length,staleCount);
await refresh(snapshot());await click('explain',{explain:jobs[0].id});const staleExplain=sent.at(-1);run("openModal('other','sentinel','','details')");reply(staleExplain,{job:jobs[0],explanation:{checks:[]}});await tick();assert.equal(el('modal-body').innerHTML,'sentinel');
await click('explain',{explain:jobs[0].id});const offlineExplain=sent.at(-1);await refresh('offline',false);reply(offlineExplain,{job:jobs[0],explanation:{checks:[]}});await tick();assert.match(el('modal-body').innerHTML,/失效/);assert.equal(run('batchSelection.size'),0);
await refresh(snapshot());await click('explain',{explain:jobs[0].id});const otherContext=sent.at(-1);run('connectionGeneration++;render()');reply(otherContext,{job:jobs[0],explanation:{checks:[]}});await tick();assert.match(el('modal-body').innerHTML,/失效/);
await refresh(snapshot({supports_batch:false,supports_explain:false}));const unsupported=sent.length;await click('batch-select');await click('batch',{batchAction:'allow'});await click('explain',{explain:jobs[0].id});assert.equal(sent.length,unsupported);assert.match(el('error').textContent,/升级/);
await refresh(snapshot());run("$('modal').close();modalKind='' ");await click('rule-create');el('rule-session').value=s.id;const templateBefore=sent.length;
for(const [i,command] of ['hostname','uptime','free -h','df -h','uname -sr','ps -eo pid,comm,pcpu,pmem --sort=-pcpu'].entries()){el('command-template').value=String(i);await click('apply-template');assert.equal(el('rule-name').value,command);assert.equal(el('rule-program').value,command.split(' ')[0]);assert.equal(el('rule-patterns').value,command.split(' ').slice(1).join('\n'));assert.equal(el('rule-session').value,s.id);}assert.equal(sent.length,templateBefore);
console.log('PASS: displayed one-click snapshots, visible bounded batch, no shifting selection, lost ack, stale replies, policy toggle and observer guards');
})().catch(e=>{console.error(e);process.exitCode=1;});
