using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Threading.Tasks;
using System.Diagnostics;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;
using System.Web.Script.Serialization;

namespace XshellDesktop {
// Native adapter: shared Bridge API and fixed local diagnostics.
public sealed partial class Backend : IDisposable {
    readonly JavaScriptSerializer json = new JavaScriptSerializer { MaxJsonLength=4*1024*1024 };
    Func<string,string,object,Task<object>> call;
    string[] originalArgs;
    string selectedProfile, connectionName="宿主机工作台", connectionType="local", connectionPurpose="", connectionEndpoint;
    static HttpClient NewClient(ConnectionProfiles.Client config) {
        var client=new HttpClient(new HttpClientHandler { UseProxy=false,AllowAutoRedirect=false }) { BaseAddress=config.Endpoint,Timeout=TimeSpan.FromSeconds(8),MaxResponseContentBufferSize=2*1024*1024 };
        try { client.DefaultRequestHeaders.Authorization=new AuthenticationHeaderValue("Bearer",config.Token); return client; }
        catch { client.Dispose(); throw new UserError("客户端凭据无效。"); }
    }
    bool disposed;
    FileStream desktopLifetime;
    BridgeProcessLifetime bridgeLifetime;
    bool closeAttempted, closeAccepted;
    public bool AcquireDesktopLifetime() {
        RequireReverseContext();
        if(desktopLifetime!=null)return true;
        string path=Path.Combine(runtimeDirectory,".desktop-owner.lock");ConnectionProfiles.Safe(path);
        try {desktopLifetime=new FileStream(path,FileMode.OpenOrCreate,FileAccess.ReadWrite,FileShare.None);return true;}
        catch(IOException e) {if((e.HResult & 0xffff)==32)return false;throw;}
    }
    public async Task BindBridgeLifetimeAsync() {
        if(desktopLifetime==null)throw new UserError("工作台未取得运行目录所有权。");
        string marker=Path.Combine(runtimeDirectory,"bridge.lock");ConnectionProfiles.Safe(marker);
        int pid;if(!Int32.TryParse(File.ReadAllText(marker).Trim(),out pid))throw new UserError("Bridge 进程标记无效。");
        using(var process=Process.GetProcessById(pid)) {
            // Open and retain the process handle before asking the authenticated server
            // for its PID. PID reuse cannot retarget this handle.
            IntPtr handle=process.Handle;
            var health=Map(await call("GET","/v1/health",null).ConfigureAwait(false));
            if(Number(health,"bridge_pid")!=pid || process.HasExited ||
               !String.Equals(process.MainModule.FileName,Path.Combine(AppDomain.CurrentDomain.BaseDirectory,"xshctl.exe"),StringComparison.OrdinalIgnoreCase))
                throw new UserError("Bridge 进程身份或程序包不匹配，请先完成版本切换。");
            object capability;
            if(!health.TryGetValue("desktop_shutdown",out capability)||!(capability is bool)||!(bool)capability)
                throw new UserError("Bridge 不支持随工作台退出，请先完成版本切换。");
            bridgeLifetime=new BridgeProcessLifetime(process);
        }
    }
    public async Task CloseBridgeAsync() {
        await gate.WaitAsync().ConfigureAwait(false);
        try {
            if(HandoffScheduled || closeAccepted)return;
            RequireReverseContext();
            if(desktopLifetime==null)throw new UserError("当前窗口不是 Bridge 所属工作台。");
            if(closeAttempted)throw new UserError("Bridge 停机结果未知；不会重复发送停机请求。");
            var health=Map(await call("GET","/v1/health",null).ConfigureAwait(false));
            object capability;
            if(!health.TryGetValue("desktop_shutdown",out capability)||!(capability is bool)||!(bool)capability)
                throw new UserError("Bridge 不支持工作台退出，请更新后台程序。");
            closeAttempted=true;
            await call("POST","/v1/admin/desktop-shutdown",new{}).ConfigureAwait(false);
            closeAccepted=true;
            if(bridgeLifetime!=null && !await Task.Run(()=>bridgeLifetime.WaitForExit(12000)).ConfigureAwait(false))
                throw new UserError("Bridge 尚未完成退出。");
        } finally {gate.Release();}
    }
    readonly SemaphoreSlim gate = new SemaphoreSlim(1,1);
    readonly object reviewLock = new object();
    readonly Dictionary<string,Review> reviews = new Dictionary<string,Review>();
    HttpClient http;
    Integrations integrations;
    readonly Updates updates = new Updates();
    internal interface IPreparedUpdate : IDisposable { bool Start(); }
    sealed class NativePreparedUpdate : IPreparedUpdate {
        readonly UpdateHandoff.Prepared prepared;
        internal NativePreparedUpdate(UpdateCandidate.Plan plan) { prepared=UpdateHandoff.Prepare(plan); }
        public bool Start() { return prepared.Start(); }
        public void Dispose() { prepared.Dispose(); }
    }
    Func<UpdateCandidate.Plan,IPreparedUpdate> prepareUpdate=plan=>new NativePreparedUpdate(plan);
    public bool HandoffScheduled { get; private set; }
    internal Backend(Func<string,string,object,Task<object>> transport,bool readOnly,string mode,Updates updater,string[] args,string selection,Func<UpdateCandidate.Plan,IPreparedUpdate> preparer) : this(transport,readOnly,mode) {
        updates=updater;originalArgs=(string[])args.Clone();prepareUpdate=preparer;
        var context=OriginalUpdateContext();runtimeDirectory=context.Runtime;selectedProfile=selection;
    }
    sealed class UpdateContext { internal string Mode,Runtime; }
    UpdateContext OriginalUpdateContext() {
        if(selectedProfile!=null)throw new UserError("当前运行上下文无效，请从宿主机 Start.cmd 重新打开工作台。");
        if(originalArgs==null)throw new UserError("更新需要有效的原始启动上下文。");
        string mode="Local",dir=null,observer=null;var seen=new HashSet<string>();
        for(int i=0;i<originalArgs.Length;i+=2) {
            if(i+1>=originalArgs.Length||!seen.Add(originalArgs[i]))throw new UserError("原始启动参数无效。");
            switch(originalArgs[i]) {case "--mode":mode=originalArgs[i+1];break;case "--dir":dir=originalArgs[i+1];break;case "--observer-config":observer=originalArgs[i+1];break;default:throw new UserError("原始启动参数无效。");}
        }
        if(mode!="Local" || mode!=Mode || observer!=null || ReadOnly)throw new UserError("仅支持宿主机操作员更新。");
        if(dir==null)dir=Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"XshellBridge-"+mode.ToLowerInvariant(),"runtime");
        return new UpdateContext{Mode=mode,Runtime=Path.GetFullPath(dir)};
    }
    async Task<UpdateContext> ValidateUpdateContext() {
        GuardReverseContextChange();
        var context=OriginalUpdateContext();
        if(runtimeDirectory!=context.Runtime)throw new UserError("运行目录不匹配，请从宿主机 Start.cmd 重新打开工作台。");
        string before=ConnectionContext();
        var health=Map(await call("GET","/v1/health",null).ConfigureAwait(false));
        var sessions=await call("GET","/v1/sessions",null).ConfigureAwait(false) as object[];
        var jobs=await call("GET","/v1/jobs",null).ConfigureAwait(false) as object[];
        if(sessions==null||jobs==null)throw new UserError("无法核对更新前的会话和任务状态。");
        if(before!=ConnectionContext())throw new UserError("连接已变化，请重新预览更新。");
        {
            object safe;if(!health.TryGetValue("safe_shutdown",out safe)||!(safe is bool)||!(bool)safe)throw new UserError("Bridge 不支持安全关闭，不能应用更新。");
            var blockingSessions=sessions.Select(Map).Where(s=>Str(s,"state")=="active"||Str(s,"state")=="pending").Select(s=>Str(s,"id")).ToArray();
            var blockingJobs=jobs.Select(Map).Where(j=>new[]{"pending","queued","dispatched","running"}.Contains(Str(j,"status"))).Select(j=>Str(j,"id")).ToArray();
            if(blockingSessions.Length!=0||blockingJobs.Length!=0)throw new UserError("更新被占用状态阻止：会话 "+blockingSessions.Length+" ["+String.Join(",",blockingSessions)+"]；任务 "+blockingJobs.Length+" ["+String.Join(",",blockingJobs)+"]。请结束会话并核对任务后重新预览。");
        }
        return context;
    }
    async Task<object> PrepareUpdate() {
        InvalidateReviews();var c=await ValidateUpdateContext().ConfigureAwait(false);
        return updates.PrepareApply(false,c.Mode,c.Runtime,null);
    }
    async Task<object> ApplyUpdate(string ticket) {
        var c=await ValidateUpdateContext().ConfigureAwait(false);
        UpdateCandidate.Plan plan=null;
        updates.ConfirmApply(ticket,false,c.Mode,c.Runtime,null,p=>plan=p);
        // Confirmation is consumed and the candidate hashes/context are validated before local writes.
        using(var prepared=prepareUpdate(plan)) {
            if(prepared==null)throw new UserError("无法准备更新交接；旧版本目录已保留，没有关闭 Bridge。");
            try {await call("POST","/v1/admin/shutdown",new{}).ConfigureAwait(false);}
            catch {throw new UserError("安全关闭结果未知；桌面保持打开，旧版本目录已保留。不会启动更新助手或自动重试。请人工查询 Bridge 状态并核对运行目录和锁，再决定手动恢复。");}
            bool started=false;try {started=prepared.Start();}catch { }
            if(!started)throw new UserError("更新助手未确认启动；Bridge 可能已关闭。桌面保持打开，旧版本目录已保留；不会自动重试。请人工核对 Bridge 状态后从保留目录手动恢复。");
            HandoffScheduled=true;
            return new{scheduled=true,desktop_only=false,message="更新交接已安排；旧版本目录保留，不自动重试。"};
        }
    }
    public Backend(Func<string,string,object,Task<object>> transport,bool readOnly,string mode,Integrations manager) : this(transport,readOnly,mode) { integrations=manager; }
    Integrations LocalIntegrations() { if(integrations==null)throw new UserError("此测试或宿主未配置本机接入管理器。");return integrations; }
    string runtimeDirectory;
    public string Mode { get; private set; }
    public bool ReadOnly { get; private set; }
    public string ProfileDirectory { get; private set; }
    sealed class UserError : Exception { public UserError(string message):base(message) {} }
    sealed class Review {
        public string Action, Target, Signature;
        public DateTime Expires;
        public object Session;
        public string SpecJSON, Context;
        internal HostReverse.PreparedExport ReverseExportPlan;
        internal LocalAgent.Prepared LocalAgentPlan;
    }
    string ConnectionContext() { return json.Serialize(new object[]{selectedProfile,connectionName,connectionType,connectionPurpose,connectionEndpoint,ReadOnly,Mode,runtimeDirectory,originalArgs}); }
    string SaveReview(Review review) {
        byte[] bytes=new byte[32];using(var rng=RandomNumberGenerator.Create())rng.GetBytes(bytes);
        string ticket=Convert.ToBase64String(bytes);review.Expires=DateTime.UtcNow.AddMinutes(2);
        lock(reviewLock) { InvalidateReviews();if(disposed)throw new UserError("连接已关闭。");reviews.Add(ticket,review); }
        return ticket;
    }
    Review TakeReview(string ticket,string action) {
        Review r;lock(reviewLock) {if(!reviews.TryGetValue(ticket,out r))throw new UserError("确认已失效，请重新预览。");reviews.Remove(ticket);}
        if(r.Action!=action || DateTime.UtcNow>=r.Expires) {throw new UserError("确认已失效或过期，请重新预览。");}return r;
    }
    static string TextField(Dictionary<string,object> map,string key) {
        object v;if(!map.TryGetValue(key,out v)||!(v is string))throw new UserError("文本字段无效。");return (string)v;
    }
    object ValidateRule(object value) {
        var s=Map(value);
        string[] keys={"name","session_id","generation","program","argument_patterns","cwd","max_timeout_ms","minutes"};
        if(s.Count!=keys.Length || s.Keys.Any(k=>!keys.Contains(k)))throw new UserError("规则字段无效。");
        ID(s,"session_id");ID(s,"generation");
        var utf8=new UTF8Encoding(false,true);string name=TextField(s,"name"),program=TextField(s,"program"),cwd=TextField(s,"cwd");
        if(String.IsNullOrWhiteSpace(name)||utf8.GetByteCount(name)>200||name.Any(char.IsControl))throw new UserError("规则名称须为 1–200 UTF-8 字节且不含控制字符。");
        if(program.Length>128||!Regex.IsMatch(program,"\\A[A-Za-z0-9_][A-Za-z0-9_.-]*\\z"))throw new UserError("程序须为不超过 128 字符的 ASCII 字面名称。");
        if(utf8.GetByteCount(cwd)>4096||cwd.IndexOfAny(new[]{'\0','\r','\n'})>=0)throw new UserError("工作目录无效。");
        int timeout=Number(s,"max_timeout_ms"),minutes=Number(s,"minutes");
        if(timeout<1000||timeout>3600000||minutes<1||minutes>480)throw new UserError("规则时限无效。");
        object a;var patterns=s.TryGetValue("argument_patterns",out a)?a as object[]:null;
        if(patterns==null||patterns.Length>16)throw new UserError("参数模式须为最多 16 项的数组。");
        foreach(object p in patterns) {
            if(!(p is string)||((string)p).Any(char.IsControl)||utf8.GetByteCount((string)p)<1||utf8.GetByteCount((string)p)>256)throw new UserError("参数模式须为 1–256 UTF-8 字节且不含控制字符。");
        }
        // Patterns are deliberately not compiled with .NET Regex. Go validates RE2 at creation.
        return s;
    }
    async Task<Review> CaptureRule(string specJSON) {
        var spec=Map(json.DeserializeObject(specJSON));
        var list=await call("GET","/v1/sessions",null).ConfigureAwait(false) as object[];
        var s=list==null?null:list.Select(Map).FirstOrDefault(x=>Str(x,"id")==Str(spec,"session_id"));
        if(s==null||Str(s,"state")!="active"||Str(s,"generation")!=Str(spec,"generation"))throw new UserError("规则目标已变化或不是活动会话。");
        return new Review{Action="rule",SpecJSON=specJSON,Session=s,Signature=json.Serialize(new object[]{Str(s,"id"),Str(s,"generation"),Str(s,"fingerprint"),Str(s,"name"),Str(s,"environment"),Str(s,"state")})};
    }
    public Backend(Func<string,string,object,Task<object>> transport,bool readOnly,string mode) {
        call=transport;ReadOnly=readOnly;Mode=mode;
    }
    public Backend(string[] args) {
        originalArgs=(string[])args.Clone();
        string mode="Local",dir=null,observer=null;
        var seen=new HashSet<string>();
        for(int i=0;i<args.Length;i+=2) {
            if(i+1>=args.Length || !seen.Add(args[i])) throw new UserError("参数无效或重复。");
            switch(args[i]) { case "--mode":mode=args[i+1];break;case "--dir":dir=args[i+1];break;case "--observer-config":observer=args[i+1];break;default:throw new UserError("只支持 --mode、--dir、--observer-config。"); }
        }
        if(mode!="Local" || observer!=null) throw new UserError("仅支持宿主机操作员模式；请使用 Start.cmd。");
        Mode=mode;ReadOnly=false;
        var appData=Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData);
        if(dir==null) dir=Path.Combine(appData,"XshellBridge-"+mode.ToLowerInvariant(),"runtime");
        runtimeDirectory=Path.GetFullPath(dir);
        var configPath=Path.Combine(dir,"admin.json");
        ConnectionProfiles.Client config;
        try {
            config=ConnectionProfiles.ReadClient(Path.GetFullPath(configPath));
        } catch { throw new UserError("桌面端只接受本机数字回环服务地址与有效操作员配置。"); }
        connectionEndpoint=config.Endpoint.AbsoluteUri;
        http=NewClient(config);
        call=CallHTTP;
        integrations=new Integrations(AppDomain.CurrentDomain.BaseDirectory,Path.Combine(appData,"XshellBridge-integrations"),Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),Path.Combine(runtimeDirectory,"client.json"),null);
        // Browser profile is separate from runtime credentials and private to this user.
        ProfileDirectory=Path.Combine(appData,"XshellBridge-desktop",mode.ToLowerInvariant()+"-operator");
    }
    async Task<object> CallHTTP(string method,string path,object body) {
        using(var request=new HttpRequestMessage(new HttpMethod(method),path)) {
            if(body!=null)request.Content=new StringContent(json.Serialize(body),Encoding.UTF8,"application/json");
            using(var response=await http.SendAsync(request).ConfigureAwait(false)) {
                if(!response.IsSuccessStatusCode) throw new UserError("Bridge 拒绝请求（HTTP "+(int)response.StatusCode+"）。请刷新核对目标与任务状态。");
                return json.DeserializeObject(await response.Content.ReadAsStringAsync().ConfigureAwait(false));
            }
        }
    }
    static Dictionary<string,object> Map(object value) {
        var map=value as Dictionary<string,object>;if(map==null)throw new UserError("无效的数据对象。");return map;
    }
    static string Str(Dictionary<string,object> map,string key) {
        object value;return map.TryGetValue(key,out value)&&value is string?(string)value:"";
    }
    static int Number(Dictionary<string,object> map,string key) {
        object value;if(!map.TryGetValue(key,out value)||!(value is int))throw new UserError("无效的数字字段。");return (int)value;
    }
    static string LabelText(Dictionary<string,object> map,string key,bool required) {
        object value;if(!map.TryGetValue(key,out value)||!(value is string))throw new UserError("名称和分组必须是文本。");
        var text=(string)value;if(text.Any(char.IsControl))throw new UserError("名称和分组不能包含控制字符。");
        text=text.Trim();if((required&&text.Length==0)||new UTF8Encoding(false,true).GetByteCount(text)>200)throw new UserError("名称不能为空，名称和分组各不超过 200 个 UTF-8 字节。");return text;
    }
    static void Fields(Dictionary<string,object> request,params string[] extra) {
        var allowed=new HashSet<string>(extra.Concat(new[]{"id","op"}));
        if(request.Keys.Any(k=>!allowed.Contains(k)))throw new UserError("不支持的请求字段。");
    }
    static string ID(Dictionary<string,object> request,string field) {
        var id=Str(request,field);if(!Regex.IsMatch(id,"\\A[0-9a-f]{48}\\z"))throw new UserError("无效的任务或会话标识。");return id;
    }
    public void InvalidateReviews() { lock(reviewLock) {updates.InvalidateApply();reviews.Clear();} }
    // Fixed local diagnostic: no PATH lookup, browser arguments or Bridge calls.
    internal Func<string> processSnapshot=ReadProcessSnapshot;
    internal static System.Diagnostics.ProcessStartInfo ProcessDiagnosticStartInfo() {
        return new System.Diagnostics.ProcessStartInfo(Path.Combine(AppDomain.CurrentDomain.BaseDirectory,"xshctl.exe"),"processes") {
            UseShellExecute=false,CreateNoWindow=true,WindowStyle=System.Diagnostics.ProcessWindowStyle.Hidden,
            RedirectStandardOutput=true,RedirectStandardError=true,StandardOutputEncoding=new UTF8Encoding(false,true)
        };
    }
    static string ReadProcessSnapshot() {
        using(var p=new System.Diagnostics.Process()) {
            p.StartInfo=ProcessDiagnosticStartInfo();var output=new StringBuilder();int total=0,failed=0;
            p.Start();
            ThreadStart readOut=delegate {try {char[] buffer=new char[2048];int n;while((n=p.StandardOutput.Read(buffer,0,buffer.Length))>0) {if(Interlocked.Add(ref total,n)<=131072)output.Append(buffer,0,n);else Interlocked.Exchange(ref failed,1);}}catch {Interlocked.Exchange(ref failed,1);}};
            ThreadStart readError=delegate {try {char[] buffer=new char[2048];int n;while((n=p.StandardError.Read(buffer,0,buffer.Length))>0) {Interlocked.Exchange(ref failed,1);}}catch {Interlocked.Exchange(ref failed,1);}};
            var a=new Thread(readOut){IsBackground=true};var b=new Thread(readError){IsBackground=true};a.Start();b.Start();
            bool done=p.WaitForExit(5000);
            // Kill only the diagnostic child created by this invocation, on timeout.
            if(!done) {try {p.Kill();}catch {}}
            bool joined=a.Join(500)&b.Join(500);
            if(!done||!joined||failed!=0||p.ExitCode!=0)throw new UserError("本机进程诊断失败或超时，请手动刷新。");
            return output.ToString();
        }
    }
    static void ExactProcessFields(Dictionary<string,object> value,params string[] fields) {
        if(value.Count!=fields.Length||value.Keys.Any(k=>!fields.Contains(k)))throw new UserError("进程诊断数据格式无效。");
    }
    static void ProcessPID(Dictionary<string,object> row,string key,bool nonzero) {
        object v=row[key];if(!(v is int)&&!(v is long))throw new UserError("进程编号无效。");
        long n=Convert.ToInt64(v);if(n<(nonzero?1:0)||n>UInt32.MaxValue)throw new UserError("进程编号无效。");
    }
    internal object ParseProcessSnapshot(string raw) {
        if(raw==null||raw.Length>131072)throw new UserError("进程诊断数据过大。");
        var report=Map(json.DeserializeObject(raw));ExactProcessFields(report,"available","truncated","note","processes");
        if(!(report["available"] is bool)||!(report["truncated"] is bool)||!(report["note"] is string)||((string)report["note"]).Length>4096)throw new UserError("进程诊断数据格式无效。");
        var rows=report["processes"] as object[];if(rows==null||rows.Length>512)throw new UserError("进程诊断记录过多或无效。");
        foreach(var value in rows) {
            var row=Map(value);ExactProcessFields(row,"pid","parent_pid","role","parent_status");ProcessPID(row,"pid",true);ProcessPID(row,"parent_pid",false);
            if(!new[]{"bridge","mcp","cli","unknown"}.Contains(Str(row,"role"))||!new[]{"alive","exited","unknown"}.Contains(Str(row,"parent_status")))throw new UserError("进程角色或父进程状态无效。");
        }
        return report;
    }
    public async Task<object> Handle(string message) {
        int id=0;await gate.WaitAsync().ConfigureAwait(false);
        try {
            if(disposed)throw new UserError("连接已关闭。");
            if(message==null||message.Length>65536)throw new UserError("请求过大。");
            var request=Map(json.DeserializeObject(message));id=Number(request,"id");if(id<1)throw new UserError("无效的请求编号。");
            string operation=Str(request,"op");
            if(HandoffScheduled&&!new[]{"snapshot","output","updateStatus","integrationStatus","processStatus"}.Contains(operation))throw new UserError("更新交接已安排，不再接受新的更改。");
            object data;
            switch(operation) {
            case "processStatus":
                Fields(request);data=ParseProcessSnapshot(await Task.Run(processSnapshot).ConfigureAwait(false));break;
            case "displayedDecide":
                Fields(request,"action","items","remember","context");RequireOperator();data=await DecideDisplayed(request).ConfigureAwait(false);break;
            case "builtinPolicySet":
                Fields(request,"expected","enabled","context");RequireOperator();data=await SetDisplayedPolicy(request).ConfigureAwait(false);break;
            case "sessionRemovalPrepare":
                Fields(request,"action","target");RequireOperator();data=await PrepareSessionRemoval(request).ConfigureAwait(false);break;
            case "sessionRemovalConfirm":
                Fields(request,"ticket");RequireOperator();data=await ConfirmSessionRemoval(TextField(request,"ticket")).ConfigureAwait(false);break;
            case "explainJob":
                Fields(request,"target");data=await ExplainJob(ID(request,"target")).ConfigureAwait(false);break;
            case "updatePrepare":
                Fields(request);
                try {data=await PrepareUpdate().ConfigureAwait(false);}catch(InvalidOperationException e){throw new UserError(e.Message);}break;
            case "updateApply":
                Fields(request,"ticket");
                if(request.Count!=3)throw new UserError("更新应用只接受 ticket。");
                try {data=await ApplyUpdate(TextField(request,"ticket")).ConfigureAwait(false);}catch(InvalidOperationException e){throw new UserError(e.Message);}break;
            case "rulePrepare":
                Fields(request,"spec");RequireOperator();InvalidateReviews();
                object specValue;if(!request.TryGetValue("spec",out specValue))throw new UserError("缺少规则 spec。");
                var ruleReview=await CaptureRule(json.Serialize(ValidateRule(specValue))).ConfigureAwait(false);
                var ruleTicket=SaveReview(ruleReview);
                data=new{ticket=ruleTicket,spec=json.DeserializeObject(ruleReview.SpecJSON),session=ruleReview.Session,expires=ruleReview.Expires.ToString("o"),regex_engine="Go RE2：逐参数全匹配；Bridge 创建前进行权威语法验证，不使用 .NET Regex。",notice="创建规则不会批准已经待审批的任务。"};break;
            case "ruleConfirm":
                Fields(request,"ticket");RequireOperator();
                var storedRule=TakeReview(TextField(request,"ticket"),"rule");
                var freshRule=await CaptureRule(storedRule.SpecJSON).ConfigureAwait(false);
                if(freshRule.Signature!=storedRule.Signature)throw new UserError("会话身份已变化；没有创建规则，请重新核对。");
                try {await call("POST","/v1/admin/rules",json.DeserializeObject(storedRule.SpecJSON)).ConfigureAwait(false);}
                catch {throw new UserError("规则创建结果未确认（也可能被 Bridge RE2 验证拒绝）。请刷新规则列表核对；不会自动重试。已经待审批的任务未获批准。");}
                data=new{recorded=true,message="规则已创建；已经待审批的任务仍需单独审批。"};break;
            case "ruleRevoke":
                Fields(request,"target");RequireOperator();var ruleID=ID(request,"target");InvalidateReviews();
                try {await call("POST","/v1/admin/revoke-rule",new{id=ruleID}).ConfigureAwait(false);}
                catch {throw new UserError("规则撤销结果未确认，请刷新规则列表；不会自动重试，已派发任务不会停止。");}
                data=new{recorded=true};break;
            case "updateStatus":
                Fields(request);data=updates.Status();break;
            case "updateCheck":
                Fields(request);data=await updates.Check().ConfigureAwait(false);break;
            case "updateSource":
                Fields(request);
                try{data=await updates.Source().ConfigureAwait(false);}catch(InvalidOperationException e){throw new UserError(e.Message);}break;
            case "updateImport":
                Fields(request,"path","sha256");
                try{data=await updates.Import(Str(request,"path"),Str(request,"sha256")).ConfigureAwait(false);}catch(InvalidOperationException e){throw new UserError(e.Message);}break;
            case "integrationStatus":
                Fields(request);data=LocalIntegrations().Status();break;
            case "integrationPreview":
                Fields(request,"action","component");
                try { data=LocalIntegrations().Preview(Str(request,"action"),Str(request,"component")); }
                catch(InvalidOperationException e) {throw new UserError(e.Message);}break;
            case "integrationApply":
                Fields(request,"ticket");
                try { data=LocalIntegrations().Apply(Str(request,"ticket")); }
                catch(InvalidOperationException e) {throw new UserError(e.Message);}break;
            case "snapshot":
                Fields(request);
                var health=Map(await call("GET","/v1/health",null).ConfigureAwait(false));
                object capability;bool supports=health.TryGetValue("temporary_grants",out capability) && capability is bool && (bool)capability;
                object labelsCapability;bool supportsLabels=health.TryGetValue("session_labels",out labelsCapability) && labelsCapability is bool && (bool)labelsCapability;
                object rulesCapability;bool supportsRules=health.TryGetValue("approval_rules",out rulesCapability)&&rulesCapability is bool&&(bool)rulesCapability;
                var rules=supportsRules?await call("GET","/v1/rules",null).ConfigureAwait(false):new object[0];
                var sessions=await call("GET","/v1/sessions",null).ConfigureAwait(false);
                bool supportsRemoval=ApprovalCapability(health,"session_removal");
                var historySessions=supportsRemoval&&!ReadOnly?await call("GET","/v1/admin/sessions?include_archived=true",null).ConfigureAwait(false):new object[0];
                var jobs=await call("GET","/v1/jobs",null).ConfigureAwait(false);
                var grants=supports?await call("GET","/v1/grants",null).ConfigureAwait(false):new object[0];
                bool supportsBuiltin=ApprovalCapability(health,"builtin_policy");
                var builtin=supportsBuiltin?await call("GET","/v1/policy",null).ConfigureAwait(false):null;
                data=new {bridge_version=Str(health,"version"),desktop_version=Integrations.Version,selected_profile_id=selectedProfile,connection_name=connectionName,connection_type=connectionType,connection_purpose=connectionPurpose,endpoint=connectionEndpoint,mode=Mode,read_only=ReadOnly,sessions=sessions,history_sessions=historySessions,supports_session_removal=supportsRemoval,jobs=jobs,grants=grants,supports_grants=supports,supports_labels=supportsLabels,rules=rules,supports_rules=supportsRules,supports_explain=ApprovalCapability(health,"approval_explain"),supports_batch=ApprovalCapability(health,"batch_approval"),
                    approval_context=DisplayedApprovalContext,builtin_policy=builtin,supports_builtin_policy=supportsBuiltin,supports_displayed_approval=ApprovalCapability(health,"displayed_approval")};break;
            case "labelSession":
                Fields(request,"target","generation","name","environment");RequireOperator();
                var labelID=ID(request,"target");var labelGeneration=ID(request,"generation");
                var labelName=LabelText(request,"name",true);var labelEnvironment=LabelText(request,"environment",false);
                InvalidateReviews();
                try {await call("POST","/v1/admin/session-label",new{id=labelID,generation=labelGeneration,name=labelName,environment=labelEnvironment}).ConfigureAwait(false);}
                catch {throw new UserError("名称保存未确认，请刷新会话列表核对；接入可能已变化。不会自动重复保存。");}
                data=new {recorded=true};break;
            case "output":
                Fields(request,"target","cursor");var target=ID(request,"target");var cursor=Number(request,"cursor");
                if(cursor<0||cursor>1024*1024)throw new UserError("输出游标无效。");
                data=await call("GET","/v1/output?id="+target+"&cursor="+cursor,null).ConfigureAwait(false);break;
            case "prepare":
                Fields(request,"action","target");RequireOperator();
                var action=Str(request,"action");target=ID(request,"target");
                if(action!="activate")throw new UserError("不支持的审批操作。");
                var review=await CaptureActivation(target).ConfigureAwait(false);
                review.Expires=DateTime.UtcNow.AddMinutes(2);
                byte[] bytes=new byte[32];using(var rng=RandomNumberGenerator.Create())rng.GetBytes(bytes);
                var ticket=Convert.ToBase64String(bytes);
                lock(reviewLock){InvalidateReviews();reviews.Add(ticket,review);}
                data=new {ticket=ticket,action=review.Action,session=review.Session,expires=review.Expires.ToString("o")};break;
            case "cancelReview":
                Fields(request);InvalidateReviews();data=new {cancelled=true};break;
            case "confirm":
                Fields(request,"ticket","remember");RequireOperator();ticket=Str(request,"ticket");
                object rememberValue;bool remember=false;
                if(request.TryGetValue("remember",out rememberValue)) {if(!(rememberValue is bool))throw new UserError("记住授权参数无效。");remember=(bool)rememberValue;}
                lock(reviewLock){if(!reviews.TryGetValue(ticket,out review))throw new UserError("确认已失效，请重新核对。");reviews.Remove(ticket);}
                if(review.Action!="activate") {throw new UserError("确认操作不匹配，请重新预览。");}
                if(remember)throw new UserError("激活不能记住授权。");
                if(DateTime.UtcNow>=review.Expires)throw new UserError("确认已过期，请重新核对。");
                var current=await CaptureActivation(review.Target).ConfigureAwait(false);
                if(current.Signature!=review.Signature)throw new UserError("目标或任务已变化，没有批准。请重新核对。");
                // Consume before POST. A lost response must never trigger an automatic retry.
                try {
                    var s=Map(review.Session);
                    await call("POST","/v1/admin/activate",new {id=review.Target,generation=Str(s,"generation")}).ConfigureAwait(false);
                } catch { InvalidateReviews();throw new UserError("决定的结果未确认。请刷新查询原任务；不要自动重复批准或重新提交命令。"); }
                data=new {recorded=true};break;
            case "revoke":
                Fields(request,"target");RequireOperator();target=ID(request,"target");InvalidateReviews();
                try {await call("POST","/v1/admin/revoke-grant",new {id=target}).ConfigureAwait(false);}
                catch {throw new UserError("撤销结果未确认，请刷新授权列表。已派发的任务不会因此停止。");}
                data=new {recorded=true};break;
            default:
                if(operation.StartsWith("localAgent",StringComparison.Ordinal)) { data=await HandleLocalAgent(operation,request).ConfigureAwait(false); break; }
                if(!operation.StartsWith("reverse",StringComparison.Ordinal))throw new UserError("不支持的桌面操作。");
                try {data=await HandleReverse(operation,request).ConfigureAwait(false);}
                catch(ReverseExportError) {throw;}
                catch(InvalidOperationException) {throw new UserError("反向连接操作被拒绝或不可用，请核对连接配置和文件。旧窗口模式连接前须显式重新导出。不会自动重试。");}
                break;
            }
            return new {id=id,ok=true,data=data};
        } catch(DecisionNotSent error) {InvalidateReviews();return new {id=id,ok=false,code="DECISION_NOT_SENT",error=error.Message};}
        catch(ReverseExportError error) { InvalidateReviews();return new {id=id,ok=false,code=error.Code,stage=error.Stage,path=error.ChosenPath,error=error.DisplayMessage()}; }
        catch(UserError error) { InvalidateReviews();return new {id=id,ok=false,error=error.Message}; }
        catch { InvalidateReviews();return new {id=id,ok=false,error="连接或数据读取失败。旧确认已作废；恢复连接后查询原任务，不会重放命令。"}; }
        finally { gate.Release(); }
    }
    void RequireOperator() { if(ReadOnly)throw new UserError("当前上下文没有操作员权限，请在宿主机工作台审批。"); }
    async Task<Review> CaptureActivation(string target) {
        var list=await call("GET","/v1/sessions",null).ConfigureAwait(false) as object[];
        var session=list==null?null:list.Select(Map).FirstOrDefault(x=>Str(x,"id")==target);
        if(session==null || Str(session,"state")!="pending")throw new UserError("会话不可用，请重新接入并核对。");
        var values=new List<object>{"activate",target,Str(session,"id"),Str(session,"generation"),Str(session,"fingerprint"),Str(session,"name"),Str(session,"environment"),Str(session,"state")};
        return new Review{Action="activate",Target=target,Session=session,Signature=json.Serialize(values)};
    }
    public void Dispose() {
        // Serialize disposal with active and queued handlers; never dispose their transport mid-await.
        gate.Wait();
        try {
            lock(reviewLock) {disposed=true;InvalidateReviews();if(integrations!=null)integrations.Invalidate();if(http!=null){http.Dispose();http=null;}}
            // The handler gate is held, but not reviewLock. Stop only processes owned by this native manager.
            if(reverse!=null) {reverse.Dispose();reverse=null;}
            if(bridgeLifetime!=null) {bridgeLifetime.Dispose();bridgeLifetime=null;}
            if(desktopLifetime!=null) {desktopLifetime.Dispose();desktopLifetime=null;}
        }
        finally {gate.Release();}
    }
}

// The unnamed, non-inheritable job belongs only to this desktop. Windows closes
// it even when Task Manager kills the desktop, terminating its verified Bridge.
internal sealed class BridgeProcessLifetime : IDisposable {
    sealed class JobHandle : SafeHandleZeroOrMinusOneIsInvalid {
        public JobHandle():base(true){}
        protected override bool ReleaseHandle(){return CloseHandle(handle);}
    }
    [StructLayout(LayoutKind.Sequential)] struct BasicLimits {
        public long ProcessTime,JobTime;public uint Flags;public UIntPtr MinWorking,MaxWorking;
        public uint ActiveProcesses;public UIntPtr Affinity;public uint Priority,Scheduling;
    }
    [StructLayout(LayoutKind.Sequential)] struct IoCounters {public ulong A,B,C,D,E,F;}
    [StructLayout(LayoutKind.Sequential)] struct ExtendedLimits {
        public BasicLimits Basic;public IoCounters Io;public UIntPtr ProcessMemory,JobMemory,PeakProcess,PeakJob;
    }
    [DllImport("kernel32.dll",SetLastError=true)] static extern JobHandle CreateJobObject(IntPtr attributes,string name);
    [DllImport("kernel32.dll",SetLastError=true)] static extern bool SetInformationJobObject(JobHandle job,int kind,ref ExtendedLimits limits,uint size);
    [DllImport("kernel32.dll",SetLastError=true)] static extern bool AssignProcessToJobObject(JobHandle job,IntPtr process);
    [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
    readonly JobHandle job;readonly Process process;
    internal BridgeProcessLifetime(Process target) {
        process=Process.GetProcessById(target.Id);
        // The caller pins target identity; retain our own handle for exit observation.
        IntPtr handle=process.Handle;
        if(target.HasExited){process.Dispose();throw new InvalidOperationException("Bridge 已退出。");}
        job=CreateJobObject(IntPtr.Zero,null);
        var limits=new ExtendedLimits();limits.Basic.Flags=0x2000;
        if(job.IsInvalid || !SetInformationJobObject(job,9,ref limits,(uint)Marshal.SizeOf(typeof(ExtendedLimits))) || !AssignProcessToJobObject(job,handle)) {
            job.Dispose();process.Dispose();throw new InvalidOperationException("无法绑定 Bridge 生命周期。");
        }
    }
    internal bool WaitForExit(int milliseconds){return process.WaitForExit(milliseconds);}
    public void Dispose(){job.Dispose();process.Dispose();}
}
}
