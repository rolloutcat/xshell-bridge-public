using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Text.RegularExpressions;
using System.Threading.Tasks;

namespace XshellDesktop {
public sealed partial class Backend {
    HostReverse reverse;
    Func<string,string> reversePathSelector;

    // Native test seam only: never reachable through IPC. The supplied manager owns only fake processes in tests.
    internal Backend(Func<string,string,object,Task<object>> transport, bool readOnly, string mode, string runtime, string endpoint, HostReverse manager, Func<string,string> selector)
        : this(transport, readOnly, mode) {
        runtimeDirectory = runtime;
        originalArgs = runtime == null ? null : new[] { "--mode", mode, "--dir", runtime };
        connectionEndpoint = endpoint;
        reverse = manager;
        reversePathSelector = selector;
    }
    internal void SetReversePathSelector(Func<string,string> selector) { reversePathSelector = selector; }

    string ReverseUnavailableReason() {
        if (ReadOnly) return "观察连接不能管理宿主反向 SSH。";
        if (selectedProfile != null) return "请先恢复本机原始操作员连接。";
        if (Mode != "Local") return "宿主反向 SSH 仅支持 Local 模式。";
        if (runtimeDirectory == null || originalArgs == null) return "缺少本机原始操作员运行上下文。";
        try {
            var context = OriginalUpdateContext();
            if (!String.Equals(context.Runtime, runtimeDirectory, StringComparison.OrdinalIgnoreCase)) return "原始运行上下文不匹配。";
        } catch { return "运行上下文无效，请从宿主机 Start.cmd 重新打开工作台。"; }
        return null;
    }
    void RequireReverseContext() {
        string reason = ReverseUnavailableReason();
        if (reason != null) throw new UserError(reason);
    }
    HostReverse ReverseManager() {
        RequireReverseContext();
        if (reverse == null) reverse = new HostReverse(
            Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "XshellBridge-host-reverse"),
            AppDomain.CurrentDomain.BaseDirectory, runtimeDirectory);
        return reverse;
    }
    // Does not instantiate a manager or read the catalog or runtime credentials. Fail closed on process observation errors.
    public bool HasReverseTunnels {
        get {
            if (reverse == null) return false;
            try { return reverse.HasActiveOwnedProcesses; }
            catch { return true; }
        }
    }
    void GuardReverseContextChange() {
        if (HasReverseTunnels) throw new UserError("存在本桌面拥有的活动反向 SSH 隧道；请先在反向连接列表断开，再切换连接或更新。不会自动停止隧道或改变路由。");
    }
    static string ReverseID(Dictionary<string,object> request) {
        string value = TextField(request, "profile_id");
        if (!Regex.IsMatch(value, "\\A[0-9a-f]{32}\\z")) throw new UserError("反向连接标识无效。");
        return value;
    }
    // This is intentionally separate from listing. Only explicit connect/export may read pairing files.
    void NativeReverseRead() {
        RequireReverseContext();
        ConnectionProfiles.Client client, agent;
        try {
            HostReverse.Safe(Path.Combine(runtimeDirectory, "client.json"));
            HostReverse.Safe(Path.Combine(runtimeDirectory, "agent.json"));
            client = ConnectionProfiles.ReadClient(Path.Combine(runtimeDirectory, "client.json"));
            agent = ConnectionProfiles.ReadClient(Path.Combine(runtimeDirectory, "agent.json"));
        } catch (Exception error) { throw ReverseExportError.At(error, null, null, "EXPORT_CONFIG_INVALID"); }
        Uri current;
        if (!Uri.TryCreate(connectionEndpoint, UriKind.Absolute, out current) ||
            client.Endpoint.AbsoluteUri != agent.Endpoint.AbsoluteUri ||
            client.Endpoint.AbsoluteUri != current.AbsoluteUri)
            throw new ReverseExportError("EXPORT_PAIRING_MISMATCH");
    }
    async Task RequireReverseCapability() {
        RequireReverseContext();
        string before = ConnectionContext();
        Dictionary<string,object> health;
        try { health = Map(await call("GET", "/v1/health", null).ConfigureAwait(false)); }
        catch { throw new ReverseExportError("EXPORT_CAPABILITY_UNAVAILABLE"); }
        object capability;
        if (before != ConnectionContext() || !health.TryGetValue("source_agents", out capability) || !(capability is bool) || !(bool)capability)
            throw new ReverseExportError("EXPORT_CAPABILITY_UNAVAILABLE");
        NativeReverseRead();
    }
    string ReverseSuggestedPath() {
        string desktop = Environment.GetFolderPath(Environment.SpecialFolder.DesktopDirectory);
        if (String.IsNullOrEmpty(desktop)) desktop = Environment.GetFolderPath(Environment.SpecialFolder.UserProfile);
        return Path.Combine(desktop, "xshell_agent.js");
    }
    string ReverseSource(Dictionary<string,object> session) {
        // Fingerprint is an untrusted JSON string, not a host/name label or an IPC object.
        string fingerprint = Str(session, "fingerprint");
        if (fingerprint.Length == 0 || fingerprint.Length > 16384) return null;
        try {
            var value = json.DeserializeObject(fingerprint) as Dictionary<string,object>;
            if (value == null) return null;
            // Match the Bridge's exact field contract; lowercase local claims are not remote telemetry.
            string source = Str(value, "SourceID");
            return Regex.IsMatch(source, "\\A[0-9a-f]{32}\\z") ? source : null;
        } catch { return null; }
    }
    async Task<object> ReverseList() {
        string reason = ReverseUnavailableReason();
        if (reason != null) return new { available = false, reason = reason, profiles = new object[0], suggested_export_path = (string)null };
        var native = ReverseManager().List();
        object[] sessions = null;
        try { sessions = await call("GET", "/v1/sessions", null).ConfigureAwait(false) as object[]; }
        catch { /* Optional telemetry must not turn a live process into a readiness claim. */ }
        DateTime now = DateTime.UtcNow;
        var grouped = new Dictionary<string,List<Dictionary<string,object>>>();
        if (sessions != null) foreach (object raw in sessions) {
            var session = raw as Dictionary<string,object>;
            if (session == null) continue;
            string source = ReverseSource(session);
            if (source == null) continue;
            List<Dictionary<string,object>> group;
            if (!grouped.TryGetValue(source, out group)) grouped.Add(source, group = new List<Dictionary<string,object>>());
            group.Add(session);
        }
        var result = new List<object>();
        foreach (var profile in native) {
            int count = 0; bool detached = false; DateTime? last = null;
            List<Dictionary<string,object>> group;
            if (grouped.TryGetValue(profile.id, out group)) foreach (var session in group) {
                DateTime seen;
                if (!DateTime.TryParse(Str(session, "last_seen"), CultureInfo.InvariantCulture, DateTimeStyles.RoundtripKind, out seen)) continue;
                seen = seen.ToUniversalTime();
                double age = (now - seen).TotalSeconds;
                if (age < 0) continue;
                if (!last.HasValue || seen > last.Value) last = seen;
                if (age > 15) continue;
                string state = Str(session, "state");
                if (state == "active" || state == "pending") count++;
                else if (state == "detached") detached = true;
            }
            bool alive = profile.status == HostReverse.AwaitingAgent;
            string agentStatus = !alive || sessions == null ? "offline" : count > 0 ? "online" : detached ? "detached" : "offline";
            result.Add(new {
                id = profile.id, name = profile.name, ssh_host = profile.ssh_host, user = profile.user,
                environment = profile.environment, binding_mode = profile.binding_mode, log_encoding = profile.log_encoding, status = profile.status,
                ssh_port = profile.ssh_port, remote_port = profile.remote_port, exit_code = profile.exit_code,
                agent_count = sessions == null ? 0 : count, last_seen = last.HasValue ? last.Value.ToString("o") : null,
                agent_status = agentStatus
            });
        }
        return new { available = true, reason = "", profiles = result.ToArray(), suggested_export_path = ReverseSuggestedPath() };
    }
    async Task<object> HandleReverse(string operation, Dictionary<string,object> request) {
        string stage = operation == "reverseChoosePath" ? "selection" : operation == "reverseExportPrepare" ? "preparation" : operation == "reverseExportConfirm" ? "confirmation" : null;
        if (stage == null) return await HandleReverseCore(operation, request).ConfigureAwait(false);
        try { return await HandleReverseCore(operation, request).ConfigureAwait(false); }
        catch (Exception error) {
            object raw; string path = operation == "reverseExportPrepare" && request.TryGetValue("path", out raw) ? raw as string : null;
            throw ReverseExportError.At(error, stage, path, error is UserError ? "EXPORT_REQUEST_REJECTED" : "EXPORT_UNEXPECTED");
        }
    }
    async Task<object> HandleReverseCore(string operation, Dictionary<string,object> request) {
        switch (operation) {
        case "reverseList":
            Fields(request); return await ReverseList().ConfigureAwait(false);
        case "reverseAdd": {
            Fields(request, "name", "ssh_host", "ssh_port", "user", "key_path", "known_hosts_path", "remote_port", "environment", "binding_mode", "log_encoding");
            RequireReverseContext();
            // Read/validate every field before creating the lazy manager or writing its catalog.
            string name = TextField(request, "name"), host = TextField(request, "ssh_host"), user = TextField(request, "user");
            string key = TextField(request, "key_path"), known = TextField(request, "known_hosts_path");
            string environment = TextField(request, "environment"), binding = TextField(request, "binding_mode"), encoding = request.ContainsKey("log_encoding") ? TextField(request, "log_encoding") : "auto";
            int port = Number(request, "ssh_port"), remotePort = Number(request, "remote_port");
            InvalidateReviews();
            return new { profile_id = ReverseManager().Add(name, host, port, user, key, known, remotePort, environment, binding, encoding) };
        }
        case "reverseEncodingSave": {
            Fields(request, "profile_id", "log_encoding"); RequireReverseContext();
            string id = ReverseID(request), encoding = TextField(request, "log_encoding");
            InvalidateReviews(); ReverseManager().SetLogEncoding(id, encoding); return new { saved = true };
        }
        case "reverseConnect": {
            Fields(request, "profile_id"); RequireReverseContext(); string id = ReverseID(request);
            await RequireReverseCapability().ConfigureAwait(false);
            InvalidateReviews(); return ReverseManager().Connect(id);
        }
        case "reverseDisconnect": {
            Fields(request, "profile_id"); RequireReverseContext(); string id = ReverseID(request);
            InvalidateReviews(); ReverseManager().Disconnect(id); return new { disconnected = true };
        }
        case "reverseDelete": {
            Fields(request, "profile_id"); RequireReverseContext(); string id = ReverseID(request);
            InvalidateReviews(); ReverseManager().Remove(id); return new { deleted = true };
        }
        case "reverseExportPrepare": {
            Fields(request, "profile_id", "path"); RequireReverseContext();
            string id = ReverseID(request), path = TextField(request, "path");
            InvalidateReviews(); await RequireReverseCapability().ConfigureAwait(false);
            var plan = ReverseManager().PrepareExport(id, path);
            var review = new Review { Action = "reverseExport", Context = ConnectionContext(), ReverseExportPlan = plan };
            string ticket = SaveReview(review);
            return new { ticket = ticket, expires = review.Expires.ToString("o"), preview = plan.PublicPreview() };
        }
        case "reverseExportConfirm": {
            Fields(request, "ticket");
            // Consume first, including when a context/capability/digest/destination check fails.
            Review review;
            try { review = TakeReview(TextField(request, "ticket"), "reverseExport"); }
            catch (UserError) { throw new ReverseExportError("EXPORT_CONFIRMATION_INVALID"); }
            try {
                RequireReverseContext();
                if (review.Context != ConnectionContext()) throw new ReverseExportError("EXPORT_CONTEXT_CHANGED");
                await RequireReverseCapability().ConfigureAwait(false);
                if (review.Context != ConnectionContext()) throw new ReverseExportError("EXPORT_CONTEXT_CHANGED");
                var preview = ReverseManager().ConfirmExport(review.ReverseExportPlan);
                return new { exported = true, path = preview.path, preview = preview };
            } catch (Exception error) { throw ReverseExportError.At(error, "confirmation", review.ReverseExportPlan.PublicPreview().path, error is UserError ? "EXPORT_REQUEST_REJECTED" : "EXPORT_UNEXPECTED"); }
        }
        case "reverseChoosePath": {
            Fields(request, "kind"); RequireReverseContext();
            string kind = TextField(request, "kind");
            if (kind != "key" && kind != "known_hosts" && kind != "export") throw new UserError("文件选择类型无效。");
            if (reversePathSelector == null) throw new ReverseExportError("EXPORT_UI_UNAVAILABLE");
            string path = reversePathSelector(kind);
            return new { available = true, cancelled = String.IsNullOrEmpty(path), path = String.IsNullOrEmpty(path) ? null : path };
        }
        default: throw new UserError("不支持的桌面操作。");
        }
    }
}
}
