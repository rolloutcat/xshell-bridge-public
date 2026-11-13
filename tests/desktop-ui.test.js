'use strict';
const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const html = fs.readFileSync('desktop/app.html', 'utf8');
const helper = fs.readFileSync('desktop/attachment-state.js', 'utf8');
const scripts = [...html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/gi)];
assert.equal(scripts.length, 1);
assert.ok(scripts[0][1].includes('/*__ATTACHMENT_STATE__*/'));
const source = scripts[0][1].replace('/*__ATTACHMENT_STATE__*/', () => helper);
const compiled = new vm.Script(source, { filename: 'desktop/app.html:inline' });
const elements = new Map();
const handlers = {};
function element(id) {
  if (!elements.has(id)) elements.set(id, {
    id, innerHTML: '', textContent: '', className: '', disabled: false,
    checked: false, open: false, dataset: {}, listeners: {},
    classList: { toggle() {} },
    addEventListener(type, fn) { this.listeners[type] = fn; },
    showModal() { this.open = true; }, close() { this.open = false; }
  });
  return elements.get(id);
}
let receive;
const receivers=[];
let timer = 0;
const sent = [];
const context = vm.createContext({
  console,
  document: {
    getElementById: element,
    querySelectorAll() { return []; },
    addEventListener(type, fn) { handlers[type] = fn; }
  },
  window: { chrome: { webview: {
    addEventListener(type, fn) { assert.equal(type, 'message'); receivers.push(fn); receive = event => receivers.forEach(f=>f(event)); },
    postMessage(message) { sent.push(JSON.parse(message)); }
  } } },
  setTimeout() { return ++timer; }, clearTimeout() {},
  setInterval() { return ++timer; }, clearInterval() {}
});
compiled.runInContext(context);
const run = code => vm.runInContext(code, context);
const tick = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };
function reply(request, data, ok = true) {
  receive({ data: ok ? { id: request.id, ok: true, data } : { id: request.id, ok: false, error: data } });
}
function snapshot(overrides = {}) {
  return { sessions: [], jobs: [], grants: [], supports_grants: false,
    read_only: false, mode: 'Local', agent_script_installed: true,
    agent_script_path: 'C:\\private\\A&B<test>\\xshell_agent.js', ...overrides };
}
async function refreshWith(data) {
  const start = sent.length;
  run('refresh()');
  const request = sent.slice(start).find(r => r.op === 'snapshot');
  assert.ok(request, 'refresh requests authoritative server state');
  reply(request, data);
  await tick();
}
(async () => {
  assert.equal(typeof handlers.click, 'function');
  assert.equal(typeof element('modal').listeners.cancel, 'function');
  const first = sent.shift();
  assert.equal(first.op, 'snapshot');
  reply(first, snapshot());
  await tick();
  assert.match(element('attachment').innerHTML, /未检测到有效脚本接入/);
  run('attachmentGuide()');
  assert.equal(element('modal').open, true);
  const guide = element('modal-body').innerHTML;
  assert.match(guide, /每个/);
  assert.match(guide, /核对目标和新的接入代次并激活/);
  assert.doesNotMatch(guide, /TabManager|install-script|页签检测/);

  await refreshWith(snapshot({ sessions: [{ id: 'pending-session', state: 'pending', last_seen: '2000-01-01T00:00:00Z' }] }));
  assert.match(element('attachment').innerHTML, /待激活接入/);
  assert.match(element('attachment').innerHTML, /无需重复运行/);
  await refreshWith(snapshot({ sessions: [{ id: 'active-session', state: 'active', last_seen: '2000-01-01T00:00:00Z' }] }));
  assert.match(element('attachment').innerHTML, /有效接入/);
  assert.ok(!element('attachment').innerHTML.includes('未检测到有效'));
  await refreshWith(snapshot({ sessions: [{ id: 'old-session', state: 'expired' }] }));
  assert.match(element('attachment').innerHTML, /历史接入已失效/);

  const offlineStart = sent.length;
  run('refresh()');
  reply(sent[offlineStart], 'simulated offline', false);
  await tick();
  assert.match(element('attachment').innerHTML, /暂时无法检测/);
  assert.ok(!element('attachment').innerHTML.includes('未检测到有效脚本接入'));
  run('attachmentGuide()');
  assert.match(element('modal-body').innerHTML, /导出脚本/);

  run("openModal('test', '<p>ordinary guide</p>', '<button data-close>关闭</button>', 'details')");
  let prevented = false;
  element('modal').listeners.cancel({ preventDefault() { prevented = true; } });
  assert.equal(prevented, true);
  assert.equal(element('modal').open, false);
  assert.equal(sent.at(-1).op, 'cancelReview');
  reply(sent.at(-1), { cancelled: true });
  await tick();
  // Review still requires an explicit button, without an extra acknowledgement checkbox.
  const labelSession={id:'a'.repeat(48),generation:'b'.repeat(48),name:'B<name>',environment:'company&test',fingerprint:'{}',state:'active'};
  await refreshWith(snapshot({supports_labels:true,sessions:[labelSession]}));
  run("page='sessions';render()");
  assert.match(element('content').innerHTML, /编辑名称/);
  run(`editLabel('${labelSession.id}')`);
  assert.match(element('modal-body').innerHTML, /B&lt;name&gt;/);
  assert.match(element('modal-body').innerHTML, /分组／环境/);
  element('label-name').value='业务服务器·终端1';element('label-environment').value='测试环境';
  const labelStart=sent.length;run('saveLabel()');run('saveLabel()');
  assert.equal(sent.length,labelStart+1);
  assert.deepEqual(sent.at(-1), {id:sent.at(-1).id,deadline_ms:sent.at(-1).deadline_ms,op:'labelSession',target:labelSession.id,generation:labelSession.generation,name:'业务服务器·终端1',environment:'测试环境'});
  reply(sent.at(-1),{recorded:true});await tick();reply(sent.at(-1),snapshot());await tick();
  await refreshWith(snapshot({supports_labels:true,read_only:true,sessions:[labelSession]}));
  const observerLabelStart=sent.length;run(`editLabel('${labelSession.id}')`);run('saveLabel()');assert.equal(sent.length,observerLabelStart);
  await refreshWith(snapshot({supports_labels:false,sessions:[labelSession]}));
  assert.ok(!element('content').innerHTML.includes('data-edit-label'));
  await refreshWith(snapshot({supports_grants:true}));
  const legacyBefore=sent.length;run(`prepare('allow','${'a'.repeat(48)}')`);run(`prepare('deny','${'a'.repeat(48)}')`);assert.equal(sent.length,legacyBefore,'old approval review must not send');
  for (const action of ['activate']) {
    await refreshWith(snapshot({supports_grants:true}));
    const before = sent.length;
    run(`prepare('${action}','${'a'.repeat(48)}')`);
    const request = sent[before];
    assert.equal(request.op, 'prepare');
    reply(request, {ticket:'ticket-'+action, action, expires:new Date(Date.now()+60000).toISOString(),
      session:{id:'a'.repeat(48),generation:'g',name:'<target>',environment:'group',fingerprint:'{}'},
      job:action==='activate'?null:{id:'j',cwd:'/',timeout_ms:15000,command:'printf test'}});
    await tick();
    assert.ok(!element('modal-body').innerHTML.includes('type="checkbox"'));
    assert.ok(!/id="confirm"[^>]*disabled/.test(element('modal-actions').innerHTML));
    assert.ok(!element('modal-actions').innerHTML.includes('confirm-remember'));
    assert.ok(!sent.slice(before).some(r=>r.op==='confirm'), 'opening review must not approve');
    run('confirm()'); run('confirm()');
    const confirmations = sent.slice(before).filter(r=>r.op==='confirm');
    assert.equal(confirmations.length, 1, 'one explicit click, no repeated decision');
    assert.equal(confirmations[0].remember, false);
    reply(confirmations[0], {recorded:true}); await tick();
    assert.equal(element('modal').open, false, 'successful activation closes its review');
    assert.match(element('feedback').textContent, /激活/);
    assert.equal(run('review'), null, 'activation ticket is consumed');
    reply(sent.at(-1), snapshot()); await tick();
  }
  const prepared={ticket:'once',action:'activate',expires:new Date(Date.now()+60000).toISOString(),session:labelSession,job:null};
  run(`prepare('activate','${labelSession.id}')`);reply(sent.at(-1),prepared);await tick();run('confirm()');const late=sent.at(-1);
  run("openModal('new window','sentinel','','details')");reply(late,{recorded:true});await tick();assert.equal(element('modal').open,true);assert.equal(element('modal-body').innerHTML,'sentinel');reply(sent.at(-1),snapshot());await tick();
  run(`prepare('activate','${labelSession.id}')`);reply(sent.at(-1),prepared);await tick();run('confirm()');const uncertain=sent.at(-1);reply(uncertain,'lost',false);await tick();assert.equal(element('modal').open,false);assert.match(element('error').textContent,/未确认.*只读刷新/);assert.equal(sent.at(-1).op,'snapshot');const count=sent.filter(q=>q.op==='confirm').length;run('confirm()');assert.equal(sent.filter(q=>q.op==='confirm').length,count);reply(sent.at(-1),snapshot());await tick();
  run(`prepare('activate','${labelSession.id}')`);reply(sent.at(-1),prepared);await tick();run('confirm()');const oldContext=sent.at(-1);run("connectionGeneration++;mutating=false;openModal('new connection','new context','','details')");reply(oldContext,{recorded:true});await tick();assert.equal(element('modal-body').innerHTML,'new context');assert.equal(element('modal').open,true);
  console.log('PASS: syntax, guide escaping, manual mapping, fixed installation IPC, observer, server attachment states, offline status, modal cancellation');
})().catch(error => { console.error(error); process.exitCode = 1; });
