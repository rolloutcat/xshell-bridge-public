// ES3 runtime contract, isolated fake log capture.
(function(){
function assert(v,m){if(!v){throw new Error(m);}}
function bytes(s){var x=encodeURIComponent(s),a=[],i=0;while(i<x.length){if(x.charAt(i)==='%'){a.push(parseInt(x.substr(i+1,2),16));i+=3;}else{a.push(x.charCodeAt(i++));}}return a;}
function fixture(o){
o=o||{};var events=[],sends=0,stops=0,closed=0,polled=0,data=[],sent=false,nativeFault=false;
var session={Connected:true,Path:'fixture',RemoteAddress:'fixture',RemotePort:22,SessionName:'fixture',TabText:'fixture',UserName:'fixture',Logging:!!o.foreign,LogFilePath:'user.log'};
session.StartLog=function(){if(o.startFail){if(o.startOpened){this.Logging=true;}throw new Error('StartLog failed');}this.Logging=true;};
session.StopLog=function(){stops++;this.Logging=false;if(o.finalFlush){data=bytes('__XSH_BEGIN_abc\nfinal\n__XSH_END_abc:7\n__XSH_DONE_abc\n');}};
var screen={Synchronous:false,IsAltScreen:function(){return false;},Send:function(){sends++;sent=true;},WaitForStrings:function(v,ms){
if(v[0].indexOf('__XSH_PREFLIGHT_')===0){WScript.Sleep(ms);return -1;}
assert(v[0]==='__XSH_DONE_abc\r\n','wait must use exact DONE');
if(o.lf){assert(v[1]==='__XSH_DONE_abc\n','LF sentinel missing');return 2;}if(o.badWait){return 3;}if(o.incomplete){data=bytes('__XSH_BEGIN_abc\nconfirmed\n__XSH_END_abc:0');return 1;}if(o.drift){session.TabText='other';return -1;}if(o.foreignAfter){session.LogFilePath='foreign.log';return -1;}if(o.timeout){WScript.Sleep(5);return -1;}if(o.nativeFail){return -1;}return 1;}};
var isBound=false;
var capture={path:'private.log',eof:true,prepared:function(){if(o.precondition){throw new Error('sharing unavailable');}},bind:function(){assert(!isBound,'duplicate bind');isBound=true;},owned:function(){assert(isBound,'not bound');},check:function(){assert(isBound,'not bound');if(nativeFault){throw new Error('log truncated');}},read:function(){assert(isBound,'not bound');if(!sent){return [];}if(o.lateInvalid&&!session.Logging){if(!o.finalReads){o.finalReads=1;capture.eof=false;return [];}capture.eof=true;return [255];}if(o.nativeFail){nativeFault=true;return bytes('__XSH_BEGIN_abc\nconfirmed\n');}if(o.parseFail){o.parseFail=false;return bytes('__XSH_BEGIN_abc\nconfirmed\n').concat([255]);}if(o.timeout&&!data.length){data=bytes('__XSH_BEGIN_abc\nconfirmed\n');}var out=data;data=[];return out;},close:function(keep){closed++;assert(keep===!!o.failure,'retention');isBound=false;}};
if(!o.finalFlush){data=bytes('__XSH_BEGIN_abc\n\nsame\nsame\n'+new Array(10001).join('x')+'\n\u4e2d\ud83d\ude42\n__XSH_END_abc:7\n__XSH_DONE_abc\n');}
var api={post:function(p,d){if(p==='/v1/agent/register'){return {id:'s',generation:'g'};}if(p==='/v1/agent/event'){if(o.transport && d.status==='completed'){throw new Error('uncertain completion transport');}events.push(d);}if(p==='/v1/agent/poll'){polled++;}return {};}};
var a=new Agent({Session:session,Screen:screen},{binding_mode:'manual_terminal',environment:'fixture',logCapture:function(){return capture;}},api);
return {agent:a,session:session,screen:screen,events:events,job:{id:'abc',command:'fixture',timeout_ms:o.timeout?1:2000},sends:function(){return sends;},stops:function(){return stops;},closed:function(){return closed;},polls:function(){return polled;}};
}
function run(o){var f=fixture(o),e=null;try{f.agent.execute(f.job);}catch(x){e=x;}return {f:f,error:e};}
var r=run({}),f=r.f;assert(!r.error&&f.sends()===1&&f.stops()===1&&f.closed()===1,'normal capture: '+r.error);
var out='';for(var i=0;i<f.events.length;i++){out+=f.events[i].output;}assert(out==='\nsame\nsame\n'+new Array(10001).join('x')+'\n\u4e2d\ud83d\ude42\n','output');
assert(f.events[f.events.length-1].status==='completed'&&f.events[f.events.length-1].exit_code===7,'completion');assert(f.session.LogFilePath==='user.log'&&!f.screen.Synchronous,'restore');
r=run({lf:true});assert(!r.error&&r.f.sends()===1&&r.f.events[r.f.events.length-1].status==='completed','LF-only completion: '+r.error);
r=run({badWait:true,failure:true});assert(r.error&&r.f.events[r.f.events.length-1].status==='unknown','invalid wait accepted');
r=run({finalFlush:true});assert(!r.error&&r.f.events[1].output==='final\n','final flush');
r=run({foreign:true,failure:true});assert(r.error&&r.f.sends()===0&&r.f.stops()===0&&r.f.closed()===0,'foreign log touched');
r=run({startFail:true,failure:true});assert(r.error&&r.f.sends()===0&&r.f.stops()===0,'failed StartLog sent/stopped');
r=run({startFail:true,startOpened:true,failure:true});assert(r.error&&r.f.sends()===0&&r.f.stops()===1,'partly failed StartLog cleanup');
r=run({precondition:true,failure:true});assert(r.error&&r.f.sends()===0,'helper precondition sent');
r=run({parseFail:true,failure:true});f=r.f;assert(r.error&&f.sends()===1&&f.events[1].output==='confirmed\n'&&f.events[2].status==='unknown','parse failure output');
r=run({nativeFail:true,failure:true});assert(r.error&&r.f.events[1].output==='confirmed\n'&&r.f.events[2].status==='unknown'&&r.f.stops()===1,'native failure lost output/owned stop');
r=run({timeout:true,failure:true});assert(r.error&&r.f.sends()===1&&r.f.events[r.f.events.length-1].status==='unknown','timeout');
r=run({drift:true,failure:true});assert(r.error&&r.f.sends()===1&&r.f.polls()>0,'target drift');
r=run({foreignAfter:true,failure:true});assert(r.error&&r.f.sends()===1&&r.f.stops()===0&&r.f.session.LogFilePath==='foreign.log','foreign stop');
f=fixture({parseFail:true,failure:true});var polls=0;f.agent.poll=function(){polls++;return {job:f.job};};try{f.agent.run();}catch(e){}assert(polls===1&&f.sends()===1,'dispatch after unknown');
r=run({transport:true,failure:true});assert(r.error&&r.f.closed()===1&&r.f.events[r.f.events.length-1].status!=='unknown','uncertain transport retried');
r=run({lateInvalid:true,failure:true});assert(r.error&&r.f.events[r.f.events.length-1].status==='unknown','empty non-EOF read finalized');
r=run({incomplete:true,failure:true});assert(r.error&&r.f.events[r.f.events.length-1].status==='unknown','incomplete END completed');
try{f.agent.execute(f.job);}catch(e){}assert(f.sends()===1,'unknown job replayed directly');
assert(commandText('printf hello','','abc').indexOf('__XSH_DONE_abc')===-1,'echo matches DONE');
var text=commandText('printf hello','','abc');
assert(text.indexOf('PAGER=cat')>=0 && text.indexOf('SYSTEMD_PAGER=cat')>=0 && text.indexOf('SYSTEMD_PAGERSECURE=1')>=0 && text.indexOf('SYSTEMD_COLORS=0')>=0 && text.indexOf('GIT_PAGER=cat')>=0,'task pager environment missing');
}());
