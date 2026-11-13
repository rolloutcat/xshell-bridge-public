'use strict';
// Execute only generated synthetic commands in a local POSIX shell.
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),assert=require('node:assert/strict'),{spawnSync}=require('node:child_process');
const c=vm.createContext({});vm.runInContext(fs.readFileSync('agent/xshell_agent.js','utf8'),c);
let sh='sh',shellPath=process.env.PATH;
if(process.platform==='win32'){
 const git=spawnSync('where.exe',['git.exe'],{encoding:'utf8'});
 assert.equal(git.status,0,'Git for Windows must be available on PATH');
 const gitRoot=path.dirname(path.dirname(git.stdout.trim().split(/\r?\n/)[0]));
 sh=path.join(gitRoot,'bin','sh.exe');
 shellPath=[path.join(gitRoot,'usr','bin'),path.dirname(sh),shellPath].join(path.delimiter);
}
function run(command,cwd){
 const generated=c.commandText(command,cwd,'fixture-id');
 const outer="PAGER=outer SYSTEMD_PAGER=outer SYSTEMD_PAGERSECURE=outer SYSTEMD_COLORS=outer GIT_PAGER=outer; export PAGER SYSTEMD_PAGER SYSTEMD_PAGERSECURE SYSTEMD_COLORS GIT_PAGER\n"+generated+"\nprintf 'OUTSIDE:%s:%s:%s:%s:%s\\n' \"$PAGER\" \"$SYSTEMD_PAGER\" \"$SYSTEMD_PAGERSECURE\" \"$SYSTEMD_COLORS\" \"$GIT_PAGER\"\n";
 const result=spawnSync(sh,['-c',outer],{encoding:'utf8',timeout:10000,env:{...process.env,PATH:shellPath}});
 assert.equal(result.status,0,result.stderr);return result.stdout.replace(/\r/g,'');
}
const out=run("printf 'IN:%s:%s:%s:%s:%s\\n' \"$PAGER\" \"$SYSTEMD_PAGER\" \"$SYSTEMD_PAGERSECURE\" \"$SYSTEMD_COLORS\" \"$GIT_PAGER\"; printf '%s\\n' \"a'b\"; exit 7",'/tmp');
assert.match(out,/__XSH_BEGIN_fixture-id\nIN:cat:cat:1:0:cat\na'b\n\n__XSH_END_fixture-id:7\n__XSH_DONE_fixture-id\nOUTSIDE:outer:outer:outer:outer:outer/);
assert.match(run('exit 0','/nonexistent-xsh-fixture-dir'),/__XSH_BEGIN_fixture-id\n\n__XSH_END_fixture-id:125\n__XSH_DONE_fixture-id\nOUTSIDE:outer:outer:outer:outer:outer/);
console.log('PASS: local synthetic shell environment, quoting, scope and framing');
