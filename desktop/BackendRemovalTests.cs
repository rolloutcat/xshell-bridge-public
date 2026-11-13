using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Threading.Tasks;
using System.Web.Script.Serialization;
namespace XshellDesktop {
internal static class BackendRemovalTests {
    static readonly JavaScriptSerializer json=new JavaScriptSerializer();
    static string Id(char c){return new string(c,48);}
    static void Check(bool ok,string message){if(!ok)throw new Exception(message);}
    sealed class Fixture:IDisposable {
        internal Dictionary<string,object>[] Sessions,Jobs=new Dictionary<string,object>[0];
        internal List<string> Writes=new List<string>();internal List<object> Bodies=new List<object>();
        internal bool Fail;internal object Cap=true,Health;internal int Reads;internal Backend Backend;
        internal Fixture(bool readOnly=false){Sessions=new[]{Session('a','b',"detached"),Session('c','d',"blocked")};Backend=new Backend(Call,readOnly,"Local");}
        internal static Dictionary<string,object> Session(char id,char gen,string state){return new Dictionary<string,object>{{"id",Id(id)},{"generation",Id(gen)},{"state",state},{"name","历史名称"},{"environment","历史分组"},{"fingerprint","fixture-terminal"},{"last_seen","2000-01-01T00:00:00Z"}};}
        internal object Clone(object value){return json.DeserializeObject(json.Serialize(value));}
        Task<object> Call(string method,string path,object body){
            if(method=="POST"){Writes.Add(path);Bodies.Add(Clone(body));Check(path=="/v1/admin/remove-sessions","unexpected write");if(Fail)throw new Exception("private");return Task.FromResult<object>(new{});}
            Reads++;if(path=="/v1/health")return Task.FromResult(Clone(Health??new{version="fixture-bridge",session_labels=true,session_removal=Cap}));
            if(path=="/v1/sessions"||path=="/v1/admin/sessions?include_archived=true")return Task.FromResult(Clone(Sessions));
            if(path=="/v1/jobs")return Task.FromResult(Clone(Jobs));if(path=="/v1/grants"||path=="/v1/rules")return Task.FromResult(Clone(new object[0]));
            throw new Exception("unexpected path "+path);
        }
        internal Dictionary<string,object> Request(object value,bool ok){var r=(Dictionary<string,object>)Clone(Backend.Handle(json.Serialize(value)).GetAwaiter().GetResult());Check((bool)r["ok"]==ok,json.Serialize(r));return r;}
        internal string Prepare(string action="cleanup",string target=null){var args=new Dictionary<string,object>{{"id",1},{"op","sessionRemovalPrepare"},{"action",action}};if(target!=null)args["target"]=target;var r=Request(args,true);Check(Writes.Count==0,"preview wrote");return (string)((Dictionary<string,object>)r["data"])["ticket"];}
        internal void Confirm(string ticket,bool ok){Request(new{id=2,op="sessionRemovalConfirm",ticket=ticket},ok);}
        internal void AddJob(string status){Jobs=new[]{new Dictionary<string,object>{{"id",Id('e')},{"session_id",Id('a')},{"generation",Id('b')},{"status",status},{"output","NEVER LEAK OUTPUT"}}};}
        public void Dispose(){Backend.Dispose();}
    }
    static void Main(){try{Run();ExportSnapshot();Console.WriteLine("PASS: native removal authorization, immutable tickets, capability handshake and concurrency guards");}catch(Exception e){Console.Error.WriteLine(e.Message);Environment.ExitCode=1;}}
    static void ExportSnapshot(){
        string output=Environment.GetEnvironmentVariable("XSH_REMOVAL_SNAPSHOT_FIXTURE");if(String.IsNullOrEmpty(output))return;
        string input=Environment.GetEnvironmentVariable("XSH_REMOVAL_GO_FIXTURE");
        Check(!String.IsNullOrEmpty(input),"snapshot export requires isolated Go fixture");
        var fixture=(Dictionary<string,object>)json.DeserializeObject(File.ReadAllText(input));
        using(var f=new Fixture()){
            f.Health=fixture["health"];f.Sessions=((object[])fixture["sessions"]).Cast<Dictionary<string,object>>().ToArray();f.Jobs=((object[])fixture["jobs"]).Cast<Dictionary<string,object>>().ToArray();
            var packet=f.Request(new{id=1,op="snapshot"},true);var data=(Dictionary<string,object>)packet["data"];
            Check((bool)data["supports_session_removal"]&&(bool)data["supports_labels"]&&f.Sessions.Length==6,"Go/native capability handshake");
            Check(((Dictionary<string,object>)((object[])data["sessions"])[0]).ContainsKey("created_at"),"native dropped creation metadata");
            File.WriteAllText(output,json.Serialize(packet));
            string ticket=f.Prepare();f.Confirm(ticket,true);f.Confirm(ticket,false);
            var items=(object[])((Dictionary<string,object>)f.Bodies[0])["items"];
            Check(items.Length==4&&items.Cast<Dictionary<string,object>>().All(s=>new[]{"blocked","detached","expired"}.Contains((string)s["state"])),"six-terminal cleanup changed active/pending membership");
        }
    }
    static void Run(){
        using(var f=new Fixture(true)){f.Request(new{id=1,op="sessionRemovalPrepare",action="cleanup"},false);f.Confirm("forged",false);Check(f.Reads==0&&f.Writes.Count==0,"read-only removal I/O");}
        using(var f=new Fixture()){var t=f.Prepare();f.Confirm(t,true);f.Confirm(t,false);Check(f.Writes.Count==1,"replayed removal");var b=(Dictionary<string,object>)f.Bodies[0];Check(b.Count==2&&!(bool)b["stop_active"]&&((object[])b["items"]).Length==2,"batch scope");Check(!json.Serialize(b).Contains("last_seen"),"heartbeat bound into immutable review");}
        foreach(string state in new[]{"pending","active"})using(var f=new Fixture()){f.Sessions[0]["state"]=state;f.Request(new{id=1,op="sessionRemovalPrepare",action="cleanup",target=Id('a')},false);var t=f.Prepare("remove",Id('a'));f.Confirm(t,true);Check((bool)((Dictionary<string,object>)f.Bodies[0])["stop_active"],"single stop missing");}
        foreach(string status in new[]{"pending","queued","dispatched","running"})using(var f=new Fixture()){f.AddJob(status);f.Request(new{id=1,op="sessionRemovalPrepare",action="remove",target=Id('a')},false);f.Request(new{id=1,op="sessionRemovalPrepare",action="cleanup"},false);Check(f.Writes.Count==0,"busy archived");}
        foreach(string field in new[]{"generation","state","name","environment","fingerprint"})using(var f=new Fixture()){var t=f.Prepare();f.Sessions[1][field]="changed";f.Confirm(t,false);Check(f.Writes.Count==0,"changed second item partially committed");}
        foreach(string status in new[]{"pending","completed"})using(var f=new Fixture()){var t=f.Prepare();f.AddJob(status);f.Confirm(t,false);f.Confirm(t,false);Check(f.Writes.Count==0,"new job accepted or ticket reusable");}
        using(var f=new Fixture()){f.AddJob("unknown");var t=f.Prepare();Check(!json.Serialize(f.Bodies).Contains("NEVER LEAK"),"output leak");f.Jobs[0]["status"]="completed";f.Confirm(t,false);Check(f.Writes.Count==0,"job state changed");}
        using(var f=new Fixture()){var t=f.Prepare();f.Sessions=f.Sessions.Concat(new[]{Fixture.Session('e','f',"expired")}).ToArray();f.Confirm(t,true);Check(((object[])((Dictionary<string,object>)f.Bodies[0])["items"]).Length==2,"new candidate silently included");}
        using(var f=new Fixture()){var t=f.Prepare();f.Fail=true;f.Confirm(t,false);f.Confirm(t,false);Check(f.Writes.Count==1,"uncertain write retried");}
        using(var f=new Fixture()){var t=f.Prepare();f.Backend.InvalidateReviews();f.Confirm(t,false);Check(f.Writes.Count==0,"cancelled ticket committed");}
        using(var f=new Fixture()){var t=f.Prepare();typeof(Backend).GetField("connectionName",BindingFlags.NonPublic|BindingFlags.Instance).SetValue(f.Backend,"changed");f.Confirm(t,false);Check(f.Writes.Count==0,"context changed");}
        using(var f=new Fixture()){var t=f.Prepare();var reviews=(System.Collections.IDictionary)typeof(Backend).GetField("reviews",BindingFlags.NonPublic|BindingFlags.Instance).GetValue(f.Backend);var r=reviews[t];r.GetType().GetField("Expires").SetValue(r,DateTime.UtcNow.AddSeconds(-1));f.Confirm(t,false);Check(f.Writes.Count==0,"expired committed");}
        using(var f=new Fixture()){var t=f.Prepare();f.Request(new{id=2,op="batchConfirm",ticket=t},false);f.Confirm(t,false);Check(f.Writes.Count==0,"cross-action ticket");}
        using(var f=new Fixture()){f.Cap=false;f.Request(new{id=1,op="sessionRemovalPrepare",action="cleanup"},false);Check(f.Writes.Count==0,"old bridge attempted write");}
        using(var f=new Fixture()){f.Sessions=new Dictionary<string,object>[0];var r=f.Request(new{id=1,op="sessionRemovalPrepare",action="cleanup"},true);Check(((object[])((Dictionary<string,object>)r["data"])["items"]).Length==0,"empty unclear");f.Confirm("forged",false);}
        using(var f=new Fixture()){f.Sessions=new[]{f.Sessions[0],f.Sessions[0]};f.Request(new{id=1,op="sessionRemovalPrepare",action="cleanup"},false);Check(f.Writes.Count==0,"duplicate IDs");}
        using(var f=new Fixture()){f.Request(new{id=1,op="sessionRemovalConfirm",ticket="forged",path="/v1/detach"},false);Check(f.Reads==0&&f.Writes.Count==0,"arbitrary IPC");}
        using(var f=new Fixture()){f.Sessions=Enumerable.Range(0,101).Select(i=>Fixture.Session('a','b',"detached")).ToArray();for(int i=0;i<f.Sessions.Length;i++)f.Sessions[i]["id"]=i.ToString("x48");f.Request(new{id=1,op="sessionRemovalPrepare",action="cleanup"},false);Check(f.Writes.Count==0,"oversize cleanup");}
        using(var f=new Fixture()){f.Sessions[0]["state"]="active";var t=f.Prepare();f.Confirm(t,true);var items=(object[])((Dictionary<string,object>)f.Bodies[0])["items"];Check(items.Length==1&&(string)((Dictionary<string,object>)items[0])["id"]==Id('c'),"stale active included in cleanup");}
        using(var f=new Fixture()){f.AddJob("completed");var r=f.Request(new{id=1,op="sessionRemovalPrepare",action="cleanup"},true);Check(!json.Serialize(r).Contains("NEVER LEAK"),"preview leaked output");var t=(string)((Dictionary<string,object>)r["data"])["ticket"];f.Confirm(t,true);Check(!json.Serialize(f.Bodies).Contains("NEVER LEAK"),"mutation leaked output");}
        using(var f=new Fixture()){f.Sessions[0]["archived"]=true;f.Request(new{id=1,op="sessionRemovalPrepare",action="remove",target=Id('a')},false);Check(f.Writes.Count==0,"archived selected");}
        foreach(object capability in new object[]{false,"true",1,null})using(var f=new Fixture()){
            f.Cap=capability;var r=f.Request(new{id=1,op="snapshot"},true);var data=(Dictionary<string,object>)r["data"];
            Check(!(bool)data["supports_session_removal"]&&(bool)data["supports_labels"],"labels imply removal capability");
            Check((string)data["bridge_version"]=="fixture-bridge"&&(string)data["desktop_version"]==Integrations.Version,"actual version diagnostics missing");
            f.Request(new{id=1,op="sessionRemovalPrepare",action="cleanup"},false);Check(f.Writes.Count==0,"unsupported wrote");
        }
        using(var f=new Fixture()){f.Health=new{version="older",session_labels=true};var r=f.Request(new{id=1,op="snapshot"},true);Check(!(bool)((Dictionary<string,object>)r["data"])["supports_session_removal"],"missing capability assumed supported");}
        using(var f=new Fixture()){f.AddJob("unknown");f.Sessions[0]["created_at"]="2025-01-01T00:00:00Z";var t=f.Prepare();f.Sessions[0]["last_seen"]="2099-01-01T00:00:00Z";f.Sessions[0]["created_at"]="2025-01-02T00:00:00Z";f.Confirm(t,true);Check(f.Writes.Count==1&&!json.Serialize(f.Bodies).Contains("created_at"),"display timestamp affected review signature");}
        using(var f=new Fixture()){var r=f.Request(new{id=1,op="snapshot"},true);var data=(Dictionary<string,object>)r["data"];Check((bool)data["supports_session_removal"]&&((object[])data["history_sessions"]).Length==2,"history snapshot missing");}
    }
}
}
