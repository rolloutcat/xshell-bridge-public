// ES3 plain-object deferred getter timing, no native accessor support required.
(function(){
 var f=fixture({}), active='', last='', configured=[], serial=0, sent=0;
 f.session.LogFilePath='baseline.log';
 f.session.StartLog=function(){this.LogFilePath=active;this.Logging=true;};
 f.session.StopLog=function(){this.Logging=false;};
 f.screen.Send=function(){sent++;};
 f.screen.WaitForStrings=function(v,ms){if(v[0].indexOf('__XSH_PREFLIGHT_')===0){WScript.Sleep(ms);return -1;}return 1;};
 f.agent.captureFactory=function(id){
   active='private-'+(++serial)+'.log';last=f.session.LogFilePath; var beforeSend=sent;
   var checked=false,c={path:active,eof:true,owned:function(){},prepared:function(){if(!checked&&!f.session.Logging&&f.session.LogFilePath===active){f.session.LogFilePath=last;checked=true;}},bind:function(){},check:function(){},read:function(){if(sent===beforeSend)return [];if(c.used)return [];c.used=true;return bytes('__XSH_BEGIN_'+id+'\nok\n__XSH_END_'+id+':7\n');},close:function(){configured.push(f.session.LogFilePath);f.session.LogFilePath=active;}};return c;
 };
 f.agent.execute({id:'first',command:'fixture',timeout_ms:2000});
 f.agent.execute({id:'second',command:'fixture',timeout_ms:2000});
 if(sent!==2 || configured.length!==2 || configured[0]!=='baseline.log'||configured[1]!=='baseline.log'||f.session.LogFilePath!=='private-2.log')throw Error('deferred path baseline');
}());
