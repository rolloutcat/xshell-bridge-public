using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Threading;
using System.Threading.Tasks;
using System.Diagnostics;
using System.Web.Script.Serialization;

namespace XshellDesktop {
internal static class BackendTests {
    static JavaScriptSerializer json = new JavaScriptSerializer();
    static string sid = new string('a',48), gen = new string('b',48), jid = new string('c',48);
    static Dictionary<string,object> session, job;
    static int writes; static bool failWrite;
    static object Clone(object o) { return json.DeserializeObject(json.Serialize(o)); }
    static Task<object> Transport(string method,string path,object body) {
        if(method=="POST") { writes++; if(failWrite) throw new Exception("secret transport details");
            if(path=="/v1/admin/session-label") {
                var label=(Dictionary<string,object>)Clone(body);
                Check((string)label["id"]==sid && (string)label["generation"]==gen,"label target binding");
                session["name"]=label["name"];session["environment"]=label["environment"];
            } else if(path=="/v1/admin/rules") {
                var spec=(Dictionary<string,object>)Clone(body);
                Check((string)spec["session_id"]==sid && (string)spec["generation"]==gen,"rule scope");
            } else if(path=="/v1/admin/activate") {
                var target=(Dictionary<string,object>)Clone(body);Check((string)target["id"]==sid&&(string)target["generation"]==gen,"activation target binding");session["state"]="active";
            } else if(path!="/v1/admin/revoke-rule") {job["status"]="queued";} return Task.FromResult<object>(new {}); }
        if(path=="/v1/health") return Task.FromResult(Clone(new {approval_rules=true,temporary_grants=false,session_labels=true}));
        if(path=="/v1/rules") return Task.FromResult<object>(new object[0]);
        if(path=="/v1/sessions") return Task.FromResult(Clone(new object[]{session}));
        if(path=="/v1/jobs") return Task.FromResult(Clone(new object[]{job}));
        if(path.StartsWith("/v1/job?")) return Task.FromResult(Clone(job));
        if(path.StartsWith("/v1/output?")) return Task.FromResult<object>(new {output="中文",next_cursor=6,status="completed",exit_code=0});
        throw new Exception("unexpected route");
    }
    static Backend Fresh(bool readOnly=false) {
        session=new Dictionary<string,object>{{"id",sid},{"generation",gen},{"name","demo"},{"environment","test"},{"fingerprint","terminal-A"},{"state","active"}};
        job=new Dictionary<string,object>{{"id",jid},{"session_id",sid},{"generation",gen},{"status","pending"},{"command","uptime"},{"cwd","/"},{"timeout_ms",15000}};
        writes=0;failWrite=false;return new Backend(Transport,readOnly,"Local");
    }
    static Dictionary<string,object> Call(Backend b,object request) { return (Dictionary<string,object>)Clone(b.Handle(json.Serialize(request)).GetAwaiter().GetResult()); }
    static string Prepare(Backend b) { session["state"]="pending";var r=Call(b,new{id=1,op="prepare",action="activate",target=sid}); Check((bool)r["ok"],"prepare activation");return (string)((Dictionary<string,object>)r["data"])["ticket"]; }
    static void Check(bool v,string message) { if(!v) throw new Exception(message); }
    static void Rejected(Dictionary<string,object> r) { Check(!(bool)r["ok"],"request unexpectedly allowed"); }
    static object RuleSpec() { return new {name="scope",session_id=sid,generation=gen,program="git",argument_patterns=new[]{"status","(?P<go>.*)"},cwd="/",max_timeout_ms=15000,minutes=30}; }
    static string RuleTicket(Backend b) {
        var r=Call(b,new{id=1,op="rulePrepare",spec=RuleSpec()});Check((bool)r["ok"],"rule prepare");
        Check(json.Serialize(r).Contains("RE2"),"regex engine not shown");Check(writes==0,"prepare posted");
        return (string)((Dictionary<string,object>)r["data"])["ticket"];
    }
    sealed class PreparedUpdate : Backend.IPreparedUpdate {
        internal int Starts, Disposals; internal bool Result=true;
        public bool Start() { Starts++;return Result; }
        public void Dispose() { Disposals++; }
    }
    static void DesktopCloseFlows() {
        string root=Path.Combine(Path.GetTempPath(),"desktop-close-"+Guid.NewGuid().ToString("N"));Directory.CreateDirectory(root);
        try {
            for(int scenario=0;scenario<3;scenario++) {
                int posts=0; bool supported=scenario!=1, lost=scenario==2;
                Func<string,string,object,Task<object>> transport=(method,path,body)=>{
                    if(method=="GET")return Task.FromResult(Clone(new{desktop_shutdown=supported}));
                    Check(path=="/v1/admin/desktop-shutdown" && json.Serialize(body)=="{}","close route");posts++;
                    if(lost)throw new Exception("lost response");return Task.FromResult<object>(new{});
                };
                using(var b=new Backend(transport,false,"Local",root,"http://127.0.0.1:17890/",null,null)) {
                    Check(b.AcquireDesktopLifetime(),"first desktop");
                    using(var other=new Backend(transport,false,"Local",root,"http://127.0.0.1:17890/",null,null)) {
                        Check(!other.AcquireDesktopLifetime(),"second desktop acquired ownership");
                        bool rejected=false;try{other.CloseBridgeAsync().GetAwaiter().GetResult();}catch{rejected=true;}
                        Check(rejected && posts==0,"non-owner stopped bridge");
                    }
                    for(int i=0;i<2;i++) {
                        bool failed=false;try{b.CloseBridgeAsync().GetAwaiter().GetResult();}catch{failed=true;}
                        Check(failed==(scenario!=0),"close outcome");
                    }
                    Check(posts==(supported?1:0),"shutdown replay");
                }
            }
        } finally {Directory.Delete(root,true);}
    }
    static void DesktopJobFlows() {
        var start=new ProcessStartInfo(System.Reflection.Assembly.GetExecutingAssembly().Location,"--lifetime-owner") {UseShellExecute=false,CreateNoWindow=true,RedirectStandardOutput=true};
        using(var owner=Process.Start(start)) {
            Process child=null;
            try {
                var line=Task.Run(()=>owner.StandardOutput.ReadLine());Check(line.Wait(5000),"owner startup timeout");
                int pid;Check(Int32.TryParse(line.Result,out pid),"owner child pid");child=Process.GetProcessById(pid);
                Check(!child.HasExited,"child exited early");
                owner.Kill();Check(owner.WaitForExit(5000),"owner survived termination");
                Check(child.WaitForExit(5000),"Bridge survived desktop termination");
            } finally {
                if(!owner.HasExited)owner.Kill();
                if(child!=null){if(!child.HasExited)child.Kill();child.Dispose();}
            }
        }
    }
    static void UpdateFlows() {
        string root=Path.Combine(Path.GetTempPath(),"backend-update-"+Guid.NewGuid().ToString("N"));Directory.CreateDirectory(root);
        try {
            string source=Path.Combine(root,"source"),bundle=Path.Combine(source,"bundle");Directory.CreateDirectory(Path.Combine(bundle,"scripts"));
            foreach(string f in new[]{"xshdesk.exe","xshctl.exe","xshupdater.exe"})File.WriteAllText(Path.Combine(bundle,f),"fixture, never executed");
            File.WriteAllText(Path.Combine(bundle,"VERSION"),"1.2.3");File.WriteAllText(Path.Combine(bundle,"scripts","start-desktop.ps1"),"fixture, never executed");
            string zip=Path.Combine(root,"fixture.zip");using(var archive=System.IO.Compression.ZipFile.Open(zip,System.IO.Compression.ZipArchiveMode.Create)) { foreach(var file in Directory.GetFiles(source,"*",SearchOption.AllDirectories)) { var entry=archive.CreateEntry(file.Substring(source.Length+1).Replace('\\','/'));using(var input=File.OpenRead(file))using(var output=entry.Open())input.CopyTo(output); } }
            string hash;using(var s=System.Security.Cryptography.SHA256.Create())hash=BitConverter.ToString(s.ComputeHash(File.ReadAllBytes(zip))).Replace("-","");
            Func<Updates> imported=()=>{var u=new Updates(Path.Combine(root,Guid.NewGuid().ToString("N")),"1.0.0",(a,c)=>{throw new Exception("unexpected external command");});u.ImportZip(zip,hash);return u;};
            string runtime=Path.Combine(root,"runtime"),observer=Path.Combine(root,"observer.json");
            for(int scenario=0;scenario<7;scenario++) {
                if(scenario==4) continue;
                bool observing=false;int posts=0,preparations=0;bool busy=false,lost=scenario==2;
                var helper=new PreparedUpdate{Result=scenario!=3};
                Func<string,string,object,Task<object>> transport=(method,path,body)=>{
                    if(method=="POST") {Check(path=="/v1/admin/shutdown" && json.Serialize(body)=="{}","wrong shutdown");posts++;if(lost)throw new Exception("private shutdown detail");return Task.FromResult<object>(new{});}
                    if(path=="/v1/health")return Task.FromResult(Clone(new{safe_shutdown=true}));
                    if(path=="/v1/sessions")return Task.FromResult(Clone(busy?new object[]{new{id=sid,state="active"}}:new object[0]));
                    if(path=="/v1/jobs")return Task.FromResult(Clone(busy?new object[]{new{id=jid,status="dispatched"}}:new object[0]));
                    throw new Exception("unexpected route");
                };
                var u=imported();
                using(var b=new Backend(transport,observing,"Local",u,observing?new[]{"--mode","Local","--observer-config",observer}:new[]{"--mode","Local","--dir",runtime},scenario==6?"selected":null,p=>{preparations++;return helper;})) {
                    Rejected(Call(b,new{id=1,op="updatePrepare",path=bundle}));
                    if(scenario==6) {Rejected(Call(b,new{id=1,op="updatePrepare"}));Check(posts==0&&preparations==0,"selected profile mutated");continue;}
                    var r=Call(b,new{id=1,op="updatePrepare"});Check((bool)r["ok"],"update preview");string t=(string)((Dictionary<string,object>)r["data"])["ticket"];
                    if(scenario==0) {Call(b,new{id=2,op="snapshot"});}
                    if(scenario==1||observing)busy=true;
                    if(scenario==5)File.AppendAllText((string)((Dictionary<string,object>)r["data"])["launcher_path"],"changed");
                    r=Call(b,new{id=3,op="updateApply",ticket=t});
                    if(scenario==1||scenario==2||scenario==3||scenario==5)Rejected(r);else Check((bool)r["ok"]&&b.HandoffScheduled,"handoff not scheduled");
                    Rejected(Call(b,new{id=4,op="updateApply",ticket=t}));
                    Check(posts==((scenario==0||scenario==2||scenario==3)?1:0),"shutdown replay or observer POST");
                    Check(preparations==((scenario==1||scenario==5)?0:1),"premature local mutation");
                    Check(helper.Starts==((scenario==0||scenario==3||observing)?1:0),"unexpected helper start");
                    if(scenario==2||scenario==3)Check(json.Serialize(r).Contains("旧版本目录")&&!json.Serialize(r).Contains("private shutdown detail"),"unsafe recovery diagnostic");
                    if(b.HandoffScheduled) {Rejected(Call(b,new{id=5,op="connectionAdd",alias="x",kind="local",purpose="x",client_config=observer}));Check((bool)Call(b,new{id=6,op="updateStatus"})["ok"],"scheduled read blocked");}
                }
            }
            using(var b=new Backend((a,c,d)=>{throw new Exception("must reject before transport");},false,"Local"))Rejected(Call(b,new{id=1,op="updatePrepare"}));
        } finally {Directory.Delete(root,true);}
    }
    static void Main(string[] args) { try {
        if(args.Length==1 && args[0]=="--lifetime-child") {Thread.Sleep(60000);return;}
        if(args.Length==1 && args[0]=="--lifetime-owner") {
            using(var child=Process.Start(new ProcessStartInfo(System.Reflection.Assembly.GetExecutingAssembly().Location,"--lifetime-child"){UseShellExecute=false,CreateNoWindow=true}))
            using(var lifetime=new BridgeProcessLifetime(child)) {Console.WriteLine(child.Id);Console.Out.Flush();Thread.Sleep(60000);}
            return;
        }
        if(args.Length==1&&args[0]=="--process-diagnostic") {
            using(var backend=new Backend((m,p,v)=>{throw new Exception("offline");},true,"Local")) {
                Console.WriteLine(json.Serialize(backend.Handle("{\"id\":1,\"op\":\"processStatus\"}").GetAwaiter().GetResult()));
            }
        } else RunTests(); } catch(Exception e) { Console.Error.WriteLine(e.GetType().Name+": "+e.Message); Environment.ExitCode=1; } }
    static void ProcessStatusTests() {
        var psi=Backend.ProcessDiagnosticStartInfo();Check(psi.FileName==Path.Combine(AppDomain.CurrentDomain.BaseDirectory,"xshctl.exe")&&psi.Arguments=="processes"&&!psi.UseShellExecute&&psi.CreateNoWindow&&psi.WindowStyle==System.Diagnostics.ProcessWindowStyle.Hidden,"fixed hidden diagnostic executable");
        using(var b=new Backend((m,p,v)=>{throw new Exception("offline");},true,"Local")) {
            int calls=0;b.processSnapshot=()=>{calls++;return "{\"available\":true,\"truncated\":false,\"note\":\"<safe>\",\"processes\":[{\"pid\":12,\"parent_pid\":10,\"role\":\"mcp\",\"parent_status\":\"alive\"}]}";};
            Check((bool)Call(b,new{id=1,op="processStatus"})["ok"],"offline readonly diagnostic");
            Rejected(Call(b,new{id=1,op="processStatus",path="other.exe"}));
            Rejected(Call(b,new{id=1,op="processStatus",args="serve"}));Check(calls==1,"fixed IPC rejected before subprocess");
            foreach(string bad in new[]{"{}","[]","{bad",new string('x',131073),"{\"available\":true,\"truncated\":false,\"note\":\"\",\"processes\":[{\"pid\":-1,\"parent_pid\":0,\"role\":\"mcp\",\"parent_status\":\"alive\"}]}","{\"available\":true,\"truncated\":false,\"note\":\"\",\"processes\":[],\"command_line\":\"secret\"}"}) {
                b.processSnapshot=()=>bad;Rejected(Call(b,new{id=1,op="processStatus"}));
            }
            var rows=Enumerable.Range(1,513).Select(n=>new{pid=n,parent_pid=0,role="unknown",parent_status="unknown"}).ToArray();
            b.processSnapshot=()=>json.Serialize(new{available=true,truncated=false,note="",processes=rows});Rejected(Call(b,new{id=1,op="processStatus"}));
        }
    }
    static void RunTests() {
        ProcessStatusTests();
        using(var b=Fresh(true)) {Rejected(Call(b,new{id=1,op="rulePrepare",spec=RuleSpec()}));Rejected(Call(b,new{id=2,op="ruleRevoke",target=jid}));Check(writes==0,"readonly rules write");}
        using(var b=Fresh()) {var t=RuleTicket(b);session["environment"]="changed";Rejected(Call(b,new{id=2,op="ruleConfirm",ticket=t}));Check(writes==0,"stale rule identity");}
        using(var b=Fresh()) {var t=RuleTicket(b);Check((bool)Call(b,new{id=2,op="ruleConfirm",ticket=t})["ok"],"rule confirm");Rejected(Call(b,new{id=3,op="ruleConfirm",ticket=t}));Check(writes==1 && (string)job["status"]=="pending","rule replay or pending approval");}
        using(var b=Fresh()) {var t=RuleTicket(b);failWrite=true;Rejected(Call(b,new{id=2,op="ruleConfirm",ticket=t}));Rejected(Call(b,new{id=3,op="ruleConfirm",ticket=t}));Check(writes==1,"rule failure retried");}
        using(var b=Fresh()) {var t=RuleTicket(b);Call(b,new{id=2,op="cancelReview"});Rejected(Call(b,new{id=3,op="ruleConfirm",ticket=t}));Check(writes==0,"cancel rule");}
        DesktopCloseFlows();
        DesktopJobFlows();
        UpdateFlows();
        using(var b=Fresh()) {
            foreach(var op in new[]{"connectionList","connectionAdd","connectionSwitch","connectionRestore","connectionPrepare","connectionConfirm","attachmentGet","attachmentSet","installScript"}) Rejected(Call(b,new{id=1,op=op}));
            Check(writes==0,"retired operation wrote");
        }
        using(var b=Fresh(true)) { Rejected(Call(b,new{id=1,op="labelSession",target=sid,generation=gen,name="display",environment="group"}));Check(writes==0,"observer label write"); }
        using(var b=Fresh()) { var ticket=Prepare(b);Check((bool)Call(b,new{id=2,op="labelSession",target=sid,generation=gen,name="  display  ",environment="company"})["ok"],"label edit failed");Check((string)session["name"]=="display" && (string)job["status"]=="pending","label edited job or not trimmed");Rejected(Call(b,new{id=3,op="confirm",ticket=ticket}));Check(writes==1,"label allowed stale review"); }
        using(var b=Fresh()) { Rejected(Call(b,new{id=1,op="labelSession",target=sid,generation=gen,name="bad\n",environment="group"}));Rejected(Call(b,new{id=2,op="labelSession",target=sid,generation=gen,name=" ",environment="group"}));Rejected(Call(b,new{id=3,op="labelSession",target=sid,generation="stale",name="valid",environment="group"}));Rejected(Call(b,new{id=4,op="labelSession",target=sid,generation=gen,name="valid",environment=1}));Check(writes==0,"invalid label wrote"); }
        using(var b=Fresh()) {var t=Prepare(b);session["name"]="renamed elsewhere";Rejected(Call(b,new{id=2,op="confirm",ticket=t}));Check(writes==0,"changed display name allowed stale approval");}
        using(var b=Fresh(true)) { Rejected(Call(b,new{id=1,op="prepare",action="activate",target=sid}));Rejected(Call(b,new{id=2,op="revoke",target=jid}));Rejected(Call(b,new{id=3,op="installScript"}));Check(writes==0,"observer write"); }
        using(var b=Fresh()) { Rejected(Call(b,new{id=1,op="confirm",ticket="forged"}));Rejected(Call(b,new{id=2,op="request",path="/v1/admin/decide"}));Check(writes==0,"unreviewed write"); }
        using(var b=Fresh()) { var t=Prepare(b);session["generation"]="new-generation";Rejected(Call(b,new{id=2,op="confirm",ticket=t}));Check(writes==0,"changed generation"); }
        using(var b=Fresh()) { var t=Prepare(b);session["fingerprint"]="terminal-B";Rejected(Call(b,new{id=2,op="confirm",ticket=t}));Check(writes==0,"changed target"); }
        using(var b=Fresh()) { var t=Prepare(b);b.InvalidateReviews();Rejected(Call(b,new{id=2,op="confirm",ticket=t}));Check(writes==0,"invalidated review"); }
        using(var b=Fresh()) { var t=Prepare(b);Check((bool)Call(b,new{id=2,op="confirm",ticket=t})["ok"],"activation failed");Rejected(Call(b,new{id=3,op="confirm",ticket=t}));Check(writes==1,"duplicate write"); }
        using(var b=Fresh()) { var t=Prepare(b);failWrite=true;var r=Call(b,new{id=2,op="confirm",ticket=t});Rejected(r);Check(!json.Serialize(r).Contains("secret"),"error leak");Rejected(Call(b,new{id=3,op="confirm",ticket=t}));Check(writes==1,"uncertain write retried"); }
        using(var b=Fresh()) { Rejected(Call(b,new{id=1,op="output",target="../admin",cursor=0}));Rejected(Call(b,new{id=2,op="snapshot",token="injected"}));Check(writes==0,"invalid route write"); }
        using(var b=Fresh()) { var t=Prepare(b);Rejected(Call(b,new{id=2,op="confirm",ticket=t,remember="true"}));Check(writes==0,"invalid remember type"); }
        using(var b=Fresh()) { var t=Prepare(b);Rejected(Call(b,new{id=2,op="confirm",ticket=t,remember=true}));Check(writes==0,"activation cannot remember approval"); }
        Console.WriteLine("PASS: observer isolation, strict IPC, immutable review, target/generation change, single-use confirmation, no replay, error redaction");
    }
}
}
