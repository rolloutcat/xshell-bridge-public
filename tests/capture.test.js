const fs = require('fs'), vm = require('vm');
const context = vm.createContext({WScript:{Sleep:function(ms) { const until = Date.now() + ms; while (Date.now() < until) {} }}});
vm.runInContext(fs.readFileSync('agent/xshell_agent.js', 'utf8'), context);
vm.runInContext(fs.readFileSync('agent/log_parser.js', 'utf8'), context);
vm.runInContext(`var OriginalAgent=Agent; Agent=function(x,c,a){ ['Get','Rows','Columns','CurrentRow','CurrentRowInScreen'].forEach(function(k){Object.defineProperty(x.Screen,k,{get:function(){throw Error('forbidden '+k);}});}); return new OriginalAgent(x,c,a); };`, context);
vm.runInContext(fs.readFileSync('tests/capture_cases.js', 'utf8'), context);
console.log('PASS: capture fixtures');
