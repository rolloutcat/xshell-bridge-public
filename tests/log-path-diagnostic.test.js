'use strict';
const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const source=fs.readFileSync('agent/log_path_diagnostic.js','utf8');
assert.doesNotMatch(source,/\.\s*(?:Send|StartLog|StopLog|Run|Exec|Open|Connect)\s*\(/);
for(const mode of ['normal','number','ignored','throw','throw-applied','deferred','foreign','concurrent','unreadable','active']){
 let path='old\nuser.log',logging=mode==='active',sets=[],reads=0;
 const session={};Object.defineProperty(session,'Logging',{get(){if(mode==='unreadable')throw Error('getter');return logging;}});
 Object.defineProperty(session,'LogFilePath',{get(){reads++;return mode==='number'?42:path;},set(value){sets.push(value);if(mode==='throw')throw Error('setter');if(mode==='ignored'||mode==='deferred')return;path=mode==='foreign'?'foreign.log':value;if(mode==='concurrent')logging=true;if(mode==='throw-applied'&&sets.length===1)throw Error('after assignment');}});
 const context=vm.createContext({});vm.runInContext(source,context);
 const r=context.diagnoseLogPath(session,'C:\\private\\fresh\\capture.log');
 assert.match(r,/phase=NoSend/);assert.match(r,/before|refused/);
 if(['number','active','unreadable'].includes(mode))assert.equal(sets.length,0,mode);
 if(['normal','throw-applied'].includes(mode)){assert.equal(path,'old\nuser.log');assert.equal(sets.length,2);assert.match(r,/original configured preference unknown/);}
 if(['ignored','deferred','throw','foreign','concurrent'].includes(mode)){assert.ok(sets.length<=1,mode);assert.match(r,/manual restore|untouched/);}
 if(mode==='normal')assert.match(r,/old\\nuser.log/);
}
console.log('PASS: local path diagnostic getter/setter modes; no logging or terminal operations');
