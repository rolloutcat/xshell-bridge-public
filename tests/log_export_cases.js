// Actual exported bootstrap + winexe + native fake writer, both WSH bitnesses.
// WRITER is a fixture executable, never a shell or target connection.
(function(){
    function assert(v,m){if(!v){throw new Error(m);}}
    var shell=new ActiveXObject('WScript.Shell'), fs=new ActiveXObject('Scripting.FileSystemObject');
    function terminal(label,encoding){
        var writer=null, sent=0, output='', calls=[], job='abc', path='previous.log', value=encoding==='gbk'?'\u4e2d':'\u4e2d\ud83d\ude42';
        function write(op,extra){var q={op:op},k;for(k in extra){if(own(extra,k)){q[k]=extra[k];}}writer.StdIn.WriteLine(stringifyJSON(q));assert(writer.StdOut.ReadLine()==='OK','writer failed');}
        var session={Connected:true,Path:'fixture',RemoteAddress:'fixture.invalid',RemotePort:22,SessionName:label,TabText:label,UserName:'fixture',Logging:false,LogFilePath:path,Sleep:function(ms){WScript.Sleep(ms);}};
        session.StartLog=function(){var p=this.LogFilePath;if(fs.FileExists(p)){p=p.replace(/\.log$/,'(2).log');}this.LogFilePath=p;writer=shell.Exec('"'+WRITER+'"');write('open',{path:p});this.Logging=true;};
        session.StopLog=function(){
            // Simulate Xshell's final flush: no file output was visible until now.
            write('write',{encoding:encoding,bom:encoding==='utf-16le'||encoding==='utf-16be'||encoding==='utf-8',text:'echo '+commandText('fixture','',job).replace(/\r/g,'')+'\n__XSH_BEGIN_'+job+'\r\n\r\nsame\r\nsame\r\n'+new Array(40001).join('x')+'\r\n'+value+'\r\n__XSH_END_'+job+':7\r\n__XSH_DONE_'+job+'\r\n'});
            write('close',{});writer.StdIn.Close();this.Logging=false;
        };
        var screen={Synchronous:false,IsAltScreen:function(){return false;},Send:function(command){sent++;assert(command.indexOf('__XSH_DONE_'+job)<0,'echo DONE');},WaitForStrings:function(v,ms){if(v[0].indexOf('__XSH_PREFLIGHT_')===0){WScript.Sleep(ms);return -1;}assert(v[0]==='__XSH_DONE_'+job+'\r\n','DONE');return 1;}};
        var api={post:function(p,d){if(p==='/v1/agent/register'){return {id:label,generation:'fixture'};}if(p==='/v1/agent/event'){calls.push(d);output+=d.output;}return {};}};
        return {xsh:{Session:session,Screen:screen},api:api,setJob:function(id){job=id;},check:function(count){var expected='',i;for(i=0;i<count;i++){expected+='\nsame\nsame\n'+new Array(40001).join('x')+'\n'+value+'\n';}assert(sent===count && calls[calls.length-1].status==='completed' && calls[calls.length-1].exit_code===7,'completion');assert(output===expected,'decoded final output');assert(session.LogFilePath===path && !session.Logging && !screen.Synchronous,'restore');}};
    }
    var a=terminal('one','utf-8'),one=null; var modes=['utf-8','utf-16le','utf-16be','gbk','gb18030'],i,fixture;
    try{
        var corrupt=parseJSON(stringifyJSON(XSH_REMOTE_BUNDLE));corrupt.helper_sha256=new Array(65).join('0');var refused=false;
        try{remoteBootstrap(a.xsh,corrupt);}catch(e){refused=true;}assert(refused,'unverified helper executed');
        for(i=0;i<modes.length;i++){fixture=terminal('fixture'+i,modes[i]);XSH_REMOTE_BUNDLE.config.log_encoding=i===1||i===2?'auto':modes[i];one=remoteBootstrap(fixture.xsh,XSH_REMOTE_BUNDLE);var agent=new Agent(fixture.xsh,one.config,fixture.api);agent.execute({id:'abc',command:'NEVER EXECUTED BY FIXTURE',timeout_ms:10000});fixture.check(1);if(i===0){fixture.setJob('def');agent.execute({id:'def',command:'NEVER EXECUTED BY FIXTURE',timeout_ms:10000});fixture.check(2);}one.close();one=null;}
    }finally{if(one){one.close();}}
    assert(!fs.FileExists(VM_ROOT+'\\agent.json'),'VM agent.json created');
    var base=shell.ExpandEnvironmentStrings('%LOCALAPPDATA%')+'\\XshellBridge-agent';
    assert(fs.GetFolder(base).Files.Count===0 && fs.GetFolder(base).SubFolders.Count===0,'owned cleanup left files');
    WScript.Echo('XSH_NATIVE_EXPORT_COMPLETED');
}());
