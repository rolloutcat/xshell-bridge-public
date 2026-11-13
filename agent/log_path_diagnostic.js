// Optional manual Xshell diagnostic. Never starts logging or writes terminal input.
// Kept separate from the normal exported agent. ES3 / Windows JScript compatible.
function diagnosticValue(value) {
    var type=typeof value,text;
    if(type==='string'){text=value;}else if(value===null){text='null';}else if(type==='number'||type==='boolean'||type==='undefined'){text=String(value);}else{text='<non-scalar>';}
    return type+' "'+text.replace(/[\\"\x00-\x1f\x7f-\uffff]/g,function(c){if(c==='\\'){return '\\\\';}if(c==='"'){return '\\"';}if(c==='\n'){return '\\n';}if(c==='\r'){return '\\r';}if(c==='\t'){return '\\t';}return '\\u'+('0000'+c.charCodeAt(0).toString(16)).slice(-4);})+'"';
}
function diagnoseLogPath(session,expected) {
    var lines=['phase=NoSend; assignment/readback only; no logging start'],before,after,attempted=false;
    function read(label){try{var v=session.LogFilePath;lines.push(label+'='+diagnosticValue(v));return v;}catch(e){lines.push(label+'=<getter threw>');return null;}}
    function idle(){try{return session.Logging===false;}catch(e){return false;}}
    if(!idle()){return lines.join('\n')+'\nrefused: already logging or unreadable Logging; untouched';}
    before=read('before');
    if(typeof before!=='string'||typeof expected!=='string'||!/^[A-Za-z]:\\/.test(expected)||/[\x00-\x1f]/.test(expected)||before.toLowerCase()===expected.toLowerCase()){
        return lines.join('\n')+'\nrefused: unreadable/non-string original or invalid fresh local path; untouched';
    }
    lines.push('expected='+diagnosticValue(expected));
    if(!idle()){return lines.join('\n')+'\nrefused: Logging changed; untouched';}
    try{attempted=true;session.LogFilePath=expected;lines.push('setter=returned');}catch(e){lines.push('setter=threw (cause unknown)');}
    after=read('after');
    // Do not "restore" a getter still showing the old path: a deferred setter
    // may have changed invisible state. Nor touch foreign or concurrent logging.
    if(attempted&&typeof after==='string'&&after===expected&&idle()){
        try{
            if(session.LogFilePath===expected&&idle()){
                session.LogFilePath=before;
                var restored=read('restore_readback');
                lines.push(restored===before&&idle()?'baseline setter returned and readback matched; original configured preference unknown':'manual restore required: restoration not confirmed');
            }else{lines.push('manual restore required: path or Logging changed; untouched');}
        }catch(e){lines.push('manual restore required: restoration failed; cause unknown');}
    }else{lines.push('manual restore guidance: verify the actual next-log setting in Xshell after all logging is stopped; deferred setter state cannot be proven, foreign logging untouched');}
    lines.push('This does not verify StartLog, encoding, file sharing or capture compatibility.');
    return lines.join('\n');
}
function Main() {
    var report='phase=NoSend; diagnostic setup refused; no logging or terminal input';
    try {
        // FSO only, no process launch. Use a fresh dedicated local temp folder;
        // inherit the current user temp permissions; no operational files read.
        var fso=new ActiveXObject('Scripting.FileSystemObject'),temp=String(fso.GetSpecialFolder(2)),folder=fso.GetFolder(temp),p=temp;
        if(!/^[A-Za-z]:\\/.test(temp)||fso.GetDrive(fso.GetDriveName(temp)).DriveType!==2){throw new Error('local user temp required');}
        while(p){if((fso.GetFolder(p).Attributes&1024)!==0){throw new Error('reparse directory refused');}var parent=fso.GetParentFolderName(p);if(parent===p){break;}p=parent;}
        var candidate=fso.BuildPath(temp,'xsh-log-diagnostic-'+fso.GetTempName());
        if(fso.FolderExists(candidate)||fso.FileExists(candidate)){throw new Error('fresh path collision');}
        folder=fso.CreateFolder(candidate);
        report=diagnoseLogPath(xsh.Session,fso.BuildPath(String(folder.Path),'capture.log'));
        // Retain the empty dedicated folder when deferred state is unknown. No
        // log/result file is created; the local dialog contains all evidence.
    }catch(e){report+='\nsetup failed; cause not inferred';}
    xsh.Dialog.MsgBox(report);
}
