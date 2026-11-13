'use strict';
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const c=vm.createContext({WScript:{Sleep(ms){const until=Date.now()+ms;while(Date.now()<until){}}}});
vm.runInContext(fs.readFileSync('agent/xshell_agent.js','utf8')+'\n'+fs.readFileSync('agent/log_parser.js','utf8'),c);
let cases=fs.readFileSync('tests/capture_cases.js','utf8');cases=cases.slice(cases.indexOf('(function(){')+12,cases.lastIndexOf('}());'));
vm.runInContext(cases.slice(0,cases.indexOf('var r=run({})')),c);
for(const initial of ['', 'previous.log']) {
 const f=c.fixture({finalFlush:true}); let getter=initial,next='',starts=0,assigned=[];
 Object.defineProperty(f.session,'LogFilePath',{get(){return getter},set(v){next=v;assigned.push(v)}});
 f.session.StartLog=function(){starts++;getter=next;this.Logging=true};
 f.agent.execute(f.job);
 assert.equal(starts,1);assert.equal(f.sends(),1);assert.equal(getter,'private.log');assert.equal(next,initial);
 f.job.id='abc';f.agent.execute(f.job);assert.equal(f.sends(),2);assert.deepEqual(assigned,['private.log',initial,'private.log',initial]);
}
for(const mode of ['number','undefined','object','before-throw','third','throw','after-throw']){
 const f=c.fixture({failure:true});let current='old\nuser.log',assigned=false,starts=0;
 f.session.StartLog=()=>{starts++;throw Error('must not start')};
 Object.defineProperty(f.session,'LogFilePath',{get(){if(mode==='before-throw'||mode==='after-throw'&&assigned)throw Error('getter');return mode==='number'?42:mode==='undefined'?undefined:mode==='object'?{}:current;},set(value){assigned=true;if(mode==='throw')throw Error('setter');if(mode==='after-throw')current=value;if(mode==='third')current='foreign.log';}});
 let error;try{f.agent.execute(f.job)}catch(e){error=e}
 assert.ok(error);assert.match(error.message,/phase=NoSend/);assert.equal(starts,0);assert.equal(f.sends(),0);assert.equal(f.stops(),0);
}
for(const mode of ['ignored','foreign','drift','partial']) {
 const f=c.fixture({failure:true});let getter='old',next='',starts=0;
 Object.defineProperty(f.session,'LogFilePath',{get(){return getter},set(v){next=v;if(mode==='foreign')f.session.Logging=true;if(mode==='drift')f.session.TabText='changed';}});
 f.session.StartLog=function(){starts++;getter=mode==='ignored'?'foreign.log':next;this.Logging=true;if(mode==='partial')throw Error('partial');};
 let error;try{f.agent.execute(f.job)}catch(e){error=e}
 assert.ok(error);assert.equal(f.sends(),0);assert.equal(starts,mode==='foreign'||mode==='drift'?0:1);assert.equal(f.stops(),mode==='partial'?1:0);
 if(mode==='ignored')assert.match(error.message,/manually/i);
}
for(const mode of ['numbered','false','race','source','partial-source','partial-send']) {
 const f=c.fixture({failure:true});let starts=0;
 if(mode==='race') { const factory=f.agent.captureFactory; f.agent.captureFactory=function(id){const capture=factory(id),prepared=capture.prepared;capture.prepared=function(){prepared();f.session.Logging=true};return capture}; }
 f.session.StartLog=function(){starts++;this.Logging=mode!=='false';if(mode==='numbered')this.LogFilePath='private(2).log';if(mode==='source'||mode==='partial-source')this.TabText='changed';if(mode==='partial-source')throw Error('partial');};
 if(mode==='partial-send')f.screen.Send=function(){throw Error('sensitive command text')};
 let error;try{f.agent.execute(f.job)}catch(e){error=e}
 assert.ok(error);assert.equal(f.sends(),0);assert.equal(starts,mode==='race'?0:1);
 assert.equal(f.stops(),mode==='partial-send'?1:0);
 assert.match(error.message,mode==='partial-send'?/phase=SendAttempted/:/phase=NoSend/);
 assert.match(error.message,/actual=.*expected=.*Logging=/);
 if(mode==='numbered')assert.match(error.message,/private\(2\)\.log/);
 if(mode==='false')assert.match(error.message,/Logging=boolean "false"/);
 assert.doesNotMatch(error.message,/sensitive command text/);
}
{
 const f=c.fixture({failure:true}),factory=f.agent.captureFactory;
 f.agent.captureFactory=function(id){const capture=factory(id),owned=capture.owned;capture.owned=function(){owned();f.session.TabText='changed'};return capture};
 f.session.StartLog=function(){this.Logging=true;throw Error('partial')};
 let error;try{f.agent.execute(f.job)}catch(e){error=e}
 assert.ok(error);assert.equal(f.sends(),0);assert.equal(f.stops(),0);
}
{
 const f=c.fixture({finalFlush:true,failure:true}),factory=f.agent.captureFactory;
 f.agent.captureFactory=function(id){const capture=factory(id),read=capture.read;capture.read=function(){const data=read();if(!f.session.Logging)f.session.LogFilePath='user.log';return data};return capture};
 let error;try{f.agent.execute(f.job)}catch(e){error=e}
 assert.ok(error);assert.ok(!f.events.some(event=>event.status==='completed'),'path changed back to baseline during drain must not complete');
}
console.log('PASS: deferred/eager path ownership, baseline and guards');
