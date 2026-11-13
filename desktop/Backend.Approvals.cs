using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using System.Security.Cryptography;
using System.Text;

namespace XshellDesktop {
public sealed partial class Backend {
    readonly string approvalContextNonce=Guid.NewGuid().ToString("N");
    internal string DisplayedApprovalContext {get {using(var hash=SHA256.Create()){return Convert.ToBase64String(hash.ComputeHash(Encoding.UTF8.GetBytes(approvalContextNonce+ConnectionContext())));}}}
    readonly HashSet<string> displayedDecisions=new HashSet<string>();
    sealed class DecisionNotSent : Exception {internal DecisionNotSent(string message):base(message) {}}
    async Task<object> DecideDisplayed(Dictionary<string,object> request) {
        bool sent=false;
        try {
        if(TextField(request,"context")!=DisplayedApprovalContext)throw new UserError("显示快照属于其他连接上下文；没有提交决定。");
        string action=TextField(request,"action");object raw,rememberValue;
        if(action!="allow"&&action!="deny")throw new UserError("审批操作无效。");
        var items=request.TryGetValue("items",out raw)?raw as object[]:null;
        if(items==null||items.Length<1||items.Length>20||!request.TryGetValue("remember",out rememberValue)||!(rememberValue is bool))throw new UserError("审批需要 1–20 个完整显示快照。");
        bool remember=(bool)rememberValue;if(remember&&(action!="allow"||items.Length!=1))throw new UserError("记住授权仅适用于单条批准。");
        var normalized=new List<object>();var ids=new List<string>();
        foreach(var value in items) {
            var item=Map(value);Fields(item,"job","session");
            var job=ApprovalJob(Map(item["job"]));var session=ApprovalSession(Map(item["session"]));
            if(Str(job,"status")!="pending"||Str(session,"state")!="active"||Str(job,"session_id")!=Str(session,"id")||Str(job,"generation")!=Str(session,"generation"))throw new UserError("显示的审批身份无效。");
            ids.Add(Str(job,"id"));normalized.Add(new{job=job,session=session});
        }
        if(ids.Distinct().Count()!=ids.Count)throw new UserError("任务不能重复。");
        string context=ConnectionContext();
        if(ids.Any(id=>displayedDecisions.Contains(context+":"+id))){sent=true;throw new UserError("此任务的决定已发送。仅查询原任务，不重复审批。");}
        InvalidateReviews();await RequireApprovalCapability("displayed_approval").ConfigureAwait(false);
        var prepared=await CaptureBatch(action,ids.ToArray()).ConfigureAwait(false);
        if(context!=ConnectionContext()||prepared.Signature!=json.Serialize(normalized))throw new UserError("当前显示的命令或目标已变化，没有提交决定。请刷新后核对。");
        // Internal prepare/confirm: compare the displayed snapshot to BOTH fresh
        // native captures; the server repeats the comparison under its store lock.
        var current=await CaptureBatch(action,ids.ToArray()).ConfigureAwait(false);
        if(context!=ConnectionContext()||current.Signature!=prepared.Signature)throw new UserError("审批期间目标或命令已变化，没有提交决定。");
        var frozen=Map(json.DeserializeObject(prepared.SpecJSON));if(remember)frozen["remember_minutes"]=30;
        sent=true;foreach(string id in ids)displayedDecisions.Add(context+":"+id);
        try {await call("POST","/v1/admin/decide-batch",frozen).ConfigureAwait(false);}
        catch {throw new UserError("决定结果未确认。只查询原任务，不重试审批或重新提交命令。");}
        return new{recorded=true,ids=ids.ToArray(),message="决定已记录；任务状态将自动刷新，不代表已完成。"};
        } catch(UserError error) {if(sent)throw;throw new DecisionNotSent(error.Message);}
    }
    async Task<object> SetDisplayedPolicy(Dictionary<string,object> request) {
        if(TextField(request,"context")!=DisplayedApprovalContext)throw new UserError("规则包显示属于其他连接上下文，请刷新。");
        object value,enabled;if(!request.TryGetValue("expected",out value)||!request.TryGetValue("enabled",out enabled)||!(enabled is bool))throw new UserError("规则包开关需要完整显示状态。");
        var expected=Map(value);Fields(expected,"version","enabled","revision");
        bool knownPack=(Str(expected,"id")=="builtin-readonly-v5"&&Number(expected,"version")==5);
        if(expected.Count!=4||!knownPack||Number(expected,"revision")<0||!(expected["enabled"] is bool)||(bool)expected["enabled"]==(bool)enabled)throw new UserError("显示的默认规则包无效。");
        string context=ConnectionContext();InvalidateReviews();await RequireApprovalCapability("builtin_policy").ConfigureAwait(false);
        var current=Map(await call("GET","/v1/policy",null).ConfigureAwait(false));
        if(context!=ConnectionContext()||new[]{"id","version","revision","enabled"}.Any(key=>!current.ContainsKey(key)||!Object.Equals(current[key],expected[key])))throw new UserError("规则包状态已变化，请刷新后操作。");
        try {await call("POST","/v1/admin/policy",new{expected=expected,enabled=(bool)enabled}).ConfigureAwait(false);}
        catch {throw new UserError("规则包设置结果未确认，请查询当前状态；不会自动重试。");}
        return new{recorded=true,message=(bool)enabled?"默认只读规则已启用；只影响新提交。":"默认只读规则已停用；尚未派发的默认放行任务转为待审批。自定义规则与临时授权继续生效。"};
    }
    static bool ApprovalCapability(Dictionary<string,object> health,string key) {
        object value;return health.TryGetValue(key,out value)&&value is bool&&(bool)value;
    }
    async Task RequireApprovalCapability(string key) {
        var health=Map(await call("GET","/v1/health",null).ConfigureAwait(false));
        if(!ApprovalCapability(health,key))throw new UserError("旧版 Bridge 不支持此功能。请在 Bridge 所在机器升级后重试；不会尝试未知接口或退回逐项审批。");
    }
    Dictionary<string,object> ApprovalJob(Dictionary<string,object> source) {
        var result=new Dictionary<string,object>();
        foreach(string key in new[]{"id","session_id","generation"})result[key]=ID(source,key);
        foreach(string key in new[]{"status","command","cwd"})result[key]=TextField(source,key);
        int timeout=Number(source,"timeout_ms");if(timeout<1000||timeout>3600000)throw new UserError("任务超时时限无效。");
        result["timeout_ms"]=timeout;
        foreach(string key in new[]{"grant_id","rule_id","builtin_rule_id","approval_source"}) {
            object value;
            if(source.TryGetValue(key,out value)&&value!=null&&!(value is string))throw new UserError("任务授权来源无效。");
            result[key]=value;
        }
        return result;
    }
    Dictionary<string,object> ApprovalSession(Dictionary<string,object> source) {
        var result=new Dictionary<string,object>();
        foreach(string key in new[]{"id","generation"})result[key]=ID(source,key);
        foreach(string key in new[]{"fingerprint","name","environment","state"})result[key]=TextField(source,key);
        if(((string)result["fingerprint"]).Length==0||((string)result["name"]).Length==0)throw new UserError("会话身份不完整。");
        return result;
    }
    async Task<object> ExplainJob(string target) {
        await RequireApprovalCapability("approval_explain").ConfigureAwait(false);
        var job=ApprovalJob(Map(await call("GET","/v1/job?id="+target,null).ConfigureAwait(false)));
        if((string)job["id"]!=target)throw new UserError("任务标识不匹配。");
        var scope=new Dictionary<string,object>();
        foreach(string key in new[]{"session_id","generation","command","cwd","timeout_ms"})scope[key]=job[key];
        var explanation=await call("POST","/v1/explain",scope).ConfigureAwait(false);
        return new{job=job,explanation=explanation,notice="当前解释只针对新提交，不会追溯批准已有待审批任务；原批准依据以 builtin_rule_id、approval_source、approval_history、grant_id/rule_id 和审计为准。"};
    }
    async Task<Review> CaptureBatch(string action,string[] targets) {
        var jobs=await call("GET","/v1/jobs",null).ConfigureAwait(false) as object[];
        var sessions=await call("GET","/v1/sessions",null).ConfigureAwait(false) as object[];
        if(jobs==null||sessions==null)throw new UserError("无法读取完整任务和会话列表。");
        var preview=new List<object>();var items=new List<object>();
        foreach(string target in targets) {
            var matches=jobs.Select(Map).Where(j=>Str(j,"id")==target).ToArray();
            if(matches.Length!=1)throw new UserError("批次任务缺失或重复。");
            var job=ApprovalJob(matches[0]);
            var candidates=sessions.Select(Map).Where(s=>Str(s,"id")==Str(job,"session_id")).ToArray();
            if(candidates.Length!=1)throw new UserError("批次会话缺失或重复。");
            var session=ApprovalSession(candidates[0]);
            if(Str(job,"status")!="pending"||Str(session,"state")!="active"||Str(job,"generation")!=Str(session,"generation"))throw new UserError("批次任务或会话已变化；没有提交任何决定。");
            preview.Add(new{job=job,session=session});
            items.Add(new{id=job["id"],session_id=job["session_id"],generation=job["generation"],fingerprint=session["fingerprint"],name=session["name"],environment=session["environment"],command=job["command"],cwd=job["cwd"],timeout_ms=job["timeout_ms"]});
        }
        string specJSON=json.Serialize(new{allow=action=="allow",items=items.ToArray()});
        if(System.Text.Encoding.UTF8.GetByteCount(specJSON)>120*1024)throw new UserError("批次请求超过 120 KiB，请选择更少任务后重新预览；没有提交任何决定。");
        return new Review{SpecJSON=specJSON,Signature=json.Serialize(preview)};
    }
}
}
