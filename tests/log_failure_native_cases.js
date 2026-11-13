// Synthetic Xshell session, real embedded helper and CreateNew writer.
(function(){
    function assert(v,m){if(!v){throw Error(m);}}
    var shell=new ActiveXObject('WScript.Shell'), modes=['numbered','race','source','partial-source','partial-start','partial-send'], i;
    for(i=0;i<modes.length;i++){
        var mode=modes[i],writer=null,starts=0,stops=0,sends=0,events=[];
        function write(op,extra){var q={op:op},k;for(k in extra){if(own(extra,k)){q[k]=extra[k];}}writer.StdIn.WriteLine(stringifyJSON(q));assert(writer.StdOut.ReadLine()==='OK','writer protocol');}
        var session={Connected:true,Path:'fixture',RemoteAddress:'fixture.invalid',RemotePort:22,SessionName:'fixture',TabText:'fixture',UserName:'fixture',Logging:false,LogFilePath:'previous.log',Sleep:function(ms){WScript.Sleep(ms);}};
        session.StartLog=function(){
            starts++;var p=this.LogFilePath;if(mode==='numbered'){p=p.replace(/\.log$/,'(2).log');this.LogFilePath=p;}
            writer=shell.Exec('"'+WRITER+'"');write('open',{path:p});this.Logging=true;
            if(mode==='source'||mode==='partial-source'){this.TabText='changed';}
            if(mode==='partial-source'||mode==='partial-start'){throw Error('partial StartLog');}
        };
        session.StopLog=function(){stops++;write('close',{});writer.StdIn.Close();writer=null;this.Logging=false;};
        var screen={Synchronous:false,IsAltScreen:function(){return false;},Send:function(){sends++;if(mode==='partial-send'){throw Error('partial Send');}},WaitForStrings:function(v,ms){WScript.Sleep(ms);return -1;}};
        var api={post:function(p,d){if(p==='/v1/agent/register'){return {id:'fixture',generation:'fixture'};}if(p==='/v1/agent/event'){events.push(d);}return {};}};
        var xsh={Session:session,Screen:screen},boot=remoteBootstrap(xsh,XSH_REMOTE_BUNDLE),agent=new Agent(xsh,boot.config,api),factory=agent.captureFactory,error=null;
        if(mode==='race'){agent.captureFactory=function(id){var c=factory(id),prepared=c.prepared;c.prepared=function(){prepared();session.Logging=true;};return c;};}
        try{agent.execute({id:'abc',command:'fixture only',timeout_ms:1000});}catch(e){error=e;}
        assert(error,'missing failure '+mode);
        assert(starts===(mode==='race'?0:1),'StartLog count '+mode);
        assert(sends===(mode==='partial-send'?1:0),'Send count '+mode);
        assert(stops===((mode==='partial-start'||mode==='partial-send')?1:0),'StopLog ownership '+mode);
        assert(String(error.message).indexOf(mode==='partial-send'?'phase=SendAttempted':'phase=NoSend')>=0,'phase '+mode);
        if(mode==='numbered'){assert(String(error.message).indexOf('capture(2).log')>=0,'numbered path');}
        if(mode==='partial-send'){assert(events[events.length-1].status==='unknown','partial Send unknown');}
        // Harness closes only its own synthetic writer after observing agent behavior.
        if(writer){write('close',{});writer.StdIn.Close();writer=null;}
        boot.close();
    }
    WScript.Echo('XSH_NATIVE_FAILURES_COMPLETED');
}());
