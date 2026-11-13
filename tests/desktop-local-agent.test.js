'use strict';
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const html=fs.readFileSync('desktop/app.html','utf8');
assert.match(html,/id="local-xshell"/,'local entry must be discoverable');
let source=html.match(/<script>([\s\S]*?)<\/script>/)[1];
for(const [marker,file] of [['/*__ATTACHMENT_STATE__*/','desktop/attachment-state.js'],['/*__UPDATES_UI__*/','desktop/updates-ui.js']])source=source.replace(marker,()=>fs.readFileSync(file,'utf8'));
const elements=new Map(),handlers={},receivers=[],sent=[];
function el(id){if(!elements.has(id))elements.set(id,{id,value:'',dataset:{},innerHTML:'',textContent:'',hidden:false,disabled:false,checked:false,classList:{toggle(){}},addEventListener(){},showModal(){this.open=true;},close(){this.open=false;},closest(){return this;},hasAttribute(k){return k==='data-close'&&this.dataset.close!==undefined;},before(){},scrollIntoView(){}});return elements.get(id);}
const context=vm.createContext({console,document:{getElementById:el,createElement(){return el('created');},querySelectorAll(){return [];},addEventListener(t,f){(handlers[t]??=[]).push(f);}},window:{chrome:{webview:{addEventListener(_,f){receivers.push(f);},postMessage(s){sent.push(JSON.parse(s));}}}},setTimeout(){return 1;},clearTimeout(){},setInterval(){}});
const run=s=>vm.runInContext(s,context),tick=async()=>{for(let i=0;i<30;i++)await Promise.resolve();};
const ops=op=>sent.filter(q=>q.op===op),last=op=>ops(op).at(-1);
function reply(q,data,ok=true){assert.ok(q);receivers.forEach(f=>f({data:ok?{id:q.id,ok,data}:{id:q.id,ok,error:data}}));}
async function click(id,dataset={}){const b=el(id);b.dataset=dataset;for(const f of handlers.click||[])f({target:b,preventDefault(){}});await tick();}
const state=(status='absent')=>({status,path:'C:\\fixture\\local-agent\\xshell_agent.js',packaged_version:'0.8.5',installed_version:'0.8.4',environment:'local',log_encoding:'auto',endpoint:'http://127.0.0.1:17890',source_id:'a'.repeat(32)});
(async()=>{
 run(source);reply(last('snapshot'),{sessions:[],jobs:[],grants:[],read_only:false});await tick();
 await click('local-xshell');reply(last('localAgentStatus'),state());await tick();
 assert.match(el('local-details').innerHTML,/local-agent/);el('local-environment').value='hospital';el('local-encoding').value='gbk';
 await click('local-prepare');assert.equal(last('localAgentPrepare').environment,'hospital');assert.equal(ops('localAgentConfirm').length,0);
 reply(last('localAgentPrepare'),{ticket:'once',expires:'2099',preview:state()});await tick();assert.equal(run('modalKind'),'local-agent-review');
 await click('close',{close:''});assert.equal(ops('localAgentConfirm').length,0);
 await click('local-xshell');reply(last('localAgentStatus'),state('update_available'));await tick();await click('local-prepare');reply(last('localAgentPrepare'),{ticket:'once',expires:'2099',preview:state('update_available')});await tick();
 await click('local-confirm');await click('local-confirm');assert.equal(ops('localAgentConfirm').length,1);reply(last('localAgentConfirm'),{installed:true});await tick();reply(last('localAgentStatus'),state('current'));await tick();
 assert.equal(run('modalKind'),'local-agent');assert.match(el('local-feedback').textContent,/完成/);assert.equal(el('modal').open,true);
 await click('local-xshell');reply(last('localAgentStatus'),state('modified'));await tick();assert.equal(el('local-prepare').disabled,true);
 run('state.read_only=true');await click('local-xshell');assert.equal(el('local-prepare').disabled,true);run('state.read_only=false');
 await click('local-xshell');reply(last('localAgentStatus'),'offline',false);await tick();assert.equal(el('local-prepare').disabled,true);
 await click('local-xshell');const late=last('localAgentStatus');run("openModal('other','sentinel','','details')");reply(late,state());await tick();assert.equal(el('modal-body').innerHTML,'sentinel');
 await click('local-xshell');reply(last('localAgentStatus'),state());await tick();await click('local-prepare');const latePrepare=last('localAgentPrepare');run("openModal('other','new window','','details')");reply(latePrepare,{ticket:'stale',expires:'2099',preview:state()});await tick();assert.equal(el('modal-body').innerHTML,'new window');
 await click('local-xshell');reply(last('localAgentStatus'),state());await tick();await click('local-prepare');reply(last('localAgentPrepare'),'failure',false);await tick();assert.match(el('modal-error').textContent,/failure/);assert.equal(ops('localAgentConfirm').length,1);
 await click('local-prepare');reply(last('localAgentPrepare'),{ticket:'failure',expires:'2099',preview:state()});await tick();el('local-confirm').disabled=false;await click('local-confirm');reply(last('localAgentConfirm'),'write failed',false);await tick();assert.match(el('modal-error').textContent,/不会自动重试.*write failed/);await click('local-confirm');assert.equal(ops('localAgentConfirm').length,2);
 await click('local-xshell');reply(last('localAgentStatus'),state());await tick();await click('local-prepare');reply(last('localAgentPrepare'),{ticket:'late-confirm',expires:'2099',preview:state()});await tick();el('local-confirm').disabled=false;await click('local-confirm');const lateConfirm=last('localAgentConfirm');run("openModal('other','still here','','details')");reply(lateConfirm,{installed:true});await tick();assert.equal(el('modal-body').innerHTML,'still here');
 assert.ok(!sent.some(q=>/reverseConnect|exec|job/i.test(q.op)));
 console.log('PASS: actual local agent UI events');
})().catch(e=>{console.error(e);process.exitCode=1;});
