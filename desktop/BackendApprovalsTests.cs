using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using System.Web.Script.Serialization;
namespace XshellDesktop {
internal static class BackendApprovalsTests {
    static readonly JavaScriptSerializer json=new JavaScriptSerializer();
    static string Id(char c) {return new string(c,48);}
    static void Check(bool value,string message) {if(!value)throw new Exception(message);}
    sealed class Fixture : IDisposable {
        internal Dictionary<string,object>[] Jobs,Sessions;
        internal List<string> Writes=new List<string>();
        internal List<object> Bodies=new List<object>();
        internal bool Cap=true,Fail,MutateAfterRead;
        internal Dictionary<string,object> Policy=new Dictionary<string,object>{{"id","builtin-readonly-v5"},{"version",5},{"revision",0},{"enabled",true}};
        internal int Reads;
        internal Backend Backend;
        internal Fixture(bool observer=false) {
            Sessions=new[]{new Dictionary<string,object>{{"id",Id('a')},{"generation",Id('b')},{"fingerprint","terminal-A"},{"name","中文主机"},{"environment","test"},{"state","active"}}};
            Jobs=new[]{Job('c'),Job('d')};Backend=new Backend(Call,observer,"Local");
        }
        static Dictionary<string,object> Job(char id) {return new Dictionary<string,object>{{"id",Id(id)},{"session_id",Id('a')},{"generation",Id('b')},{"status","pending"},{"command","printf '中文'"},{"cwd","/tmp"},{"timeout_ms",15000},{"output","MUST NOT LEAK"}};}
        object Clone(object value) {return json.DeserializeObject(json.Serialize(value));}
        Task<object> Call(string method,string path,object body) {
            if(method=="POST") {
                Writes.Add(path);Bodies.Add(Clone(body));if(Fail)throw new Exception("private detail");
                if(path=="/v1/explain")return Task.FromResult(Clone(new{decision="manual",reason_code="no_match",message="审批",matched_id="",checks=new object[0]}));
                Check(path=="/v1/admin/decide-batch"||path=="/v1/admin/policy","unexpected write");return Task.FromResult<object>(new{});
            }
            Reads++;
            if(path=="/v1/health")return Task.FromResult(Clone(new{approval_explain=Cap,batch_approval=Cap,displayed_approval=Cap,builtin_policy=Cap}));
            if(path=="/v1/policy")return Task.FromResult(Clone(Policy));
            if(path=="/v1/jobs") {var result=Clone(Jobs);if(MutateAfterRead)Jobs[0]["command"]="changed-in-flight";return Task.FromResult(result);}
            if(path=="/v1/sessions")return Task.FromResult(Clone(Sessions));
            if(path=="/v1/job?id="+Id('c'))return Task.FromResult(Clone(Jobs[0]));
            throw new Exception("unexpected GET");
        }
        internal Dictionary<string,object> Request(object request,bool ok) {
            var result=(Dictionary<string,object>)Clone(Backend.Handle(json.Serialize(request)).GetAwaiter().GetResult());
            Check((bool)result["ok"]==ok,json.Serialize(result));return result;
        }
        internal object Displayed() {return Clone(Jobs.Select(j=>new{job=j,session=Sessions[0]}).ToArray());}
        internal Dictionary<string,object> Decide(string action,object items,bool ok,bool remember=false) {
            return Request(new{id=1,op="displayedDecide",context=Backend.DisplayedApprovalContext,action=action,items=items,remember=remember},ok);
        }
        public void Dispose() {Backend.Dispose();}
    }
    static void Main() {try {Run();Console.WriteLine("PASS: native displayed approvals and explain isolation");}catch(Exception e){Console.Error.WriteLine(e.Message);Environment.ExitCode=1;}}
    static void Run() {
        using(var f=new Fixture()) {
            foreach(string action in new[]{"allow","deny"}) {
                f.Request(new{id=1,op="prepare",action=action,target=Id('c')},false);
                f.Request(new{id=2,op="batchPrepare",action=action,targets=new[]{Id('c')}},false);
            }
            f.Request(new{id=3,op="batchConfirm",ticket="forged"},false);
            Check(f.Reads==0&&f.Writes.Count==0,"retired approval entry performed I/O");
        }
        using(var f=new Fixture())using(var other=new Fixture()) {
            f.Request(new{id=1,op="displayedDecide",context=other.Backend.DisplayedApprovalContext,action="allow",items=new[]{new{job=f.Jobs[0],session=f.Sessions[0]}},remember=false},false);
            f.Request(new{id=2,op="builtinPolicySet",context=other.Backend.DisplayedApprovalContext,expected=f.Policy,enabled=false},false);
            Check(f.Reads==0&&f.Writes.Count==0,"foreign native context caused I/O");
        }
        using(var f=new Fixture(true)) {
            f.Request(new{id=1,op="builtinPolicySet",context=f.Backend.DisplayedApprovalContext,expected=f.Policy,enabled=false},false);
            f.Request(new{id=2,op="displayedDecide",context=f.Backend.DisplayedApprovalContext,action="allow",items=new[]{new{job=f.Jobs[0],session=f.Sessions[0]}},remember=false},false);
            Check(f.Reads==0&&f.Writes.Count==0,"observer mutation I/O");
        }
        foreach(string field in new[]{"name","environment","fingerprint"})using(var f=new Fixture()) {
            var displayed=json.DeserializeObject(json.Serialize(new[]{new{job=f.Jobs[0],session=f.Sessions[0]}}));f.Sessions[0][field]="changed";
            f.Request(new{id=1,op="displayedDecide",context=f.Backend.DisplayedApprovalContext,action="allow",items=displayed,remember=false},false);Check(f.Writes.Count==0,"invisible identity approved");
        }
        using(var f=new Fixture()) {
            var displayed=json.DeserializeObject(json.Serialize(new[]{new{job=f.Jobs[0],session=f.Sessions[0]}}));f.MutateAfterRead=true;
            f.Request(new{id=1,op="displayedDecide",context=f.Backend.DisplayedApprovalContext,action="allow",items=displayed,remember=false},false);Check(f.Writes.Count==0,"prepare/confirm race approved");
        }
        using(var f=new Fixture()) {
            f.Request(new{id=1,op="builtinPolicySet",context=f.Backend.DisplayedApprovalContext,expected=new{id="builtin-readonly-v1",version=1,revision=0,enabled=true},enabled=false},false);
            Check(f.Writes.Count==0,"old pack accepted as v5");
        }
        using(var f=new Fixture()) {
            f.Policy["id"]="builtin-readonly-v4";f.Policy["version"]=4;
            f.Request(new{id=1,op="builtinPolicySet",context=f.Backend.DisplayedApprovalContext,expected=f.Policy,enabled=false},false);
            Check(f.Writes.Count==0,"v4 pack accepted as v5");
        }
        using(var f=new Fixture()) {
            f.Policy["id"]="builtin-readonly-v1";f.Policy["version"]=1;
            f.Request(new{id=1,op="builtinPolicySet",context=f.Backend.DisplayedApprovalContext,expected=f.Policy,enabled=false},false);
            Check(f.Writes.Count==0,"non-v5 pack was accepted");
        }
        using(var f=new Fixture()) {
            var expected=json.DeserializeObject(json.Serialize(f.Policy));f.Policy["revision"]=1;
            f.Request(new{id=1,op="builtinPolicySet",context=f.Backend.DisplayedApprovalContext,expected=expected,enabled=false},false);Check(f.Writes.Count==0,"stale pack toggled");
            f.Request(new{id=2,op="builtinPolicySet",context=f.Backend.DisplayedApprovalContext,expected=f.Policy,enabled=false},true);Check(f.Writes.SequenceEqual(new[]{"/v1/admin/policy"}),"pack route mismatch");
        }
        using(var f=new Fixture()) {
            var displayed=json.DeserializeObject(json.Serialize(new[]{new{job=f.Jobs[0],session=f.Sessions[0]}}));f.Fail=true;
            f.Request(new{id=1,op="displayedDecide",context=f.Backend.DisplayedApprovalContext,action="allow",items=displayed,remember=false},false);
            f.Request(new{id=2,op="displayedDecide",context=f.Backend.DisplayedApprovalContext,action="allow",items=displayed,remember=false},false);Check(f.Writes.Count==1,"lost displayed ack replayed");
        }
        foreach(string field in new[]{"command","status","generation","session_id","cwd","timeout_ms"})using(var f=new Fixture()) {
            var displayed=json.DeserializeObject(json.Serialize(new[]{new{job=f.Jobs[0],session=f.Sessions[0]}}));
            f.Jobs[0][field]=field=="timeout_ms"?(object)16000:field=="generation"||field=="session_id"?Id('e'):"changed";
            var result=f.Request(new{id=10,op="displayedDecide",context=f.Backend.DisplayedApprovalContext,action="allow",items=displayed,remember=false},false);
            Check(result.ContainsKey("code")&&(string)result["code"]=="DECISION_NOT_SENT","stale snapshot must certify no decision was sent");
            Check(f.Writes.Count==0,"invisible changed command approved");
        }
        using(var f=new Fixture()) {
            var displayed=json.DeserializeObject(json.Serialize(new[]{new{job=f.Jobs[0],session=f.Sessions[0]}}));
            f.Request(new{id=10,op="displayedDecide",context=f.Backend.DisplayedApprovalContext,action="allow",items=displayed,remember=true},true);
            f.Request(new{id=11,op="displayedDecide",context=f.Backend.DisplayedApprovalContext,action="allow",items=displayed,remember=true},false);
            Check(f.Writes.Count==1,"one click replayed");
            var body=(Dictionary<string,object>)f.Bodies[0];
            Check((bool)body["allow"]&&(int)body["remember_minutes"]==30&&((object[])body["items"]).Length==1,"remember scope");
        }
        using(var f=new Fixture(true)) {
            f.Decide("allow",f.Displayed(),false);Check(f.Reads==0&&f.Writes.Count==0,"observer batch I/O");
            var r=f.Request(new{id=2,op="explainJob",target=Id('c')},true);
            Check(f.Writes.SequenceEqual(new[]{"/v1/explain"}),"explain approved");
            Check((string)f.Jobs[0]["status"]=="pending"&&!json.Serialize(r).Contains("MUST NOT LEAK"),"explain changed pending or leaked output");
            Check(json.Serialize(f.Bodies[0])==json.Serialize(new{session_id=Id('a'),generation=Id('b'),command="printf '中文'",cwd="/tmp",timeout_ms=15000}),"explain scope");
        }
        using(var f=new Fixture()) {
            var item=new{job=f.Jobs[0],session=f.Sessions[0]};
            f.Decide("deny",new[]{item},true);
            var body=(Dictionary<string,object>)f.Bodies[0];
            Check(!(bool)body["allow"]&&((object[])body["items"]).Length==1&&f.Writes.Count==1,"single denial scope");
        }
        foreach(string action in new[]{"allow","deny"})using(var f=new Fixture()) {
            var displayed=f.Displayed();var r=f.Decide(action,displayed,true);f.Decide(action,displayed,false);
            Check(!json.Serialize(r).Contains("MUST NOT LEAK"),"result leaked output");
            Check(f.Writes.SequenceEqual(new[]{"/v1/admin/decide-batch"}),"batch replay");
            var body=(Dictionary<string,object>)f.Bodies[0];var items=(object[])body["items"];
            Check(body.Count==2&&(bool)body["allow"]==(action=="allow")&&items.Length==2,"batch envelope");
            for(int i=0;i<2;i++)Check(json.Serialize(items[i])==json.Serialize(new{id=Id(i==0?'c':'d'),session_id=Id('a'),generation=Id('b'),fingerprint="terminal-A",name="中文主机",environment="test",command="printf '中文'",cwd="/tmp",timeout_ms=15000}),"frozen item mismatch");
        }
        foreach(string field in new[]{"command","status","generation","session_id","cwd","timeout_ms"})using(var f=new Fixture()) {
            var displayed=f.Displayed();f.Jobs[1][field]=field=="timeout_ms"?(object)16000:field=="generation"||field=="session_id"?Id('e'):"changed";
            f.Decide("allow",displayed,false);Check(f.Writes.Count==0,"stale second job committed");
        }
        foreach(string field in new[]{"name","environment","fingerprint"})using(var f=new Fixture()) {
            var displayed=f.Displayed();f.Sessions[0][field]="changed";f.Decide("allow",displayed,false);Check(f.Writes.Count==0,"stale identity committed");
        }
        using(var f=new Fixture()) {var displayed=f.Displayed();f.Fail=true;f.Decide("allow",displayed,false);f.Decide("allow",displayed,false);Check(f.Writes.Count==1,"unknown batch outcome retried");}
        using(var f=new Fixture()) {
            f.Cap=false;f.Decide("allow",f.Displayed(),false);f.Request(new{id=2,op="explainJob",target=Id('c')},false);Check(f.Writes.Count==0,"legacy POST attempted");
        }
        using(var f=new Fixture()) {
            var item=new{job=f.Jobs[0],session=f.Sessions[0]};
            f.Decide("allow",new object[0],false);f.Decide("allow",new[]{item,item},false);
            f.Decide("allow",Enumerable.Repeat(item,21).ToArray(),false);f.Decide("deny",new[]{item},false,true);
            f.Request(new{id=1,op="displayedDecide",context=f.Backend.DisplayedApprovalContext,action="allow",items=new[]{item},remember="true"},false);
            f.Request(new{id=1,op="displayedDecide",context=f.Backend.DisplayedApprovalContext,action="allow",items=new[]{item},remember=false,extra=true},false);
            f.Request(new{id=1,op="explainJob",target=Id('c'),extra=true},false);
            Check(f.Reads==0&&f.Writes.Count==0,"malformed request I/O");
        }
        using(var f=new Fixture()) {f.Jobs[0]["id"]="bad";f.Decide("allow",f.Displayed(),false);Check(f.Reads==0&&f.Writes.Count==0,"invalid ID caused I/O");}
        using(var f=new Fixture()) {var displayed=f.Displayed();f.Jobs[1].Remove("cwd");f.Decide("deny",displayed,false);Check(f.Writes.Count==0,"incomplete current batch accepted");}
        using(var f=new Fixture()) {
            f.Jobs=Enumerable.Range(0,20).Select(i=>new Dictionary<string,object>(f.Jobs[0])).ToArray();
            for(int i=0;i<f.Jobs.Length;i++) {f.Jobs[i]["id"]=i.ToString("x48");f.Jobs[i]["command"]=new string('x',16000);}
            f.Decide("allow",f.Displayed(),false);Check(f.Writes.Count==0,"oversize batch mutated");
        }
    }
}
}
