'use strict';
// Compile only the existing in-memory native approval fixture; no desktop build or Xshell.
const fs=require('node:fs'),path=require('node:path'),{spawnSync}=require('node:child_process');
const dir=path.resolve(process.env.REPORT_DIR||path.join(__dirname,'../dist/_test-artifacts/approval-native'));fs.mkdirSync(dir,{recursive:true});
const exe=path.join(dir,'BackendApprovalsTests.exe');
const compiler=path.join(process.env.WINDIR||'C:/Windows','Microsoft.NET/Framework64/v4.0.30319/csc.exe');
const src=['Updates.cs','UpdateCandidate.cs','UpdateHandoff.cs','CodexLocator.cs','Backend.cs','Backend.Approvals.cs','Backend.Removal.cs','HostReverse.cs','Backend.HostReverse.cs','LocalAgent.cs','Backend.LocalAgent.cs','Integrations.cs','ConnectionProfiles.cs','BackendApprovalsTests.cs'].map(x=>path.resolve('desktop',x));
const refs=['System.Windows.Forms.dll','System.Drawing.dll','System.Web.Extensions.dll','System.Net.Http.dll','System.IO.Compression.dll','System.IO.Compression.FileSystem.dll'].map(x=>'/r:'+x);
let r=spawnSync(compiler,['/nologo','/target:exe','/out:'+exe,'/resource:'+path.resolve('VERSION')+',XshellDesktop.VERSION',...refs,...src],{stdio:'inherit',timeout:60000});
if(r.error)console.error(r.error);if(r.status!==0)process.exit(r.status??1);
r=spawnSync(exe,[],{stdio:'inherit',timeout:60000});if(r.error)console.error(r.error);process.exit(r.status??1);
