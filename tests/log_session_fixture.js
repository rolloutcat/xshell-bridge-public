// Native synthetic writer only. Never executes Screen.Send command text.
function makeLogFixture(jobReader, outputText, exitCode) {
    var shell=new ActiveXObject('WScript.Shell'),writer=null,sends=0;
    function write(op,extra){var q={op:op},k;for(k in extra){if(own(extra,k)){q[k]=extra[k];}}writer.StdIn.WriteLine(stringifyJSON(q));if(writer.StdOut.ReadLine()!=='OK'){throw new Error('synthetic log writer failed');}}
    var session={Connected:true,Path:'fixture',RemoteAddress:'fixture.invalid',RemotePort:22,SessionName:'fixture',TabText:'fixture',UserName:'fixture',Logging:false,LogFilePath:'previous.log',Sleep:function(ms){WScript.Sleep(ms);}};
    session.StartLog=function(){var fs=new ActiveXObject('Scripting.FileSystemObject'),p=this.LogFilePath;if(fs.FileExists(p)){p=p.replace(/\.log$/,'(2).log');}this.LogFilePath=p;writer=shell.Exec('"'+WRITER+'"');write('open',{path:p});this.Logging=true;};
    session.StopLog=function(){var job=jobReader();write('write',{text:'\ufeff__XSH_BEGIN_'+job.id+'\r\n'+outputText+'__XSH_END_'+job.id+':'+exitCode+'\r\n__XSH_DONE_'+job.id+'\r\n'});write('close',{});writer.StdIn.Close();this.Logging=false;};
    var screen={Synchronous:false,IsAltScreen:function(){return false;},Send:function(){sends++;},WaitForStrings:function(v,ms){
        if(v[0].indexOf('__XSH_PREFLIGHT_')===0){WScript.Sleep(ms);return -1;}
        if(v[0]!=='__XSH_DONE_'+jobReader().id+'\r\n'){throw new Error('unexpected wait');}return 1;
    }};
    return {xsh:{Session:session,Screen:screen},sends:function(){return sends;}};
}
