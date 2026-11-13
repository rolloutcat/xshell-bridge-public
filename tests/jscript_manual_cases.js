if (typeof require === 'function' && typeof Agent === 'undefined') {
    var fs = require('fs'), vm = require('vm'), path = require('path');
    var sandbox = {console:console,WScript:{Sleep:function(ms) { var end = Date.now() + ms; while (Date.now() < end) {} }}};
    vm.createContext(sandbox);
    vm.runInContext(fs.readFileSync(path.join(__dirname, '../agent/xshell_agent.js'), 'utf8'), sandbox);
    vm.runInContext(fs.readFileSync(__filename, 'utf8'), sandbox);
} else {
(function () {
    function assert(ok, message) { if (!ok) { throw new Error(message); } }
    function fixture() {
        var calls = [], sends = 0, drift = false, swap = false;
        var session = {Connected:true,Path:'C:\\sessions\\observed.xsh',RemoteAddress:'192.0.2.25',RemotePort:'2222',SessionName:'observed',TabText:'A',UserName:'operator'};
        var screen = {
            Synchronous:false,
            IsAltScreen:function () { return false; },
            WaitForStrings:function (strings, timeout) {
                assert(strings instanceof Array && timeout === 100, 'unexpected preflight');
                WScript.Sleep(100);
                if (drift) { session.UserName = 'changed'; }
                if (swap) { xsh.Session = {Connected:false}; xsh.Screen = {}; }
                return 0;
            },
            Send:function () { sends++; throw new Error('unexpected Send'); }
        };
        var xsh = {Session:session,Screen:screen};
        var api = {post:function (path, data) {
            calls.push({path:path,data:data});
            assert(path === '/v1/agent/register', 'unexpected API call');
            return {id:'pending-id',generation:1};
        }};
        return {xsh:xsh,session:session,screen:screen,api:api,calls:calls,
            sends:function () { return sends; },
            drift:function () { drift = true; },swap:function () { swap = true; }};
    }
    function config() { return {binding_mode:'manual_terminal',name:'IGNORED',environment:'  staging  '}; }
    function rejected(change, message) {
        var f = fixture(), c = config(), error = null;
        change(f, c);
        try { new Agent(f.xsh, c, f.api); } catch (e) { error = e; }
        assert(error !== null, 'accepted: ' + message);
        assert(f.calls.length === 0 && f.sends() === 0, 'side effect: ' + message);
        assert(f.screen.Synchronous === false, 'synchronous not restored: ' + message);
    }
    var f = fixture(), c = config(), original = fingerprint(f.xsh);
    var agent = new Agent(f.xsh, c, f.api);
    assert(f.calls.length === 1, 'one registration required');
    assert(f.calls[0].path === '/v1/agent/register', 'registration only');
    assert(f.calls[0].data.name === 'A', 'TabText must override config name');
    assert(f.calls[0].data.auto_name === true, 'manual attachment requests stable Bridge numbering');
    assert(f.calls[0].data.environment === 'staging', 'trim environment');
    assert(f.calls[0].data.fingerprint === original && agent.bound === original, 'original fingerprint preserved');
    assert(parseJSON(agent.bound).RemotePort === '2222', 'port preserved');
    assert(f.sends() === 0 && f.screen.Synchronous === false, 'no Send; restore sync');
    rejected(function (f,c) { delete c.environment; }, 'missing environment');
    rejected(function (f,c) { c.environment = ' \t\r\n '; }, 'blank environment');
    rejected(function (f,c) { c.environment = 42; }, 'nonstring environment');
    rejected(function (f,c) { c.environment = '\u8bf7\u586b\u5199'; }, 'placeholder environment');
    rejected(function (f,c) { c.environment = new Array(202).join('x'); }, 'long environment');
    var fields = ['Path','RemoteAddress','RemotePort','SessionName','TabText','UserName'], i;
    for (i = 0; i < fields.length; i++) {
        (function (field) {
            rejected(function (f,c) { f.session[field] = ''; }, 'empty ' + field);
            rejected(function (f,c) { f.session[field] = ' \t\r\n'; }, 'blank ' + field);
        })(fields[i]);
    }
    rejected(function (f,c) { f.session.Connected = false; }, 'disconnected');
    rejected(function (f,c) { f.drift(); }, 'preflight identity drift');
    rejected(function (f,c) { delete c.binding_mode; }, 'absent mode still requires single-tab confirmation');
    rejected(function (f,c) { c.binding_mode = 'single_tab'; }, 'explicit single-tab confirmation');
    rejected(function (f,c) { c.binding_mode = 'manual_termnal'; c.exclusive_single_tab = true; }, 'typo has no fallback');
    rejected(function (f,c) { c.binding_mode = ''; c.exclusive_single_tab = true; }, 'empty mode has no fallback');
    rejected(function (f,c) { c.binding_mode = 'per_terminal_test'; c.terminal_profiles = []; }, 'profile mode still requires match');
    f = fixture(); c = config(); f.swap();
    agent = new Agent(f.xsh, c, f.api);
    assert(agent.xsh.Session === f.session && agent.xsh.Screen === f.screen, 'cached Session/Screen');
    assert(f.calls.length === 1 && f.sends() === 0, 'cached preflight registers only');
    f = fixture(); c = config(); f.session.TabText = ' A '; c.name = null;
    original = fingerprint(f.xsh); agent = new Agent(f.xsh, c, f.api);
    assert(f.calls[0].data.name === ' A ' && agent.bound === original, 'do not normalize bound metadata');
    f.session.UserName = 'other';
    var failed = false;
    try { agent.check(); } catch (e) { failed = true; }
    assert(failed && f.sends() === 0, 'post-registration identity drift still fails');
})();
}
