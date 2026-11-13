// Xshell official JScript entry point: Tools -> Script -> Run -> this file.
// Official Xshell script API + verified, stdio-only native log reader. No SSH login.
// Keep this file ASCII for legacy JScript engines. Configuration is UTF-8.

function fail(message) { throw new Error(message); }
function own(object, key) { return Object.prototype.hasOwnProperty.call(object, key); }

// Legacy JScript may not provide JSON. Parse data without eval/Function/HTML engines.
function parseJSON(text) {
    var i = 0, length = text.length;
    function white() { while (i < length && /[ \t\r\n]/.test(text.charAt(i))) { i++; } }
    function string() {
        var out = '', c, escape, digits;
        i++;
        while (i < length) {
            c = text.charAt(i++);
            if (c === '"') { return out; }
            if (c === '\\') {
                escape = text.charAt(i++);
                if (escape === 'u') {
                    digits = text.substr(i, 4);
                    if (!/^[0-9a-fA-F]{4}$/.test(digits)) { fail('invalid JSON escape'); }
                    out += String.fromCharCode(parseInt(digits, 16)); i += 4;
                } else {
                    var escapes = {'"':'"', '\\':'\\', '/':'/', b:'\b', f:'\f', n:'\n', r:'\r', t:'\t'};
                    if (!own(escapes, escape)) { fail('invalid JSON escape'); }
                    out += escapes[escape];
                }
            } else {
                if (c.charCodeAt(0) < 32) { fail('invalid JSON string'); }
                out += c;
            }
        }
        fail('unterminated JSON string');
    }
    function value(depth) {
        if (depth > 32) { fail('JSON nesting limit'); }
        white();
        var c = text.charAt(i), result, key, match;
        if (c === '"') { return string(); }
        if (c === '{' || c === '[') {
            var object = c === '{', close = object ? '}' : ']';
            result = object ? {} : []; i++; white();
            if (text.charAt(i) === close) { i++; return result; }
            while (true) {
                white();
                if (object) {
                    if (text.charAt(i) !== '"') { fail('invalid JSON key'); }
                    key = string(); white();
                    if (key === '__proto__' || key === 'constructor' || key === 'prototype' || own(result, key)) { fail('unsafe or duplicate JSON key'); }
                    if (text.charAt(i++) !== ':') { fail('invalid JSON separator'); }
                    result[key] = value(depth + 1);
                } else { result.push(value(depth + 1)); }
                white(); c = text.charAt(i++);
                if (c === close) { return result; }
                if (c !== ',') { fail('invalid JSON collection'); }
            }
        }
        match = /^(true|false|null|-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)/.exec(text.substr(i));
        if (!match) { fail('invalid JSON value'); }
        i += match[0].length;
        if (match[0] === 'true') { return true; }
        if (match[0] === 'false') { return false; }
        if (match[0] === 'null') { return null; }
        result = Number(match[0]);
        if (!isFinite(result)) { fail('invalid JSON number'); }
        return result;
    }
    var result = value(0); white();
    if (i !== length) { fail('trailing JSON data'); }
    return result;
}

function stringifyJSON(value) {
    function quoteString(text) {
        return '"' + text.replace(/["\\\x00-\x1f\x7f-\uffff]/g, function(c) {
            return '\\u' + ('0000' + c.charCodeAt(0).toString(16)).slice(-4);
        }) + '"';
    }
    if (value === null) { return 'null'; }
    if (typeof value === 'string') { return quoteString(value); }
    if (typeof value === 'boolean') { return String(value); }
    if (typeof value === 'number' && isFinite(value)) { return String(value); }
    var parts = [], key;
    if (value instanceof Array) {
        for (key = 0; key < value.length; key++) { parts.push(stringifyJSON(value[key])); }
        return '[' + parts.join(',') + ']';
    }
    if (typeof value === 'object') {
        for (key in value) { if (own(value, key)) { parts.push(quoteString(key) + ':' + stringifyJSON(value[key])); } }
        return '{' + parts.join(',') + '}';
    }
    fail('unsupported JSON value');
}

function readConfig(path, maxBytes) {
    if (typeof maxBytes === 'undefined') { maxBytes = 131072; }
    var stream = new ActiveXObject('ADODB.Stream');
    try {
        stream.Type = 2; stream.Charset = 'utf-8'; stream.Open(); stream.LoadFromFile(path);
        if (stream.Size > maxBytes) { fail('configuration too large'); }
        return parseJSON(String(stream.ReadText()).replace(/^\ufeff/, ''));
    } finally { if (stream.State) { stream.Close(); } }
}

function BridgeAPI(config) {
    this.endpoint = String(config.endpoint || '').replace(/\/$/, '');
    var address = /^http:\/\/(127\.0\.0\.1|\[::1\])(?::([0-9]{1,5}))?$/.exec(this.endpoint);
    if (!address || (address[2] && (Number(address[2]) < 1 || Number(address[2]) > 65535))) { fail('agent endpoint must be numeric loopback HTTP'); }
    this.token = config.token;
    if (typeof this.token !== 'string' || this.token.length < 32 || /[\r\n]/.test(this.token)) { fail('invalid agent token'); }
}
BridgeAPI.prototype.post = function(path, data) {
    if (!/^\/v1\/agent\/(register|poll|event|heartbeat|detach)$/.test(path)) { fail('unsupported agent endpoint'); }
    var http = new ActiveXObject('WinHttp.WinHttpRequest.5.1');
    try {
        http.Open('POST', this.endpoint + path, false);
        http.SetProxy(1); // HTTPREQUEST_PROXYSETTING_DIRECT
        http.SetAutoLogonPolicy(2); // Never send Windows logon credentials.
        http.Option(6) = false; // WinHttpRequestOption_EnableRedirects (COM property setter).
        http.SetTimeouts(5000, 5000, 5000, 5000);
        http.SetRequestHeader('Content-Type', 'application/json; charset=utf-8');
        http.SetRequestHeader('Authorization', 'Bearer ' + this.token);
        http.Send(stringifyJSON(data));
    } catch (e) { fail('Bridge transport failed; no request is automatically retried'); }
    if (http.Status !== 200) { fail('Bridge rejected request (HTTP ' + http.Status + '); reattach may be required'); }
    var text = String(http.ResponseText);
    if (text.length > 2 * 1024 * 1024) { fail('Bridge response too large'); }
    return parseJSON(text);
};

function shellQuote(value) { return "'" + value.replace(/'/g, "'\"'\"'") + "'"; }
function commandText(command, cwd, nonce) {
    var script = "printf '\\n%s%s\\n' '__XSH_BEGIN_' " + shellQuote(nonce) + '\n(\n';
    script += 'PAGER=cat SYSTEMD_PAGER=cat SYSTEMD_PAGERSECURE=1 SYSTEMD_COLORS=0 GIT_PAGER=cat\n';
    script += 'export PAGER SYSTEMD_PAGER SYSTEMD_PAGERSECURE SYSTEMD_COLORS GIT_PAGER\n';
    if (cwd) { script += 'cd -- ' + shellQuote(cwd) + ' || exit 125\n'; }
    script += command + '\n) < /dev/null\n__xsh_rc=$?\n';
    script += "printf '\\n%s%s:%s\\n' '__XSH_END_' " + shellQuote(nonce) + ' "$__xsh_rc"\n';
    script += "printf '%s%s\\n' '__XSH_DONE_' " + shellQuote(nonce) + '\n';
    return 'sh -c ' + shellQuote(script) + '\r';
}
function chunks(text) {
    var parts = [], end;
    while (text.length) {
        end = Math.min(4000, text.length);
        if (end < text.length && /[\ud800-\udbff]/.test(text.charAt(end - 1))) { end--; }
        parts.push(text.substr(0, end)); text = text.substr(end);
    }
    return parts;
}
function eventReason(text) {
    var bytes=0,i=0,c,n;
    while(i<text.length){c=text.charCodeAt(i);n=c<128?1:(c<2048?2:3);if(c>=0xd800 && c<=0xdbff && i+1<text.length){n=4;}if(bytes+n>500){break;}bytes+=n;i+=n===4?2:1;}
    return text.substr(0,i);
}
function fingerprint(xsh) {
    if (!xsh.Session.Connected) { fail('Xshell disconnected'); }
    var names = ['Path','RemoteAddress','RemotePort','SessionName','TabText','UserName'], values = {}, i, v;
    for (i = 0; i < names.length; i++) {
        v = xsh.Session[names[i]];
        if (typeof v === 'undefined' || v === null) { fail('required Xshell Session field is unavailable'); }
        v = String(v);
        if (!v.replace(/^\s+|\s+$/g, '').length) { fail('required Xshell Session field is blank'); }
        values[names[i]] = v;
    }
    return stringifyJSON(values);
}
function instanceID() {
    // A registration label only. Bridge generates cryptographic session IDs/generations.
    return 'js-' + new Date().getTime() + '-' + Math.random().toString(16).substr(2) + '-' + Math.random().toString(16).substr(2);
}
function checkWaitAPI(screen) {
    var previous = screen.Synchronous, start = new Date().getTime(), result;
    try {
        screen.Synchronous = true;
        // The official JScript interface takes a JavaScript array (VT_DISPATCH),
        // not a Scripting.Dictionary SAFEARRAY. Probe without sending any input.
        result = screen.WaitForStrings(['__XSH_PREFLIGHT_' + instanceID()], 100);
        if ((result !== -1 && result !== 0) || new Date().getTime() - start < 50) {
            fail('WaitForStrings preflight failed; no command sent. Check Xshell version/script compatibility');
        }
    } finally { screen.Synchronous = previous; }
}
function Agent(xsh, config, api) {
    // ES3 closure: source identity must not follow mutable config/public fields.
    var sourceID = config.source_id;
    if (typeof sourceID !== 'undefined' && (typeof sourceID !== 'string' || !/^[0-9a-f]{32}$/.test(sourceID))) { fail('invalid source_id'); }
    this.currentFingerprint = function() {
        var value = parseJSON(fingerprint(this.xsh));
        if (typeof sourceID !== 'undefined') { value.SourceID = sourceID; }
        return stringifyJSON(value);
    };
    var mode = config.binding_mode, environment = config.environment;
    if (mode !== 'manual_terminal') { fail('manual_terminal required; export a new script'); }
    this.xsh = {Session:xsh.Session,Screen:xsh.Screen};
    this.api = api || new BridgeAPI(config); this.bound = this.currentFingerprint();
    var name = parseJSON(this.bound).TabText;
    if (typeof environment !== 'string') { fail('manual_terminal requires environment'); }
    environment = environment.replace(/^\s+|\s+$/g, '');
    if (!environment.length || environment.length > 200 || /\u8bf7\u586b\u5199/.test(environment)) { fail('set actual environment in exported profile'); }
    if (this.xsh.Screen.IsAltScreen()) { fail('leave full-screen applications before attaching'); }
    this.captureFactory = config.logCapture;
    checkWaitAPI(this.xsh.Screen);
    if (this.currentFingerprint() !== this.bound) { fail('terminal fingerprint changed during preflight'); }
    var registration = {instance:instanceID(),name:name,environment:environment,fingerprint:this.bound};
    registration.auto_name = true;
    this.session = this.api.post('/v1/agent/register', registration);
}
Agent.prototype.check = function() {
    var current = this.currentFingerprint();
    if (current !== this.bound) {
        this.api.post('/v1/agent/poll', {id:this.session.id,generation:this.session.generation,fingerprint:current});
        fail('terminal fingerprint changed');
    }
    return current;
};
Agent.prototype.poll = function() {
    return this.api.post('/v1/agent/poll', {id:this.session.id,generation:this.session.generation,fingerprint:this.check()});
};
Agent.prototype.execute = function(job) {
    if (this.stopped) { fail('agent stopped after uncertain task; no replay or further dispatch'); }
    var self=this, sequence=0, screen=this.xsh.Screen, session=this.xsh.Session;
    var previousSync=screen.Synchronous, pending='', transportFailed=false, capture=null, owned=false, oldPath=null, ownedPath=null, completedAck=false, cleanupAttempted=false, sendAttempted=false, stopAttempted=false, partialStart=false, failureSnapshot=null;
    function pathValue(value) {
        var type=typeof value,text=type==='string'?value:(value===null?'null':(type==='number'||type==='boolean'||type==='undefined'?String(value):'<non-scalar>'));
        if(text.length>192){text=text.substr(0,96)+'...'+text.substr(text.length-96);}
        text=text.replace(/[\\"\x00-\x1f\x7f-\uffff]/g,function(c){if(c==='\\'){return '\\\\';}if(c==='"'){return '\\"';}if(c==='\n'){return '\\n';}if(c==='\r'){return '\\r';}if(c==='\t'){return '\\t';}return '\\u'+('0000'+c.charCodeAt(0).toString(16)).slice(-4);});
        if(text.length>110){text=text.substr(0,54)+'...'+text.substr(text.length-53);}
        return type+' "'+text+'"';
    }
    function pathRead() {try{return pathValue(session.LogFilePath);}catch(e){return '<getter threw>';}}
    function loggingRead() {try{return pathValue(session.Logging);}catch(e){return '<getter threw>';}}
    function failureState() {return 'phase='+(sendAttempted?'SendAttempted':'NoSend')+'; actual='+pathRead()+'; expected='+pathValue(capture?capture.path:'<unavailable>')+'; Logging='+loggingRead();}
    function pathFailure(phase,before,after,expected) {
        fail('LogFilePath '+phase+'; phase=NoSend; before='+before+'; after='+after+'; expected='+pathValue(expected)+'; no command sent. Cause unknown; the agent did not call StartLog. Check the next-log setting manually; the getter may show only the last opened log.');
    }
    function event(status, output, code, reason) {
        var data={session_id:self.session.id,generation:self.session.generation,fingerprint:self.check(),job_id:job.id,
            seq:++sequence,output:output || '',status:status,exit_code:typeof code==='number'?code:null,reason:reason || ''};
        try { return self.api.post('/v1/agent/event',data); } catch(e) { transportFailed=true; throw e; }
    }
    function flush() {
        var parts=chunks(pending); pending='';
        for(var i=0;i<parts.length;i++){event('running',parts[i]);}
    }
    function logOwned() {
        var value=session.LogFilePath; return owned && capture && typeof value==='string' && value.toLowerCase()===capture.path.toLowerCase();
    }
    function checkLog() {
        capture.check();
        self.check();
        if (screen.IsAltScreen()) { fail('terminal entered a full-screen application'); }
        if (!logOwned() || session.Logging!==true) { fail('log path or Logging state changed'); }
    }
    function stopOwned() {
        if(stopAttempted){fail('StopLog cleanup already attempted');} stopAttempted=true;
        if(partialStart && self.currentFingerprint()!==self.bound){fail('source changed during partial StartLog cleanup');}
        if (!logOwned()) { fail('log path changed; foreign logging left untouched'); }
        capture.owned();
        if(partialStart && self.currentFingerprint()!==self.bound){fail('source changed during partial StartLog cleanup');}
        if (!logOwned()) { fail('log path changed; foreign logging left untouched'); }
        if (session.Logging===true) { session.StopLog(); }
        if (session.Logging!==false || !logOwned()) { fail('StopLog did not finalize owned log'); }
    }
    try {
        this.check();
        if (screen.IsAltScreen()) { fail('terminal entered a full-screen application'); }
        if (typeof session.Logging!=='boolean') { fail('Xshell Logging state unreadable; phase=NoSend; no command sent'); }
        if (session.Logging) { fail('Xshell user logging already active; stop it manually before running a task. No command sent'); }
        if (typeof this.captureFactory!=='function') { fail('native log capture unavailable; export a new standalone script. No command sent'); }
        if (!/^[A-Za-z0-9_-]{1,96}$/.test(job.id)) { fail('invalid job nonce'); }
        checkWaitAPI(screen); screen.Synchronous=true;
        capture=this.captureFactory(job.id);
        var beforePath=pathRead();
        try{oldPath=session.LogFilePath;}catch(pathGetterError){pathFailure('before getter failed',beforePath,pathRead(),capture.path);}
        if(typeof oldPath!=='string'){pathFailure('before getter is not a string',beforePath,pathRead(),capture.path);}
        ownedPath=capture.path;
        // The getter is the last opened log, not the configured next path. Keep
        // the first observed baseline while later getters point to our deleted logs.
        if (typeof this.lastCapturePath!=='string' || oldPath.toLowerCase()!==this.lastCapturePath.toLowerCase()) { this.logBaseline=oldPath; }
        // Recheck after native startup. Never take over logging that appeared meanwhile.
        this.check(); if (session.Logging!==false) { fail('user logging started or state unreadable during capture setup; no command sent'); }
        try{session.LogFilePath=capture.path;}catch(pathSetterError){pathFailure('setter threw',beforePath,pathRead(),capture.path);}
        var afterPath;
        try{afterPath=session.LogFilePath;}catch(afterGetterError){pathFailure('after getter failed',beforePath,pathRead(),capture.path);}
        if(typeof afterPath!=='string'||(afterPath.toLowerCase()!==capture.path.toLowerCase() && afterPath.toLowerCase()!==oldPath.toLowerCase())){pathFailure('assignment/readback mismatch',beforePath,pathValue(afterPath),capture.path);}
        this.check(); capture.prepared();
        if(screen.IsAltScreen()){fail('terminal entered a full-screen application');}
        if(session.Logging!==false){fail('user logging started or state unreadable after path assignment; no command sent');}
        try{afterPath=session.LogFilePath;}catch(recheckError){pathFailure('recheck getter failed',beforePath,pathRead(),capture.path);}
        if(typeof afterPath!=='string'||(afterPath.toLowerCase()!==capture.path.toLowerCase() && afterPath.toLowerCase()!==oldPath.toLowerCase())){pathFailure('recheck mismatch',beforePath,pathValue(afterPath),capture.path);}
        try { session.StartLog(); }
        catch(startError) {
            partialStart=true;
            failureSnapshot=failureState();
            // A partial StartLog may have opened our exact file. Bind only when
            // the cached source and strict writer state still match.
            try {
                if(self.currentFingerprint()===self.bound && !screen.IsAltScreen() && session.Logging===true && typeof session.LogFilePath==='string' && session.LogFilePath.toLowerCase()===capture.path.toLowerCase()) {
                    capture.bind();
                    owned=self.currentFingerprint()===self.bound && !screen.IsAltScreen() && session.Logging===true && typeof session.LogFilePath==='string' && session.LogFilePath.toLowerCase()===capture.path.toLowerCase();
                }
            } catch(bindError) {}
            fail('Xshell StartLog failed; outcome uncertain; inspect logging and stop it manually if ownership cannot be verified');
        }
        this.check();
        if(screen.IsAltScreen()){fail('terminal entered a full-screen application');}
        if(session.Logging!==true || typeof session.LogFilePath!=='string' || session.LogFilePath.toLowerCase()!==capture.path.toLowerCase()){fail('StartLog actual path or Logging state mismatch; inspect and stop logging manually');}
        capture.bind(); owned=true;
        checkLog();
        var frames=new LogFrames(job.id,function(text){pending+=text;});
        // Probe the held reader while Xshell owns its writer, before any Send.
        frames.feed(capture.read(),false);
        event('running'); checkLog();
        sendAttempted=true;
        try{screen.Send(commandText(job.command,job.cwd || '',job.id));}catch(sendError){fail('Screen.Send failed; command outcome unknown');}
        var now=new Date().getTime(), started=now, previous=now, deadline=now+job.timeout_ms, lastContact=now;
        function clock() {
            now=new Date().getTime();
            if(now<previous){fail('local clock moved backwards; remote command may still run');}
            if(now>deadline){fail('deadline exceeded (timeout_ms='+job.timeout_ms+', elapsed_ms='+(now-started)+'); remote command may still run');}
            previous=now;
        }
        while(true) {
            checkLog(); clock();
            var waited=screen.WaitForStrings(['__XSH_DONE_'+job.id+'\r\n','__XSH_DONE_'+job.id+'\n'],200);
            checkLog(); clock();
            if(waited!==1 && waited!==2 && waited!==0 && waited!==-1){fail('capture WaitForStrings returned invalid result: '+Number(waited));}
            frames.feed(capture.read(),false);
            if(waited===1 || waited===2) {
                stopOwned();
                // Drain in bounded native reads; no completion until EOF and decoder finalization.
                while(true){this.check();if(screen.IsAltScreen()){fail('terminal entered a full-screen application');}clock();var tail=capture.read(true);frames.feed(tail,false);if(!tail.length && capture.eof===true){break;}flush();}
                frames.feed([],true);
                if(!frames.started || frames.code===null){fail('log frame incomplete; use printable text logging without timestamps or ANSI');}
                flush();
                capture.check();
                if(!logOwned() || session.Logging!==false){fail('log ownership/path/Logging changed during finalization; check foreign logging manually');}
                // Keep evidence until the completion acknowledgement is certain.
                event('completed','',frames.code); completedAck=true;
                if(!logOwned() || session.Logging!==false){fail('log ownership changed before cleanup; check foreign logging manually');}
                session.LogFilePath=this.logBaseline;this.lastCapturePath=ownedPath;
                var finalPath=session.LogFilePath;
                if(session.Logging!==false || typeof finalPath!=='string' || (finalPath.toLowerCase()!==ownedPath.toLowerCase() && finalPath.toLowerCase()!==this.logBaseline.toLowerCase())){fail('logging changed before cleanup; check foreign logging manually');}
                cleanupAttempted=true; capture.close(false); capture=null; return;
            }
            if(pending.length>=4000 || now-lastContact>=500) {
                flush();
                if(this.poll().interrupt){checkLog();screen.Send('\x03');fail('interrupt sent; termination is not confirmed');}
                lastContact=now;
            }
        }
    } catch(e) {
        this.stopped=true;
        var reason=(failureSnapshot || failureState())+'; '+String(e.message || e.description || e);
        try{if(capture && session.Logging===true && !logOwned()){reason+='; unverified log left untouched; check and stop it manually';}}catch(ownershipError){reason+='; log ownership unreadable; check logging manually';}
        // Stop the owned writer before a potentially slow Bridge error report.
        if(capture){try{if(logOwned() && session.Logging===true && !stopAttempted){stopOwned();}}catch(stopError){reason+='; owned log could not be finalized; stop it manually after checking its path';}}
        if(capture){reason+=(cleanupAttempted?'; owned cleanup path: ':'; retained log: ')+capture.path;}
        if(!completedAck && !transportFailed){try{flush();event('unknown','',null,eventReason(reason));}catch(ignored){}}
        throw new Error(reason);
    } finally {
        // A foreign path is never stopped or restored. Source drift still permits
        // cleanup of this cached Session's owned file, without sending terminal input.
        if(capture){
            try{if(logOwned() && session.Logging===true && !stopAttempted){stopOwned();}}catch(cleanupError){}
            try{capture.close(true);}catch(closeError){}
        }
        // Set the observed baseline for a future log only while this path remains
        // ours. The getter does not reveal the original configured preference.
        try{if(owned && logOwned() && session.Logging===false){session.LogFilePath=this.logBaseline;this.lastCapturePath=ownedPath;}}catch(baselineError){}
        screen.Synchronous=previousSync;
    }
};
Agent.prototype.run = function() {
    while (true) {
        var result = this.poll();
        if (result.job) { this.execute(result.job); }
        this.xsh.Session.Sleep(250);
    }
};
function Main() {
    var remote = null;
    try {
        if (typeof XSH_REMOTE_BUNDLE === 'undefined') { fail('export a new standalone script from the host'); }
        remote = remoteBootstrap(xsh, XSH_REMOTE_BUNDLE);
        new Agent(xsh, remote.config).run();
    } catch (e) {
        xsh.Dialog.MsgBox('Xshell Bridge agent stopped. No command is automatically retried.\n' + String(e.message || e.description || e));
    } finally {
        if (remote) { try { remote.close(); } catch (cleanupError) {} }
    }
}
