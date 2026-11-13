'use strict';
const fs=require('node:fs'),path=require('node:path'),{spawnSync}=require('node:child_process');
const artifactRoot=path.join(__dirname,'../dist/_test-artifacts/skill-native');
if(!process.env.REPORT_DIR)fs.mkdirSync(artifactRoot,{recursive:true});
const dir=path.resolve(process.env.REPORT_DIR||fs.mkdtempSync(path.join(artifactRoot,'run-')));fs.mkdirSync(dir,{recursive:true});
const compiler='C:/Windows/Microsoft.NET/Framework64/v4.0.30319/csc.exe';
const sources=['Updates.cs','UpdateCandidate.cs','UpdateHandoff.cs','CodexLocator.cs','Integrations.cs','ConnectionProfiles.cs'];
const backend=['Backend.cs','Backend.Approvals.cs','Backend.Removal.cs','HostReverse.cs','Backend.HostReverse.cs','LocalAgent.cs','Backend.LocalAgent.cs'];
const refs=['System.Windows.Forms.dll','System.Drawing.dll','System.Web.Extensions.dll','System.Net.Http.dll','System.IO.Compression.dll','System.IO.Compression.FileSystem.dll'].map(x=>'/r:'+x);
console.log('isolated report: '+dir);
for(const fixture of ['SkillFilesTests','IntegrationsTests','IntegrationsExternalTests']){
 const exe=path.join(dir,fixture+'.exe'),src=[...sources,...(fixture==='IntegrationsTests'?backend:[]),fixture+'.cs'].map(x=>path.resolve('desktop',x));
 for(const [program,args] of [[compiler,['/nologo','/target:exe','/out:'+exe,'/resource:'+path.resolve('VERSION')+',XshellDesktop.VERSION',...refs,...src]],[exe,[]]]){const r=spawnSync(program,args,{stdio:'inherit',timeout:60000});if(r.error)console.error(r.error);if(r.status!==0)process.exit(r.status??1);}
}
