using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;

namespace XshellDesktop {
public sealed partial class Backend {
    static bool InactiveTerminal(string state) {return state=="detached"||state=="blocked"||state=="expired";}
    async Task<Review> CaptureRemoval(string[] targets,bool stopActive) {
        var sessions=await call("GET","/v1/sessions",null).ConfigureAwait(false) as object[];
        var jobs=await call("GET","/v1/jobs",null).ConfigureAwait(false) as object[];
        if(sessions==null||jobs==null)throw new UserError("无法读取完整终端和任务列表。");
        // A null target set selects candidates only at preview. Confirmation
        // always passes the exact frozen IDs; it cannot absorb new candidates.
        var all=sessions.Select(Map).ToArray();
        if(targets==null)targets=all.Where(s=>InactiveTerminal(Str(s,"state"))).Select(s=>ID(s,"id")).OrderBy(id=>id,StringComparer.Ordinal).ToArray();
        if(targets.Length>100||targets.Distinct().Count()!=targets.Length)throw new UserError("清理最多 100 个唯一终端记录；请先逐个移除部分记录后重新预览。");
        var items=new List<object>();
        foreach(string target in targets) {
            var matches=all.Where(s=>Str(s,"id")==target).ToArray();
            if(matches.Length!=1)throw new UserError("终端记录已移除、缺失或重复；没有提交更改。");
            var item=ApprovalSession(matches[0]);string state=Str(item,"state");
            object archived;if(matches[0].TryGetValue("archived",out archived)&&!(archived is bool&&!(bool)archived))throw new UserError("终端记录已移除或无效。");
            if(!InactiveTerminal(state)&&!(stopActive&&targets.Length==1&&(state=="active"||state=="pending")))throw new UserError("批量清理只接受已经撤销、过期或阻止的终端；不会依据旧心跳停止活动接入。");
            var history=jobs.Select(Map).Where(j=>Str(j,"session_id")==target).Select(j=>new Dictionary<string,object>{{"id",ID(j,"id")},{"status",TextField(j,"status")}}).OrderBy(j=>(string)j["id"],StringComparer.Ordinal).ToArray();
            if(history.Select(j=>(string)j["id"]).Distinct().Count()!=history.Length)throw new UserError("任务快照重复，请重新查询。");
            if(history.Any(j=>new[]{"pending","queued","dispatched","running"}.Contains((string)j["status"])))throw new UserError("终端仍有待审批、排队、已派发或执行中的任务，无法移除；请先处理这些任务。");
            item["jobs"]=history;items.Add(item);
        }
        string spec=json.Serialize(new{stop_active=stopActive,items=items.ToArray()});
        if(System.Text.Encoding.UTF8.GetByteCount(spec)>120*1024)throw new UserError("终端及任务快照超过 120 KiB，请减少记录后重新预览。");
        return new Review{Action="session-removal",Target=stopActive?"stop":"inactive",Session=items.ToArray(),SpecJSON=spec,Signature=spec};
    }
    async Task<object> PrepareSessionRemoval(Dictionary<string,object> request) {
        string action=TextField(request,"action");
        if(action!="remove"&&action!="cleanup")throw new UserError("不支持的终端移除操作。");
        if(action=="cleanup"&&request.ContainsKey("target"))throw new UserError("批量清理不接受额外目标。");
        string target=action=="remove"?ID(request,"target"):null;
        InvalidateReviews();string context=ConnectionContext();
        await RequireApprovalCapability("session_removal").ConfigureAwait(false);
        // A single removal may stop the reviewed active/pending attachment.
        // The server still requires an exact state match and zero busy jobs.
        var review=await CaptureRemoval(target==null?null:new[]{target},target!=null).ConfigureAwait(false);
        if(context!=ConnectionContext())throw new UserError("连接已变化，请重新预览。");
        var items=(object[])review.Session;
        if(items.Length==0)return new{ticket=(string)null,items=items,count=0,message="没有可清理的离线终端记录。活动或待激活接入不会被批量停止。"};
        // Only label stop_active when this exact single attachment is active.
        if(target!=null&&InactiveTerminal(Str(Map(items[0]),"state"))) {
            review.Target="inactive";review.SpecJSON=json.Serialize(new{stop_active=false,items=items});review.Signature=review.SpecJSON;
        }
        review.Context=context;string ticket=SaveReview(review);
        return new{ticket=ticket,items=items,count=items.Length,stop_active=review.Target=="stop",expires=review.Expires.ToString("o"),notice="移除终端记录后从当前列表隐藏，保留任务历史、输出和名称分组。仅停止 Bridge 接入，不关闭 Xshell 或 SSH；旧脚本不能恢复该记录，重新接入须手动运行脚本并创建新记录。任一目标或任务变化则整批拒绝。"};
    }
    async Task<object> ConfirmSessionRemoval(string ticket) {
        var review=TakeReview(ticket,"session-removal");
        if(review.Context!=ConnectionContext())throw new UserError("连接已变化，请重新预览。");
        var frozen=Map(json.DeserializeObject(review.SpecJSON));
        var ids=((object[])frozen["items"]).Select(Map).Select(s=>ID(s,"id")).ToArray();
        var current=await CaptureRemoval(ids,review.Target=="stop").ConfigureAwait(false);
        if(review.Context!=ConnectionContext()||current.SpecJSON!=review.SpecJSON)throw new UserError("终端身份、状态或任务已变化；没有移除任何记录，请重新预览。");
        try {await call("POST","/v1/admin/remove-sessions",frozen).ConfigureAwait(false);}
        catch {throw new UserError("移除结果未确认。请刷新列表并查询原任务；此确认已消耗，不会自动重试或改为逐项清理。");}
        return new{recorded=true,ids=ids,message="终端记录已移除，保留任务历史；未关闭 Xshell 或 SSH。"};
    }
}
}
