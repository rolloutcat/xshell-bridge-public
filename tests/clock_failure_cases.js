// Uses the isolated capture fixture; changes only the synthetic local clock.
(function(){
    var RealDate=Date, modes=['deadline','backwards'], i;
    for(i=0;i<modes.length;i++){
        var mode=modes[i],f=fixture({failure:true}),sent=false,index=0,error=null;
        var ticks=mode==='deadline'?[10000,10001,12001]:[10000,9999];
        var originalSend=f.screen.Send;
        f.screen.Send=function(text){originalSend(text);sent=true;};
        Date=function(){return sent?{getTime:function(){return ticks[Math.min(index++,ticks.length-1)];}}:new RealDate();};
        try{f.agent.execute(f.job);}catch(e){error=e;}finally{Date=RealDate;}
        assert(error,'clock failure missing '+mode);
        var reason=String(error.message);
        if(mode==='deadline'){
            assert(reason.indexOf('deadline exceeded (timeout_ms=2000, elapsed_ms=2001)')>=0,'deadline diagnostic: '+reason);
            assert(reason.indexOf('clock moved backwards')<0,'timeout mislabelled clock rollback');
        }else{
            assert(reason.indexOf('local clock moved backwards')>=0,'clock rollback diagnostic: '+reason);
            assert(reason.indexOf('deadline exceeded')<0,'rollback mislabelled timeout');
        }
        assert(reason.indexOf('phase=SendAttempted')>=0&&reason.indexOf('remote command may still run')>=0,'uncertainty lost');
        assert(f.events[f.events.length-1].status==='unknown','clock failure completed');
        assert(f.sends()===1&&f.stops()===1&&f.closed()===1,'owned cleanup or send count');
        try{f.agent.execute(f.job);}catch(ignored){}
        assert(f.sends()===1,'clock failure replayed');
    }
}());
