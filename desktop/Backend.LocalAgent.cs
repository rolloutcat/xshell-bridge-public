using System;
using System.Collections.Generic;
using System.Threading.Tasks;

namespace XshellDesktop {
public sealed partial class Backend {
    LocalAgent localAgent;
    LocalAgent LocalAgentManager() {
        RequireReverseContext();
        if (localAgent == null) localAgent = new LocalAgent(runtimeDirectory, ReverseManager(), Integrations.Version);
        return localAgent;
    }
    async Task<object> HandleLocalAgent(string operation, Dictionary<string,object> request) {
        try {
            switch (operation) {
            case "localAgentStatus": {
                Fields(request); await RequireReverseCapability().ConfigureAwait(false);
                return LocalAgentManager().Status();
            }
            case "localAgentPrepare": {
                Fields(request,"environment","log_encoding");
                string environment=TextField(request,"environment"), encoding=TextField(request,"log_encoding");
                InvalidateReviews(); await RequireReverseCapability().ConfigureAwait(false);
                string context=ConnectionContext(); var plan=LocalAgentManager().Prepare(environment,encoding);
                if (context != ConnectionContext()) throw new UserError("连接已变化，请重新预览。");
                var review=new Review { Action="localAgent", Context=context, LocalAgentPlan=plan };
                string ticket=SaveReview(review);
                return new { ticket=ticket, expires=review.Expires.ToString("o"), preview=plan.PublicPreview() };
            }
            case "localAgentConfirm": {
                Fields(request,"ticket");
                // Consume before every context, capability, and filesystem check.
                var review=TakeReview(TextField(request,"ticket"),"localAgent");
                RequireReverseContext();
                if(review.Context!=ConnectionContext()) throw new UserError("连接已变化，请重新预览。");
                await RequireReverseCapability().ConfigureAwait(false);
                if(review.Context!=ConnectionContext()) throw new UserError("连接已变化，请重新预览。");
                LocalAgentManager().Confirm(review.LocalAgentPlan);
                return new { installed=true };
            }
            default: throw new UserError("不支持的本机脚本操作。");
            }
        } catch (UserError) { throw; }
        catch { throw new UserError("本机脚本检查或写入失败；请核对配对、程序包和受管文件，重新检查状态，不会自动重试。"); }
    }
}
}
