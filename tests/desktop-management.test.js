'use strict';
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const html=fs.readFileSync('desktop/app.html','utf8');
const scripts=[...html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/gi)];
assert.equal(scripts.length,1);
let source=scripts[0][1];
for(const [marker,file] of [['/*__ATTACHMENT_STATE__*/','desktop/attachment-state.js'],['/*__UPDATES_UI__*/','desktop/updates-ui.js']]){
 assert.equal(source.split(marker).length,2);
 source=source.replace(marker,()=>fs.readFileSync(file,'utf8'));
}
const elements=new Map(),clicks=[],sent=[],receivers=[];let receive;
function element(id){if(!elements.has(id))elements.set(id,{id,dataset:{},value:'',checked:id==='desktop-notices',innerHTML:'',textContent:'',disabled:false,open:false,classList:{toggle(){}},listeners:{},before(node){elements.set(node.id,node);},addEventListener(t,f){this.listeners[t]=f;},showModal(){this.open=true;},close(){this.open=false;},scrollIntoView(){this.scrolled=true;},closest(){return this;},hasAttribute(k){return k==='data-close'&&this.dataset.close!==undefined;}});return elements.get(id);}
const context=vm.createContext({console,document:{getElementById:element,createElement(){return element('created-'+elements.size);},querySelectorAll(selector){if(selector==='#modal-actions button')return [...element('modal-actions').innerHTML.matchAll(/id="([^"]+)"/g)].map(m=>element(m[1]));return [];},addEventListener(t,f){if(t==='click')clicks.push(f);}},window:{chrome:{webview:{addEventListener(t,f){receivers.push(f);receive=e=>receivers.forEach(fn=>fn(e));},postMessage(s){sent.push(JSON.parse(s));}}}},setTimeout(){return 1;},clearTimeout(){},setInterval(){return 1;}});
const run=s=>vm.runInContext(s,context),tick=async()=>{for(let i=0;i<24;i++)await Promise.resolve();};
function reply(r,data,ok=true){assert.ok(r);receive({data:ok?{id:r.id,ok,data}:{id:r.id,ok,error:data}});}
function snap(extra={}){return {sessions:[],jobs:[],grants:[],read_only:false,supports_grants:true,...extra};}
function strict(r,op,args={}){assert.ok(r.deadline_ms>Date.now());assert.deepEqual(r,{id:r.id,op,...args,deadline_ms:r.deadline_ms});}
async function click(id,dataset={}){const b=element(id);b.dataset=dataset;for(const f of clicks)f({target:b,preventDefault(){}});await tick();}
async function refresh(data){run('refresh()');reply(sent.at(-1),data);await tick();}
(async()=>{
 new vm.Script(source,{filename:'actual-desktop-app.js'}).runInContext(context);
 assert.equal(element('updates').textContent,'程序更新');
 strict(sent[0],'snapshot');reply(sent[0],snap());await tick();
 await click('integrations');strict(sent.at(-1),'integrationStatus');
 function integrationData(status){return {builtin_cli:{path:'C:\\bundle & tools\\xshctl.exe',version:'0.3.0',status:'bundled'},components:['mcp','skill'].map(component=>({component,status,needs_update:status==='installed',packaged_version:'0.3.0',manual_commands:{copy_source:'<source>',copy_target:'A&B',files:[{source:'ref <source>',target:'ref & target'}],commands:['echo <unsafe>'],notice:'manual'}})),notice:'notice',managed_root:'root',config_path:'config',skill_path:'skill'};}
 const rows=integrationData('installed').components;
 reply(sent.at(-1),integrationData('installed'));await tick();
 const body=element('modal-body').innerHTML;
 assert.match(body,/待更新/);assert.match(body,/&lt;source&gt;/);assert.match(body,/A&amp;B/);assert.match(body,/echo &lt;unsafe&gt;/);assert.ok(!body.includes('<unsafe>'));assert.match(body,/不会自动接管/);
 assert.match(body,/ref &lt;source&gt;/);assert.match(body,/ref &amp; target/);
 const builtin=body.match(/<section id="builtin-cli-info">([\s\S]*?)<\/section>/);
 assert.ok(builtin);assert.match(builtin[1],/程序内置 CLI 无需另装/);assert.match(builtin[1],/0\.3\.0/);assert.match(builtin[1],/C:\\bundle &amp; tools\\xshctl\.exe/);assert.ok(!/<button|data-integration-action|data-component=/.test(builtin[1]));
 assert.match(body,/MCP工具接入/);assert.match(body,/Skill操作指南/);
 run('showIntegrations()');const diagnosticData=integrationData('unavailable');
 diagnosticData.components[0].detection={status:'found',source:'package',path:'C:\\Codex & tools\\codex.exe'};
 diagnosticData.components[0].diagnostic='query <failed>';
 reply(sent.at(-1),diagnosticData);await tick();
 assert.match(element('modal-body').innerHTML,/C:\\Codex &amp; tools\\codex\.exe/);
 assert.match(element('modal-body').innerHTML,/query &lt;failed&gt;/);
 assert.ok(!element('modal-body').innerHTML.includes('query <failed>'));
 for(const status of ['absent','installed','modified','conflict','unavailable']){
  run('showIntegrations()');strict(sent.at(-1),'integrationStatus');reply(sent.at(-1),integrationData(status));await tick();
  const rendered=element('modal-body').innerHTML;
  const managed=Array.from(rendered.matchAll(/data-component="([^"]+)"/g),m=>m[1]);
  assert.deepEqual(Array.from(new Set(managed)).sort(),['mcp','skill']);
  assert.equal((rendered.match(/data-integration-action=/g)||[]).length,6);
  for(const component of ['mcp','skill'])for(const action of ['install','update','uninstall']){
   const button=rendered.match(new RegExp('<button data-integration-action="'+action+'" data-component="'+component+'"[^>]*>'));
   assert.ok(button);assert.equal(button[0].includes('disabled'),!(status==='absent'?action==='install':status==='installed'?action!=='install':false));
  }
 }
 assert.ok(!sent.some(r=>r.op==='integrationApply'));
 await click('preview',{integrationAction:'install',component:'mcp'});strict(sent.at(-1),'integrationPreview',{action:'install',component:'mcp'});reply(sent.at(-1),{ticket:'ticket',paths:[],commands:[],files:[{target:'ref <target>',sha256:'abc',content:'\n<unsafe> & text\r\n'}]});await tick();
 assert.match(element('integration-preview').innerHTML,/ref &lt;target&gt;/);assert.match(element('integration-preview').innerHTML,/<pre><span><\/span>\n&lt;unsafe&gt; &amp; text&#13;\n/);
 assert.ok(!sent.some(r=>r.op==='integrationApply'));await click('integration-confirm');strict(sent.at(-1),'integrationApply',{ticket:'ticket'});reply(sent.at(-1),{});await tick();strict(sent.at(-1),'integrationStatus');reply(sent.at(-1),integrationData('installed'));await tick();
 await click('updates');strict(sent.at(-1),'updateStatus');reply(sent.at(-1),{current:'0.3.0'});await tick();
 for(const op of ['updateCheck','updateSource','updateImport']){element('update-path').value='C:\\portable.zip';element('update-hash').value='a'.repeat(64);await click('operation',{updateOp:op});strict(sent.at(-1),op,op==='updateImport'?{path:'C:\\portable.zip',sha256:'a'.repeat(64)}:{});reply(sent.at(-1),{done:true});await tick();}
 // A slower management response cannot replace a newer modal.
 run('showIntegrations()');const oldIntegration=sent.at(-1);run("openModal('new','','','other')");reply(oldIntegration,{components:[]});await tick();assert.equal(run('modalKind'),'other');
 await click('integrations');strict(sent.at(-1),'integrationStatus');reply(sent.at(-1),{components:[]});await tick();
 // Late previews must never create a usable ticket in another modal.
 await click('preview',{integrationAction:'install',component:'mcp'});const stalePreview=sent.at(-1);
 run("openModal('other','','','connections')");reply(stalePreview,{ticket:'stale',paths:[],commands:[]});await tick();
 assert.equal(run('integrationTicket'),null);const beforeApply=sent.length;await click('integration-confirm');assert.equal(sent.length,beforeApply);
 run('showUpdates()');const staleStatus=sent.at(-1);run("openModal('other','','','connections')");reply(staleStatus,{current:'old'});await tick();assert.equal(run('modalKind'),'connections');
 run("openModal('updates','','','updates')");await click('operation',{updateOp:'updateCheck'});const staleUpdate=sent.at(-1);
 run("openModal('other','','','connections')");element('update-result').textContent='sentinel';reply(staleUpdate,{stale:true});await tick();assert.equal(element('update-result').textContent,'sentinel');
 await refresh(snap({jobs:[{id:'notice-job',status:'pending',command:'private-command',session_id:'s'}]}));
 const notices=sent.filter(r=>r.op==='pendingNotice');assert.equal(notices.at(-1).count,1);assert.ok(!JSON.stringify(notices.at(-1)).includes('private-command'));
 const noticeLength=notices.length;run('render();render()');assert.equal(sent.filter(r=>r.op==='pendingNotice').length,noticeLength);
 const decisions=sent.filter(r=>r.op==='confirm').length;receive({data:{op:'openApprovals'}});await tick();for(const r of sent.filter(r=>r.op==='cancelReview'&&run('waiting.has('+r.id+')')))reply(r,{});await tick();assert.equal(element('modal').open,false);assert.equal(run('page'),'approvals');assert.equal(sent.filter(r=>r.op==='confirm').length,decisions);
 element('search').value='draft search';run("page='jobs';render()");assert.equal(element('search').value,'draft search');
 run("openJob({id:'original',command:'echo test',session_id:'s'})");const originalOutput=sent.at(-1);strict(originalOutput,'output',{target:'original',cursor:0});reply(originalOutput,{status:'completed',output:'result',next_cursor:6});await tick();assert.equal(run('outputDrained'),false);run('loadOutput()');reply(sent.at(-1),{status:'completed',output:'',next_cursor:6});await tick();assert.equal(run('outputDrained'),true);assert.equal(element('modal-error').textContent,'');
 // VM event-flow fixtures only: no native OS, filesystem mutation, HTTP, or timer execution.
 // Offline clears both native and in-page pending indicators.
 const fixtureSession={id:'flow-session',generation:'flow-generation',state:'active',name:'Flow',environment:'test',fingerprint:'{}'};
 const fixtureJob={id:'flow-original',session_id:fixtureSession.id,generation:fixtureSession.generation,status:'pending',command:'df -h',cwd:'/',timeout_ms:15000};
 const fixture=()=>snap({supports_rules:true,sessions:[fixtureSession],jobs:[fixtureJob],rules:[]});
 const operations=op=>sent.filter(r=>r.op===op);
 function outstanding(op){return sent.filter(r=>r.op===op&&run('waiting.has('+r.id+')'));}
 function take(op){const requests=outstanding(op);assert.equal(requests.length,1,'exactly one pending '+op);return requests[0];}
 async function answer(op,data,ok=true){reply(take(op),data,ok);await tick();}
 async function observers(data=fixture()){
  for(let pass=0;pass<8;pass++){
   const pending=sent.filter(r=>run('waiting.has('+r.id+')'));
   if(!pending.length)return;
   for(const r of pending){assert.ok(['snapshot','pendingNotice'].includes(r.op),'unexpected unresolved RPC: '+r.op);reply(r,r.op==='snapshot'?data:{});}
   await tick();
  }
  assert.equal(run('waiting.size'),0);
 }
 async function resetFlow(){
  await observers();
  run("mutating=false;refreshing=false;online=true;review=null;ruleTicket=null;integrationTicket=null;updateTicket=null;outputJob='';outputBusy=false;outputDrained=false;outputGeneration++;modalKind='';page='approvals';viewVersion='';noticeSeen.clear();noticeCount=0;noticeContext=connectionGeneration;$('modal').close()");
  element('search').value='';element('status-filter').value='';element('desktop-notices').checked=true;
  await refresh(fixture());await observers();
 }
 async function flowSnapshot(data,ok=true){run('refresh()');await answer('snapshot',data,ok);await observers(data);}
 async function createRulePreview(ticket){
  await click('rule-create');assert.equal(run('modalKind'),'rule-editor');
  for(const [id,value] of Object.entries({'rule-session':fixtureSession.id,'rule-name':'disk rule','rule-program':'df','rule-cwd':'/','rule-timeout':'15','rule-minutes':'30','rule-patterns':'-h'}))element(id).value=value;
  const before=operations('ruleConfirm').length;
  await click('rule-prepare');const request=take('rulePrepare');
  strict(request,'rulePrepare',{spec:{name:'disk rule',session_id:fixtureSession.id,generation:fixtureSession.generation,program:'df',cwd:'/',max_timeout_ms:15000,minutes:30,argument_patterns:['-h']}});
  reply(request,{ticket,session:fixtureSession,spec:request.spec,regex_engine:'RE2',notice:'fixture preview',expires:'2099-01-01'});await tick();
  assert.equal(run('modalKind'),'rule-review');assert.equal(operations('ruleConfirm').length,before);
  assert.match(element('modal-actions').innerHTML,/id="rule-confirm"/);
 }
 await resetFlow();
 const pending=id=>({...fixtureJob,id});
 await flowSnapshot(snap({jobs:[pending('notice-a'),pending('notice-b')]}));
 let start=operations('pendingNotice').length;
 await flowSnapshot(snap({jobs:[pending('notice-b'),pending('notice-c')]}));
 assert.equal(operations('pendingNotice').length,start+1);assert.equal(operations('pendingNotice').at(-1).notify,true);assert.equal(operations('pendingNotice').at(-1).count,2);
 await flowSnapshot(snap({jobs:[pending('notice-c')]}));
 assert.equal(operations('pendingNotice').at(-1).count,1);assert.equal(operations('pendingNotice').at(-1).notify,false);
 await flowSnapshot('fixture offline',false);
 assert.equal(operations('pendingNotice').at(-1).count,0);assert.equal(operations('pendingNotice').at(-1).notify,false);assert.equal(element('pending-banner').hidden,true);assert.equal(element('badge').textContent,'');
 await flowSnapshot(snap({jobs:[pending('notice-c')]}));assert.equal(operations('pendingNotice').at(-1).notify,false);
 await flowSnapshot(fixture());
 const originalState=run('JSON.stringify({jobs:state.jobs,sessions:state.sessions})'),fullStats=element('stats').innerHTML,noticeBeforeSearch=operations('pendingNotice').length;
 await click('jobs-nav',{page:'jobs'});element('search').value='no matching fixture';element('search').listeners.input();
 element('status-filter').value='completed';element('status-filter').listeners.change();
 await click('sessions-nav',{page:'sessions'});
 assert.equal(run('JSON.stringify({jobs:state.jobs,sessions:state.sessions})'),originalState);assert.equal(element('stats').innerHTML,fullStats);assert.equal(operations('pendingNotice').length,noticeBeforeSearch);
 const confirmBeforeNotice=operations('confirm').length;receive({data:{op:'openApprovals'}});await tick();for(const r of sent.filter(r=>r.op==='cancelReview'&&run('waiting.has('+r.id+')')))reply(r,{});await tick();assert.equal(element('modal').open,false);await tick();assert.equal(run('page'),'approvals');assert.equal(operations('confirm').length,confirmBeforeNotice);
 await resetFlow();
 for(const succeeds of [false,true]){
  await createRulePreview('rule-'+succeeds);
  const before=operations('ruleConfirm').length;
  await click('rule-confirm');await click('rule-confirm');assert.equal(operations('ruleConfirm').length,before+1);
  await answer('ruleConfirm',succeeds?{message:'created'}:'fixture rejected',succeeds);await observers();
  assert.equal(run('ruleTicket'),null);await click('rule-confirm');assert.equal(operations('ruleConfirm').length,before+1);
  assert.match(element('modal-actions').innerHTML,/rule-query/);await click('rule-query');await observers();assert.equal(run('page'),'grants');assert.equal(run('modalKind'),'');assert.equal(element('modal').open,false);
 }
 for(const restriction of ['state.read_only=true','online=false']){
  run(restriction);const before=sent.length;await click('rule-create');await click('rule-prepare');await click('approval',{review:'allow',id:fixtureJob.id});assert.equal(sent.length,before);await resetFlow();
 }
 for(const succeeds of [true,false]){
  await resetFlow();const before=operations('confirm').length;
  await click('activation',{review:'activate',id:fixtureSession.id});strict(take('prepare'),'prepare',{action:'activate',target:fixtureSession.id});
  await answer('prepare',{ticket:'activate-'+succeeds,action:'activate',session:fixtureSession,job:null,expires:'2099-01-01'});
  assert.equal(operations('confirm').length,before);assert.match(element('modal-body').innerHTML,/flow-generation/);
  await click('confirm');await click('confirm');assert.equal(operations('confirm').length,before+1);
  await answer('confirm',succeeds?{}:'fixture uncertain',succeeds);await observers();
  assert.equal(element('modal').open,false);
  if(!succeeds){assert.match(element('error').textContent,/未确认.*只读刷新/);await click('confirm');assert.equal(operations('confirm').length,before+1);}
  assert.equal(run('review'),null);
 }
 await resetFlow();run(`openJob(${JSON.stringify(fixtureJob)},${JSON.stringify(fixtureSession)})`);
 strict(take('output'),'output',{target:fixtureJob.id,cursor:0});
 const box=element('output-text');element('follow-output').checked=true;box.scrollHeight=500;box.clientHeight=100;box.scrollTop=390;element('output-bottom').hidden=true;
 await answer('output',{status:'running',output:'first',next_cursor:5});assert.equal(box.scrollTop,500);await observers();
 box.scrollTop=25;element('follow-output').checked=false;run('loadOutput()');strict(take('output'),'output',{target:fixtureJob.id,cursor:5});await answer('output',{status:'running',output:'second',next_cursor:11});assert.equal(box.scrollTop,25);assert.equal(element('output-bottom').hidden,false);
 box.scrollTop=490;run('loadOutput()');await answer('output',{status:'running',output:'third',next_cursor:16});assert.equal(element('output-bottom').hidden,false);
 await click('output-bottom');assert.equal(element('output-bottom').hidden,true);assert.equal(box.scrollTop,box.scrollHeight);
 run('loadOutput()');await answer('output',{status:'unknown',output:'last-page',next_cursor:25});
 assert.equal(run('outputDrained'),false,'unknown must drain remaining recorded output');
 run('loadOutput()');strict(take('output'),'output',{target:fixtureJob.id,cursor:25});await answer('output',{status:'unknown',output:'',next_cursor:25});
 assert.equal(run('outputDrained'),true,'unknown stops only after recorded output is exhausted');
 let before;
 await resetFlow();await click('updates');await answer('updateStatus',{current:'fixture'});
 element('update-path').value='C:\\fixture.zip';element('update-hash').value='a'.repeat(64);before=operations('updateApply').length;
 await click('import',{updateOp:'updateImport'});await answer('updateImport',{version:'next',bundle_path:'C:\\fixture\\bundle'});assert.match(element('update-result').innerHTML,/data-update-op="updatePrepare"/);assert.equal(operations('updateApply').length,before);
 await click('prepare-update',{updateOp:'updatePrepare'});await answer('updatePrepare',{ticket:'update-ticket',version:'next',bundle_path:'C:\\fixture\\bundle',mode:'portable',observer:false,runtime:'fixture',observer_config:'',expires:'2099'});assert.equal(run('updateTicket'),'update-ticket');assert.equal(operations('updateApply').length,before);
 await click('apply-update',{updateOp:'updateApply'});await click('apply-update',{updateOp:'updateApply'});assert.equal(operations('updateApply').length,before+1);await answer('updateApply','fixture handoff failure',false);assert.equal(run('updateTicket'),null);await click('apply-update',{updateOp:'updateApply'});assert.equal(operations('updateApply').length,before+1);
 await resetFlow();await click('integrations');await answer('integrationStatus',integrationData('absent'));
 await click('preview',{integrationAction:'install',component:'mcp'});await answer('integrationPreview',{ticket:'closed-ticket',action:'install',component:'mcp',paths:['fixture'],commands:[]});
 assert.match(element('modal-actions').innerHTML,/id="integration-confirm"/);assert.ok(!element('integration-preview').innerHTML.includes('id="integration-confirm"'));
 await click('close-preview',{close:''});await answer('cancelReview',{});
 await click('integrations');await answer('integrationStatus',integrationData('absent'));assert.equal(run('integrationTicket'),null);
 before=operations('integrationApply').length;await click('integration-confirm');assert.equal(operations('integrationApply').length,before);
 await click('preview',{integrationAction:'install',component:'mcp'});const obsolete=take('integrationPreview');run("openModal('other','','','connections')");reply(obsolete,{ticket:'obsolete',paths:[],commands:[]});await tick();assert.equal(run('integrationTicket'),null);await click('integration-confirm');assert.equal(operations('integrationApply').length,before);
 // Management, update and output tests use isolated fixtures.
 await observers();assert.equal(run('waiting.size'),0,'every operation has an explicit fixture reply');
 console.log('PASS: management and event-flow regression fixtures (not native OS validation)');
})().catch(e=>{console.error(e);process.exitCode=1;});
