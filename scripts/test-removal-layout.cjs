'use strict';
// Isolated browser fixture: no Bridge, SSH, Xshell, or real session mutations.
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const {chromium}=require(process.env.PLAYWRIGHT_MODULE||'playwright');
(async()=>{
 const report=path.resolve(process.argv[2]||'dist/removal-tests/layout');fs.mkdirSync(report,{recursive:true});
 const native=process.env.XSH_REMOVAL_SNAPSHOT_FIXTURE?JSON.parse(fs.readFileSync(process.env.XSH_REMOVAL_SNAPSHOT_FIXTURE,'utf8')).data:null;
 let html=fs.readFileSync('desktop/app.html','utf8').replace(/<meta[^>]*http-equiv="Content-Security-Policy"[^>]*>/i,'');
 for(const [marker,file] of [['/*__ATTACHMENT_STATE__*/','desktop/attachment-state.js'],['/*__UPDATES_UI__*/','desktop/updates-ui.js']])html=html.replace(marker,()=>fs.readFileSync(file,'utf8'));
 const browser=await chromium.launch({headless:true,...(process.env.BROWSER_PATH?{executablePath:process.env.BROWSER_PATH}:{})});
 try{for(const [width,height] of [[1280,850],[900,600],[640,480]]){
  const page=await browser.newPage({viewport:{width,height}}),errors=[];page.on('pageerror',e=>errors.push(e.message));await page.route('**/*',r=>r.abort());
  await page.evaluate(native=>{
   const listeners=[],id=n=>n.toString(16).padStart(48,'0');
   const sessions=Array.from({length:100},(_,n)=>({id:id(n+1),generation:id(n+1001),name:'测试终端 <'+n+'>',environment:'通用环境 / '+('很长的分组名称'.repeat(20)),state:'detached',fingerprint:JSON.stringify({SessionName:'fixture',Path:'C:\\'+('long-path-'.repeat(35))}),jobs:[]}));
   const initial=native||{sessions:sessions.slice(0,6).map((s,i)=>({...s,state:['blocked','detached','expired','active','pending','blocked'][i]})),jobs:[],grants:[],rules:[],history_sessions:[],read_only:false,supports_labels:true,supports_session_removal:true,bridge_version:'fixture-bridge',desktop_version:'fixture-desktop'};
   const state=JSON.parse(JSON.stringify(initial));let reviewed=[];
   window.removalFixture={state,initial:JSON.parse(JSON.stringify(initial)),hundred:sessions,offline:false};
   window.confirmCount=0;window.chrome={webview:{addEventListener(t,f){listeners.push(f)},postMessage(raw){const q=JSON.parse(raw);let data={};
    if(q.op==='snapshot')data=state;
    if(q.op==='sessionRemovalPrepare'){reviewed=state.sessions.filter(s=>q.action==='remove'?s.id===q.target:['blocked','detached','expired'].includes(s.state));data={ticket:'fixture-ticket',count:reviewed.length,items:reviewed.map(s=>({...s,jobs:state.jobs.filter(j=>j.session_id===s.id).map(({id,status})=>({id,status}))})),stop_active:q.action==='remove'&&reviewed.some(s=>['active','pending'].includes(s.state)),expires:'2099',notice:'保留任务历史，不关闭 SSH。'};}
    if(q.op==='sessionRemovalConfirm'){window.confirmCount++;data={recorded:true,ids:reviewed.map(s=>s.id)};state.history_sessions=reviewed.map(s=>({...s,archived:true}));state.sessions=state.sessions.filter(s=>!reviewed.some(r=>r.id===s.id));}
    const response=q.op==='snapshot'&&window.removalFixture.offline?{id:q.id,ok:false,error:'isolated offline fixture'}:{id:q.id,ok:true,data:JSON.parse(JSON.stringify(data))};
    queueMicrotask(()=>listeners.forEach(f=>f({data:response})));
   }}};
  },native);
  await page.setContent(html);await page.locator('nav [data-page="sessions"]').click();await page.locator('#session-cleanup').waitFor();
  assert.equal(await page.locator('[data-remove-session]').count(),6);assert.equal(await page.locator('[data-remove-session]:disabled').count(),0);
  await page.locator('[data-edit-label]').first().click();assert.equal(await page.locator('#session-cleanup').isDisabled(),true);
  await page.locator('#save-label').click();await page.waitForFunction(()=>!document.querySelector('#modal').open&&!document.querySelector('#session-cleanup').disabled);
  assert.equal(await page.locator('[data-remove-session]:disabled').count(),0);
  await page.screenshot({path:path.join(report,`${width}-six-recovered.png`)});
  // The backend fixture changes, not the application's internal eligibility.
  for(const mode of ['unsupported','readonly','offline','busy']){
   await page.evaluate(mode=>{const f=window.removalFixture;Object.assign(f.state,JSON.parse(JSON.stringify(f.initial)));f.offline=mode==='offline';if(mode==='unsupported')f.state.supports_session_removal=false;if(mode==='readonly')f.state.read_only=true;if(mode==='busy')f.state.jobs=[{id:'b'.repeat(48),session_id:f.state.sessions.find(s=>s.state==='blocked').id,status:'running'}];},mode);
   await page.locator('#refresh').click();await page.waitForFunction(()=>document.querySelector('#session-cleanup').disabled);
   const text=await page.locator('#content').innerText();assert.match(text,mode==='unsupported'?/Bridge.*工作台.*升级/:mode==='readonly'?/只读/:mode==='offline'?/连接不可用/:/执行/);
   await page.screenshot({path:path.join(report,`${width}-${mode}.png`)});
   await page.evaluate(()=>{const f=window.removalFixture;Object.assign(f.state,JSON.parse(JSON.stringify(f.initial)));f.offline=false;});await page.locator('#refresh').click();await page.waitForFunction(()=>!document.querySelector('#session-cleanup').disabled);
  }
  await page.locator('#session-cleanup').click();await page.locator('#session-removal-confirm').waitFor();assert.equal(await page.locator('#modal-body article').count(),4);await page.locator('#modal-actions [data-close]').click();
  await page.evaluate(()=>{const f=window.removalFixture;f.state.sessions=f.hundred;f.state.jobs=[];});await page.locator('#refresh').click();await page.waitForFunction(()=>document.querySelectorAll('[data-remove-session]').length===100);
  await page.locator('#session-cleanup').click();await page.locator('#session-removal-confirm').waitFor();
  assert.equal(await page.locator('#modal-body article').count(),100);assert.equal(await page.locator('#modal-body input[type="checkbox"]').count(),0);
  const bounds=await page.evaluate(()=>{const body=document.querySelector('#modal-body'),footer=document.querySelector('#modal-actions').getBoundingClientRect();return {horizontal:document.documentElement.scrollWidth<=innerWidth,footer:footer.top>=0&&footer.bottom<=innerHeight,scroll:body.scrollHeight>body.clientHeight&&getComputedStyle(body).overflowY==='auto'};});
  assert.ok(bounds.horizontal&&bounds.footer&&bounds.scroll,JSON.stringify(bounds));
  await page.screenshot({path:path.join(report,`${width}-cleanup.png`)});
  await page.locator('#modal-body').evaluate(el=>el.scrollTop=el.scrollHeight);
  await page.locator('#session-removal-confirm').dblclick();await page.locator('#modal-body .success').waitFor();assert.equal(await page.evaluate(()=>confirmCount),1);assert.deepEqual(errors,[]);await page.close();
 }}finally{await browser.close();}
 console.log('PASS: six-terminal recovery and visible disabled reasons; 100-terminal cleanup at 1280/900/640 widths, visible actions and single confirmation');
})().catch(e=>{console.error(e);process.exitCode=1;});
