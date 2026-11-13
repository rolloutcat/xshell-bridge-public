// Isolated fakes: a legacy binding must never register or create a helper.
const assert = require('assert');
const fs = require('fs');
const vm = require('vm');
const context = { WScript: { Sleep() {} }, ActiveXObject() { throw Error('unexpected helper creation'); } };
vm.createContext(context);
vm.runInContext(fs.readFileSync('agent/xshell_agent.js', 'utf8'), context);
vm.runInContext(fs.readFileSync('agent/remote_bootstrap.js', 'utf8'), context);
let calls = 0;
const fake = { Session: { Connected:true, Path:'p', RemoteAddress:'host', RemotePort:'22', SessionName:'s', TabText:'t', UserName:'u' }, Screen: {} };
assert.throws(() => new context.Agent(fake, { binding_mode:'single_tab', exclusive_single_tab:true, name:'n', environment:'e' }, { post() { calls++; } }));
assert.equal(calls, 0);
assert.throws(() => context.remoteBootstrap(fake, { config: { endpoint:'http://127.0.0.1:18767', token:'a'.repeat(32), source_id:'b'.repeat(32), binding_mode:'window_auto', environment:'e' } }), /invalid remote configuration/);
assert.throws(() => context.remoteBootstrap(fake, { config: { endpoint:'http://127.0.0.1:18767', token:'a'.repeat(32), source_id:'b'.repeat(32), binding_mode:'manual_terminal', environment:'e' } }), /invalid embedded helper/);

for (const value of [null, 936, 'utf-32', '']) {
 const config={endpoint:'http://127.0.0.1:18767',token:'a'.repeat(32),source_id:'b'.repeat(32),binding_mode:'manual_terminal',environment:'e',log_encoding:value};
 assert.throws(()=>context.remoteBootstrap(fake,{config}),/invalid log encoding/);
}
