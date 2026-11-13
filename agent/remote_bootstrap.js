// ASCII ES3 bootstrap; ADODB reads only verified small executable bytes, never task logs.
function remoteSHA256(bytes) {
    var h = [], k = [], n = 2, p, j, prime, words = [], i, w = [], a, b, c, d, e, f, g, z, t1, t2, out = '';
    function ro(x, n) { return (x >>> n) | (x << (32 - n)); }
    while (k.length < 64) {
        prime = true;
        for (p = 2; p * p <= n; p++) { if (n % p === 0) { prime = false; break; } }
        if (prime) {
            if (h.length < 8) { h.push((Math.sqrt(n) * 4294967296) | 0); }
            k.push((Math.pow(n, 1 / 3) * 4294967296) | 0);
        }
        n++;
    }
    for (i = 0; i < bytes.length; i++) { words[i >> 2] = (words[i >> 2] || 0) | (bytes[i] << (24 - (i % 4) * 8)); }
    words[bytes.length >> 2] = (words[bytes.length >> 2] || 0) | (128 << (24 - (bytes.length % 4) * 8));
    n = Math.floor((bytes.length + 8) / 64) * 16 + 15;
    words[n] = bytes.length * 8;
    for (i = 0; i <= n; i += 16) {
        a=h[0]; b=h[1]; c=h[2]; d=h[3]; e=h[4]; f=h[5]; g=h[6]; z=h[7];
        for (j = 0; j < 64; j++) {
            if (j < 16) { w[j] = words[i+j] | 0; }
            else { p=w[j-15]; prime=w[j-2]; w[j]=((ro(p,7)^ro(p,18)^(p>>>3))+w[j-16]+(ro(prime,17)^ro(prime,19)^(prime>>>10))+w[j-7])|0; }
            t1=(z+(ro(e,6)^ro(e,11)^ro(e,25))+((e&f)^(~e&g))+k[j]+w[j])|0;
            t2=((ro(a,2)^ro(a,13)^ro(a,22))+((a&b)^(a&c)^(b&c)))|0;
            z=g; g=f; f=e; e=(d+t1)|0; d=c; c=b; b=a; a=(t1+t2)|0;
        }
        h[0]=(h[0]+a)|0; h[1]=(h[1]+b)|0; h[2]=(h[2]+c)|0; h[3]=(h[3]+d)|0;
        h[4]=(h[4]+e)|0; h[5]=(h[5]+f)|0; h[6]=(h[6]+g)|0; h[7]=(h[7]+z)|0;
    }
    for (i=0;i<8;i++) { out += ('00000000'+(h[i]>>>0).toString(16)).slice(-8); }
    return out;
}
// Verified portable log helper only. No terminal discovery or external commands.
function remoteBase64(text) {
    if(typeof text!=='string' || text.length>400000 || !/^(?:[A-Za-z0-9+\/]{4})*(?:[A-Za-z0-9+\/]{2}==|[A-Za-z0-9+\/]{3}=)?$/.test(text)){fail('invalid native base64');}
    var alphabet='ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/',out=[],i,a,b,c,d;
    for(i=0;i<text.length;i+=4){a=alphabet.indexOf(text.charAt(i));b=alphabet.indexOf(text.charAt(i+1));c=alphabet.indexOf(text.charAt(i+2));d=alphabet.indexOf(text.charAt(i+3));out.push((a<<2)|(b>>4));if(c>=0){out.push(((b&15)<<4)|(c>>2));}if(d>=0){out.push(((c&3)<<6)|d);}}
    return out;
}
function remoteBootstrap(xsh,bundle) {
    var input=bundle && bundle.config;
    if(!input || typeof input.source_id!=='string' || !/^[0-9a-f]{32}$/.test(input.source_id) || input.binding_mode!=='manual_terminal' || typeof input.environment!=='string' || !input.environment.replace(/^\s+|\s+$/g,'').length || input.environment.length>200){fail('invalid remote configuration');}
    var encoding=typeof input.log_encoding==='undefined'?'auto':input.log_encoding;if(typeof encoding!=='string'||!/^(auto|utf-8|utf-16le|utf-16be|gbk|gb18030)$/.test(encoding)){fail('invalid log encoding');}
    var config={endpoint:input.endpoint,token:input.token,source_id:input.source_id,binding_mode:input.binding_mode,environment:input.environment,log_encoding:encoding};new BridgeAPI(config);
    if(typeof bundle.helper_sha256!=='string' || !/^[0-9a-f]{64}$/.test(bundle.helper_sha256) || typeof bundle.helper_size!=='number' || bundle.helper_size<1024 || bundle.helper_size>262144){fail('invalid embedded helper metadata; export a new script');}
    var bytes=remoteBase64(bundle.helper_base64);
    if(bytes.length!==bundle.helper_size || remoteSHA256(bytes)!==bundle.helper_sha256){fail('embedded helper size/hash mismatch');}
    var fso=new ActiveXObject('Scripting.FileSystemObject'),shell=new ActiveXObject('WScript.Shell'),directory=null,helper=null,processes=[],closed=false;
    function safePath(p){if(typeof p!=='string' || p.length>229 || !/^[A-Za-z]:\\/.test(p) || /[\x00-\x1f"<>|?*]/.test(p) || /(^|\\)\.\.?($|\\)/.test(p) || /[. ]($|\\)/.test(p) || p.indexOf(':',2)!==-1){fail('unsafe private path');}return p;}
    function folder(p){safePath(p);for(var i=3;i<=p.length;i++){if(i!==3 && i!==p.length && p.charAt(i)!=='\\'){continue;}var part=p.substr(0,i);if(!fso.FolderExists(part) || (fso.GetFolder(part).Attributes&1024)!==0){fail('unsafe/reparse private directory');}}}
    function verify(){
        folder(directory);if(!fso.FileExists(helper) || (fso.GetFile(helper).Attributes&1024)!==0 || Number(fso.GetFile(helper).Size)!==bundle.helper_size){fail('native helper changed');}
        var stream=new ActiveXObject('ADODB.Stream');
        try{stream.Type=1;stream.Open();stream.LoadFromFile(helper);if(stream.Size!==bundle.helper_size || remoteSHA256(new VBArray(stream.Read()).toArray())!==bundle.helper_sha256){fail('native helper hash changed');}}
        finally{if(stream.State){stream.Close();}}
    }
    function pause(ms){xsh.Session.Sleep(ms);}
    function stop(p){var i;for(i=0;i<100 && p.Status===0;i++){pause(20);}if(p.Status===0){p.Terminate();}for(i=0;i<25 && p.Status===0;i++){pause(20);}if(p.Status===0){fail('owned log helper did not exit');}}
    function retire(p){stop(p);for(var i=0;i<processes.length;i++){if(processes[i]===p){processes.splice(i,1);break;}}}
    function close(){
        closed=true;var i;for(i=0;i<processes.length;i++){try{if(processes[i].Status===0){processes[i].Terminate();}stop(processes[i]);}catch(e){return;}}
        if(directory && helper){verify();fso.DeleteFile(helper,true);var d=fso.GetFolder(directory);if(d.Files.Count===0 && d.SubFolders.Count===0){fso.DeleteFolder(directory,false);}directory=null;}
    }
    try{
        var local=safePath(shell.ExpandEnvironmentStrings('%LOCALAPPDATA%'));folder(local);
        var base=safePath(local+'\\XshellBridge-agent');if(fso.FileExists(base)){fail('private base collision');}if(!fso.FolderExists(base)){fso.CreateFolder(base);}folder(base);
        var name=String(fso.GetTempName());if(!/^[A-Za-z0-9_.-]+$/.test(name)){fail('invalid private name');}
        directory=safePath(base+'\\helper-'+bundle.helper_sha256.substr(0,16)+'-'+name);if(fso.FileExists(directory)||fso.FolderExists(directory)){fail('private extraction collision');}fso.CreateFolder(directory);folder(directory);
        helper=safePath(directory+'\\XshLogCapture.exe');
        var xml=new ActiveXObject('MSXML2.DOMDocument.6.0'),node=xml.createElement('binary');node.dataType='bin.base64';node.text=bundle.helper_base64;
        var s=new ActiveXObject('ADODB.Stream');try{s.Type=1;s.Open();s.Write(node.nodeTypedValue);s.SaveToFile(helper,1);}finally{if(s.State){s.Close();}}verify();
    }catch(e){try{close();}catch(ignored){}throw e;}
    function start(nonce){
        if(closed || typeof nonce!=='string' || !/^[A-Za-z0-9_-]{1,96}$/.test(nonce)){fail('invalid native capture start');}verify();
        // This is an executable path with one fixed argument, never a shell or job command.
        var p=shell.Exec('"'+helper+'" --stdio-v1'),seq=0,ended=false;processes.push(p);
        function request(op,extra){
            if(ended || p.Status!==0){fail('native log helper exited; no command is retried');}
            var data={seq:++seq,op:op},k;for(k in extra){if(own(extra,k)){data[k]=extra[k];}}
            p.StdIn.WriteLine(stringifyJSON(data));
            // The verified helper bounds responses to 24 KiB and exits on native idle/lifetime limits.
            var line=String(p.StdOut.ReadLine());if(line.length>24576){fail('native response too large');}
            var r=parseJSON(line);if(r.seq!==seq || r.ok!==true){fail('native log capture: '+String(r.error || 'protocol mismatch')+(r.retained?'; retained log: '+r.retained:''));}return r;
        }
        try{
            var ready=request('prepare',{nonce:nonce,encoding:encoding});safePath(ready.path);
            var identity=null;
            var reader={path:ready.path,eof:false,prepared:function(){request('prepared',{});},
                bind:function(){if(identity!==null){fail('native log already bound');}var r=request('bind',{});if(typeof r.identity!=='string' || !/^[0-9a-f]{24}$/.test(r.identity)){fail('invalid native file identity');}identity=r.identity;},
                check:function(){var r=request('check',{});if(identity===null || r.identity!==identity){fail('native file identity mismatch');}},
                owned:function(){var r=request('owned',{});if(identity===null || r.identity!==identity){fail('native file identity mismatch');}},
                read:function(final){var r=request('read',{final:final===true}),b=remoteBase64(r.data);if(b.length>16384 || typeof r.eof!=='boolean'){fail('invalid native read');}reader.eof=r.eof;return b;},
                close:function(keep){try{request('close',{keep:keep});}finally{ended=true;try{p.StdIn.Close();}catch(e){}retire(p);}}};
            return reader;
        }catch(e){try{p.Terminate();retire(p);}catch(ignored){}throw e;}
    }
    config.logCapture=start;
    return {config:config,close:close};
}
