using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Security.AccessControl;
using System.Security.Cryptography;
using System.Security.Principal;
using System.Text;
using System.Text.RegularExpressions;
using System.Web.Script.Serialization;

namespace XshellDesktop {
/// <summary>Only allowlisted diagnostic text crosses IPC. Never carry an inner exception or its message.</summary>
public sealed class ReverseExportError : InvalidOperationException {
    public readonly string Code, Stage, ChosenPath;
    internal ReverseExportError(string code, string stage = null, string path = null) : base(Describe(code)) { Code = code; Stage = stage; ChosenPath = SafeDisplayPath(path); }
    internal static string SafeDisplayPath(string path) {
        try {
            if (String.IsNullOrWhiteSpace(path) || path.Length > 4096 || path.Any(char.IsControl) || path.Contains("\"") || !Path.IsPathRooted(path) || path.StartsWith(@"\\") || path.IndexOf(':', 2) >= 0) return null;
            return Path.GetFullPath(path);
        } catch { return null; }
    }
    internal static ReverseExportError At(Exception error, string stage, string path = null, string fallback = "EXPORT_UNEXPECTED") {
        // Control.Invoke wraps exceptions from the UI delegate. Unwrap only this known wrapper.
        while (error is System.Reflection.TargetInvocationException && error.InnerException != null) error = error.InnerException;
        var known = error as ReverseExportError;
        string code = known != null ? known.Code : error is UnauthorizedAccessException || error is System.Security.SecurityException ? "EXPORT_ACCESS_DENIED" : error is IOException ? "EXPORT_IO_UNAVAILABLE" : error is System.Threading.ThreadStateException ? "EXPORT_UI_THREAD" : error is ObjectDisposedException || error is System.ComponentModel.InvalidAsynchronousStateException ? "EXPORT_UI_UNAVAILABLE" : fallback;
        return new ReverseExportError(code, stage, path ?? (known == null ? null : known.ChosenPath));
    }
    internal string DisplayMessage() {
        string label = Stage == "selection" ? "选择保存路径" : Stage == "preparation" ? "准备导出预览" : Stage == "confirmation" ? "确认导出" : "反向连接校验";
        return label + "失败（" + Code + "）：" + Message + (ChosenPath == null ? "" : "\n所选路径：" + ChosenPath) + "\n不会自动重试。";
    }
    static string Describe(string code) {
        switch (code) {
        case "EXPORT_PATH_INVALID": return "保存路径无效，请选择本机绝对路径。";
        case "EXPORT_EXTENSION": return "保存文件须使用 .js 扩展名。";
        case "EXPORT_TARGET_EXISTS": return "目标文件或目录已存在，请选择尚不存在的 .js 文件。";
        case "EXPORT_PARENT_MISSING": return "保存目录不存在，请核对所选目录。";
        case "EXPORT_PROTECTED_PATH": return "不能导出到程序包、运行目录或反向连接配置文件。";
        case "EXPORT_REPARSE_POINT": return "路径包含重解析点，已拒绝访问。";
        case "EXPORT_ACCESS_DENIED": return "文件访问或私有 ACL 设置被拒绝，请核对目录权限。";
        case "EXPORT_IO_UNAVAILABLE": return "文件访问未完成，请核对目录、占用和磁盘状态；确认阶段请先检查目标文件。";
        case "EXPORT_SOURCE_MISSING": return "程序包缺少必要脚本或日志助手，请核对程序包完整性。";
        case "EXPORT_SOURCE_INVALID": return "程序包脚本或日志助手大小、编码无效，请核对程序包完整性。";
        case "EXPORT_CONFIG_INVALID": return "原始运行目录的客户端或代理配置缺失、不可用或无效。";
        case "EXPORT_PAIRING_MISMATCH": return "当前连接与原始客户端、代理端点不一致，请核对配对。";
        case "EXPORT_PROFILE_INVALID": return "反向连接配置已删除、不可用或无效，请刷新并核对配置。";
        case "EXPORT_SNAPSHOT_CHANGED": return "配置或程序包内容在预览期间已变化，请重新预览。";
        case "EXPORT_CONFIRMATION_INVALID": return "确认已消耗、失效或过期，请重新预览。";
        case "EXPORT_LEGACY_ACTIVE": return "旧窗口模式隧道仍在运行，请先显式断开后重新导出。";
        case "EXPORT_UI_THREAD": return "文件选择器未在 STA 界面线程运行，请关闭后重新打开工作台。";
        case "EXPORT_UI_UNAVAILABLE": return "文件选择器或工作台窗口已不可用，请重新打开工作台。";
        case "EXPORT_CAPABILITY_UNAVAILABLE": return "无法核对 Bridge 的 source_agents 能力或连接已变化，请检查连接。";
        case "EXPORT_CONTEXT_CHANGED": return "连接上下文已变化，请恢复本机操作员连接后重新预览。";
        case "EXPORT_REQUEST_REJECTED": return "当前上下文或请求无效，请核对本机操作员连接和所选配置。";
        default: return "此阶段发生未分类异常，请提供阶段和诊断码以继续排查。";
        }
    }
}
/// <summary>A started, exclusively owned process. Stop targets only this process and must be bounded; callers verify exit before releasing ownership. Factories must start the supplied command or throw without abandoning a live process.</summary>
public interface IReverseProcess : IDisposable {
    bool HasExited { get; }
    int ExitCode { get; }
    bool WaitForExit(int milliseconds);
    void Stop();
}
/// <summary>Native host reverse SSH and single-file export. No Bridge calls, remote commands, retries or readiness claims.
/// Runtime is a trusted private operator directory, authorized by the native Backend reverse-context and capability checks. PreparedExport is native-only, never an IPC DTO.
/// Contract version and status strings are stable independently of application version. Windows/.NET 4.8, C# 5.</summary>
public sealed class HostReverse : IDisposable {
    public const string ContractVersion = "host-reverse-v1";
    public const string AwaitingAgent = "awaiting_agent", Disconnected = "disconnected", Exited = "exited";
    readonly string catalog, bundle, runtime;
    readonly object gate = new object();
    readonly JavaScriptSerializer json = new JavaScriptSerializer();
    readonly Func<ProcessStartInfo, IReverseProcess> factory;
    readonly Dictionary<string, IReverseProcess> owned = new Dictionary<string, IReverseProcess>();
    readonly Dictionary<string, Profile> ownedProfiles = new Dictionary<string, Profile>();
    bool disposed;
    sealed class Profile {
        public string id, name, sshHost, user, keyPath, knownHostsPath, environment, bindingMode, logEncoding;
        public int sshPort, remotePort;
    }
    /// <summary>Safe UI projection: no local key paths, credentials or process output.</summary>
    public sealed class ProfileStatus {
        public string id, name, ssh_host, user, environment, binding_mode, log_encoding, status;
        public int ssh_port, remote_port;
        public int? exit_code;
    }
    public sealed class ExportPreview {
        public string path, profile, source_id, mode, endpoint, environment, log_encoding, warning;
    }
    /// <summary>Opaque native context; serialize PublicPreview() only. Confirmation consumes it even on failure.</summary>
    public sealed class PreparedExport {
        internal readonly HostReverse owner;
        internal readonly string id, destination, snapshot;
        private readonly ExportPreview preview;
        internal bool consumed;
        internal PreparedExport(HostReverse owner, string id, string destination, string snapshot, ExportPreview preview) { this.owner = owner; this.id = id; this.destination = destination; this.snapshot = snapshot; this.preview = preview; }
        public ExportPreview PublicPreview() { return new ExportPreview { path = preview.path, profile = preview.profile, source_id = preview.source_id, mode = preview.mode, endpoint = preview.endpoint, environment = preview.environment, log_encoding = preview.log_encoding, warning = preview.warning }; }
    }
    sealed class NativeProcess : IReverseProcess {
        readonly Process process;
        bool draining;
        public NativeProcess(ProcessStartInfo start) {
            process = new Process { StartInfo = start };
            try { process.ErrorDataReceived += delegate { }; if (!process.Start()) throw Failure(); }
            catch { process.Dispose(); throw; }
        }
        public bool HasExited { get { return process.HasExited; } }
        public int ExitCode { get { return process.ExitCode; } }
        public bool WaitForExit(int milliseconds) {
            // Connect registers ownership before starting asynchronous stderr draining.
            if (!draining) { process.BeginErrorReadLine(); draining = true; }
            return process.WaitForExit(milliseconds);
        }
        public void Stop() {
            if (!process.HasExited) process.Kill();
            if (!process.WaitForExit(2000)) throw Failure();
        }
        public void Dispose() { process.Dispose(); }
    }
    public HostReverse(string catalogRoot, string bundleRoot, string runtime, Func<ProcessStartInfo, IReverseProcess> optionalFactory = null) {
        try {
            catalog = Path.Combine(Absolute(catalogRoot), "host-reverse.json"); bundle = Absolute(bundleRoot); this.runtime = Absolute(runtime);
            Safe(catalog); Safe(bundle); Safe(this.runtime);
            if (Inside(catalog, bundle) || Inside(catalog, this.runtime)) throw Failure();
            factory = optionalFactory ?? delegate(ProcessStartInfo s) { return new NativeProcess(s); };
        } catch (ReverseExportError) { throw; }
        catch (UnauthorizedAccessException) { throw new ReverseExportError("EXPORT_ACCESS_DENIED"); }
        catch (System.Security.SecurityException) { throw new ReverseExportError("EXPORT_ACCESS_DENIED"); }
        catch { throw Failure(); }
    }
    sealed class LegacyProfileException : InvalidOperationException { public LegacyProfileException() : base("旧窗口模式不能连接；请先显式导出新的逐终端脚本，再连接并逐个激活终端。") {} }
    static InvalidOperationException Failure() { return new InvalidOperationException("Host reverse operation refused or unavailable; no external output disclosed."); }
    void Active() { if (disposed) throw Failure(); }
    static string Absolute(string path) {
        if (String.IsNullOrWhiteSpace(path) || path.Any(char.IsControl) || path.IndexOf('"') >= 0 || !Path.IsPathRooted(path) || path.StartsWith(@"\\") || path.IndexOf(':', 2) >= 0) throw Failure();
        string full = Path.GetFullPath(path);
        if (full.Split(Path.DirectorySeparatorChar).Any(x => x.EndsWith(" ") || x.EndsWith("."))) throw Failure();
        return full;
    }
    static bool Inside(string path, string root) { return String.Equals(path, root, StringComparison.OrdinalIgnoreCase) || path.StartsWith(root.TrimEnd('\\', '/') + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase); }
    internal static void Safe(string path) {
        for (string p = Absolute(path); !String.IsNullOrEmpty(p); p = Path.GetDirectoryName(p)) {
            try { if ((File.GetAttributes(p) & FileAttributes.ReparsePoint) != 0) throw new ReverseExportError("EXPORT_REPARSE_POINT"); }
            catch (FileNotFoundException) { } catch (DirectoryNotFoundException) { }
        }
    }
    static void Existing(string path) { Safe(path); if (!File.Exists(path)) throw Failure(); }
    static string Label(string s, bool required) {
        if (s == null || s.Any(char.IsControl) || Encoding.UTF8.GetByteCount(s) > 200 || (required && String.IsNullOrWhiteSpace(s))) throw Failure(); return s;
    }
    static void Validate(Profile p) {
        if (p == null || !Regex.IsMatch(p.id ?? "", "\\A[0-9a-f]{32}\\z")) throw Failure();
        Label(p.name, true); Label(p.environment, true);
        if (p.logEncoding == null) p.logEncoding = "auto";
        if (!ValidEncoding(p.logEncoding)) throw Failure();
        if (!Regex.IsMatch(p.sshHost ?? "", "\\A[A-Za-z0-9](?:[A-Za-z0-9.-]{0,251}[A-Za-z0-9])?\\z") || p.sshHost.Contains("..") || !Regex.IsMatch(p.user ?? "", "\\A[A-Za-z0-9_][A-Za-z0-9_.-]{0,63}\\z")) throw Failure();
        if (p.sshPort < 1 || p.sshPort > 65535 || p.remotePort < 1024 || p.remotePort > 65535 || (p.bindingMode != "window_auto" && p.bindingMode != "manual_terminal")) throw Failure();
        p.keyPath = Absolute(p.keyPath); p.knownHostsPath = Absolute(p.knownHostsPath);
    }
    internal static bool ValidEncoding(string e) { return e == "auto" || e == "utf-8" || e == "utf-16le" || e == "utf-16be" || e == "gbk" || e == "gb18030"; }
    List<Profile> Load() {
        Active(); Safe(catalog);
        if (!File.Exists(catalog)) return new List<Profile>();
        if (new FileInfo(catalog).Length > 1048576) throw Failure();
        string text = File.ReadAllText(catalog);
        object raw = json.DeserializeObject(text);
        var rows = raw as object[]; if (rows == null) throw Failure();
        foreach (var row in rows) { var map = row as Dictionary<string,object>; if (map == null) throw Failure(); var keys = map.Keys.Where(k => String.Equals(k, "logEncoding", StringComparison.OrdinalIgnoreCase)).ToArray(); if (keys.Length > 1 || (keys.Length == 1 && !(map[keys[0]] is string))) throw Failure(); }
        var result = json.Deserialize<List<Profile>>(text);
        if (result == null) throw Failure();
        foreach (var p in result) Validate(p);
        if (result.Select(p => p.id).Distinct().Count() != result.Count) throw Failure();
        return result;
    }
    static FileSecurity PrivateAcl() {
        var acl = new FileSecurity(); acl.SetAccessRuleProtection(true, false);
        foreach (var sid in new[] { WindowsIdentity.GetCurrent().User, new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null) }) acl.AddAccessRule(new FileSystemAccessRule(sid, FileSystemRights.FullControl, AccessControlType.Allow));
        return acl;
    }
    internal static void WritePrivate(string path, byte[] bytes, bool replace) {
        Safe(path); string parent = Path.GetDirectoryName(path); Safe(parent);
        if (!Directory.Exists(parent)) throw Failure();
        string temp = Path.Combine(parent, ".reverse-" + Guid.NewGuid().ToString("N") + ".tmp");
        try {
            using (var f = new FileStream(temp, FileMode.CreateNew, FileSystemRights.Write, FileShare.None, 4096, FileOptions.WriteThrough, PrivateAcl())) { f.Write(bytes, 0, bytes.Length); f.Flush(true); }
            Safe(path);
            if (replace && File.Exists(path)) File.Replace(temp, path, null); else File.Move(temp, path);
        } finally { if (File.Exists(temp)) File.Delete(temp); }
    }
    void Save(List<Profile> rows) {
        string dir = Path.GetDirectoryName(catalog); Safe(dir);
        if (!Directory.Exists(dir)) {
            var acl = new DirectorySecurity(); acl.SetAccessRuleProtection(true, false);
            foreach (var sid in new[] { WindowsIdentity.GetCurrent().User, new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null) }) acl.AddAccessRule(new FileSystemAccessRule(sid, FileSystemRights.FullControl, InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit, PropagationFlags.None, AccessControlType.Allow));
            Directory.CreateDirectory(dir, acl);
        }
        WritePrivate(catalog, Encoding.UTF8.GetBytes(json.Serialize(rows)), true);
    }
    Profile Find(string id) { var p = Load().FirstOrDefault(x => x.id == id); if (p == null) throw Failure(); return p; }
    ProfileStatus Status(Profile p) {
        var s = new ProfileStatus { id = p.id, name = p.name, ssh_host = p.sshHost, ssh_port = p.sshPort, user = p.user, remote_port = p.remotePort, environment = p.environment, binding_mode = p.bindingMode, log_encoding = p.logEncoding, status = Disconnected };
        IReverseProcess process;
        if (owned.TryGetValue(p.id, out process)) { s.status = process.HasExited ? Exited : AwaitingAgent; if (s.status == Exited) s.exit_code = process.ExitCode; }
        return s;
    }
    /// <summary>Observes only processes owned by this manager, independently of the catalog. Observation failures propagate so callers can fail closed.</summary>
    public bool HasActiveOwnedProcesses {
        get { lock (gate) { try { return owned.Values.Any(p => !p.HasExited); } catch { throw Failure(); } } }
    }
    public List<ProfileStatus> List() {
        lock (gate) { try {
            var rows = Load();
            // Running status must describe the captured connection, not a subsequently edited row.
            var result = rows.Select(p => { var item = Status(ownedProfiles.ContainsKey(p.id) ? ownedProfiles[p.id] : p); item.log_encoding = p.logEncoding; return item; }).ToList();
            var catalogIDs = new HashSet<string>(rows.Select(p => p.id));
            foreach (var entry in ownedProfiles) if (!catalogIDs.Contains(entry.Key)) result.Add(Status(entry.Value));
            return result;
        } catch { throw Failure(); } }
    }
    public ProfileStatus Get(string id) { lock (gate) { try { return Status(Find(id)); } catch { throw Failure(); } } }
    public string Add(string name, string sshHost, int sshPort, string user, string keyPath, string knownHostsPath, int remotePort, string environment, string bindingMode, string logEncoding = "auto") {
        lock (gate) { try {
            var rows = Load(); var p = new Profile { id = Guid.NewGuid().ToString("N"), name = name, sshHost = sshHost, sshPort = sshPort, user = user, keyPath = keyPath, knownHostsPath = knownHostsPath, remotePort = remotePort, environment = String.IsNullOrWhiteSpace(environment) ? name : environment, bindingMode = bindingMode, logEncoding = logEncoding };
            if (bindingMode != "manual_terminal") throw Failure();
            Validate(p); Existing(p.keyPath); Existing(p.knownHostsPath); rows.Add(p); Save(rows); return p.id;
        } catch { throw Failure(); } }
    }
    public void SetLogEncoding(string id, string value) { lock (gate) { try {
        if (!ValidEncoding(value)) throw Failure(); var rows = Load(); var p = rows.FirstOrDefault(x => x.id == id); if (p == null) throw Failure(); p.logEncoding = value; Save(rows);
    } catch { throw Failure(); } } }
    public void Remove(string id) { lock (gate) { try { Find(id); IReverseProcess p; if (owned.TryGetValue(id, out p) && !p.HasExited) throw Failure(); Release(id); Save(Load().Where(x => x.id != id).ToList()); } catch { throw Failure(); } } }
    public static string Quote(string value) {
        if (value == null || value.Any(char.IsControl)) throw Failure();
        var b = new StringBuilder("\""); int slashes = 0;
        foreach (char c in value) { if (c == '\\') { slashes++; continue; } b.Append('\\', c == '"' ? slashes * 2 + 1 : slashes); b.Append(c); slashes = 0; }
        return b.Append('\\', slashes * 2).Append('"').ToString();
    }
    sealed class Config { public string endpoint, token; public int port; }
    byte[] ConfigBytes(string name) {
        try {
            string path = Path.Combine(runtime, name); Existing(path); if (new FileInfo(path).Length > 65536) throw Failure();
            return File.ReadAllBytes(path);
        } catch (Exception error) { throw ReverseExportError.At(error, null, null, "EXPORT_CONFIG_INVALID"); }
    }
    Config ReadConfig(string name, bool allowIPv6 = false) {
        try {
            Dictionary<string,object> d;
            // Preserve File.ReadAllText BOM detection for existing runtime configurations.
            using (var reader = new StreamReader(new MemoryStream(ConfigBytes(name)), Encoding.UTF8, true))
                d = json.Deserialize<Dictionary<string, object>>(reader.ReadToEnd());
            if (d == null || !d.ContainsKey("endpoint") || !d.ContainsKey("token")) throw Failure();
            string endpoint = d["endpoint"] as string, token = d["token"] as string;
            string pattern = allowIPv6 ? "\\Ahttp://(?:127\\.0\\.0\\.1|\\[::1\\])(?::[0-9]{1,5})?/?\\z" : "\\Ahttp://127\\.0\\.0\\.1:[0-9]{1,5}/?\\z";
            if (endpoint == null || !Regex.IsMatch(endpoint, pattern, allowIPv6 ? RegexOptions.IgnoreCase : RegexOptions.None) || token == null || token.Length < 32 || token.Any(c => char.IsControl(c) || char.IsWhiteSpace(c))) throw Failure();
            Uri uri; if (!Uri.TryCreate(endpoint, UriKind.Absolute, out uri) || uri.Port < 1 || uri.Port > 65535) throw Failure();
            return new Config { endpoint = "http://" + (endpoint.IndexOf("[::1]", StringComparison.Ordinal) >= 0 ? "[::1]" : "127.0.0.1") + ":" + uri.Port, token = token, port = uri.Port };
        } catch (Exception error) { throw ReverseExportError.At(error, null, null, "EXPORT_CONFIG_INVALID"); }
    }
    public ProfileStatus Connect(string id) {
        lock (gate) { bool acquired = false; try {
            var p = Find(id);
            if (p.bindingMode != "manual_terminal") throw new LegacyProfileException();
            IReverseProcess old;
            if (owned.TryGetValue(id, out old) && !old.HasExited) return Status(p);
            foreach (var entry in owned) {
                if (entry.Key == id || entry.Value.HasExited) continue;
                var other = ownedProfiles[entry.Key];
                if (String.Equals(other.sshHost, p.sshHost, StringComparison.OrdinalIgnoreCase) && other.sshPort == p.sshPort && String.Equals(other.user, p.user, StringComparison.Ordinal) && other.remotePort == p.remotePort) throw Failure();
            }
            Release(id); Existing(p.keyPath); Existing(p.knownHostsPath); var client = ReadConfig("client.json");
            var args = new List<string> { "-F", "NUL", "-N", "-T" };
            foreach (string option in new[] { "BatchMode=yes", "IdentitiesOnly=yes", "StrictHostKeyChecking=yes", "GlobalKnownHostsFile=NUL", "UserKnownHostsFile=\"" + p.knownHostsPath.Replace('\\', '/') + "\"", "ExitOnForwardFailure=yes", "ServerAliveInterval=15", "ServerAliveCountMax=3", "ConnectTimeout=10" }) { args.Add("-o"); args.Add(option); }
            args.AddRange(new[] { "-i", p.keyPath, "-p", p.sshPort.ToString(System.Globalization.CultureInfo.InvariantCulture), "-R", "127.0.0.1:" + p.remotePort + ":127.0.0.1:" + client.port, p.user + "@" + p.sshHost });
            string exe = Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows), "System32", "OpenSSH", "ssh.exe"); Safe(exe);
            var start = new ProcessStartInfo(exe, String.Join(" ", args.Select(Quote))) { UseShellExecute = false, CreateNoWindow = true, RedirectStandardError = true, WorkingDirectory = Environment.GetFolderPath(Environment.SpecialFolder.Windows) };
            var process = factory(start); if (process == null) throw Failure(); owned.Add(id, process); acquired = true;
            ownedProfiles.Add(id, p);
            process.WaitForExit(250); return Status(p);
        } catch (LegacyProfileException) { throw; } catch { if (acquired) { try { Release(id); } catch { } } throw Failure(); } }
    }
    void Release(string id) {
        IReverseProcess p; if (!owned.TryGetValue(id, out p)) return;
        if (!p.HasExited) p.Stop();
        if (!p.HasExited) throw Failure();
        // Do not discard ownership on stop, observation, or disposal failure.
        p.Dispose();
        owned.Remove(id);
        ownedProfiles.Remove(id);
    }
    public void Disconnect(string id) { lock (gate) { try { Active(); if (id == null || !owned.ContainsKey(id)) Find(id); Release(id); } catch { throw Failure(); } } }
    internal static string Hash(byte[] b) { using (var h = SHA256.Create()) return BitConverter.ToString(h.ComputeHash(b)).Replace("-", "").ToLowerInvariant(); }
    byte[] Source(string name) {
        try {
            string path = Path.Combine(bundle, name); Safe(path);
            if (!File.Exists(path)) throw new ReverseExportError("EXPORT_SOURCE_MISSING");
            if (new FileInfo(path).Length > 16777216) throw new ReverseExportError("EXPORT_SOURCE_INVALID");
            byte[] bytes = File.ReadAllBytes(path);
            if (name == "XshLogCapture.exe" && (bytes.Length < 1024 || bytes.Length > 262144)) throw new ReverseExportError("EXPORT_SOURCE_INVALID");
            return bytes;
        } catch (Exception error) { throw ReverseExportError.At(error, null, null, "EXPORT_SOURCE_INVALID"); }
    }
    Profile ExportProfile(string id) {
        try { return Find(id); }
        catch (Exception error) { throw ReverseExportError.At(error, null, null, "EXPORT_PROFILE_INVALID"); }
    }
    void Destination(string path) {
        Safe(path);
        if (!String.Equals(Path.GetExtension(path), ".js", StringComparison.OrdinalIgnoreCase)) throw new ReverseExportError("EXPORT_EXTENSION");
        if (Inside(path, runtime) || Inside(path, bundle) || String.Equals(path, catalog, StringComparison.OrdinalIgnoreCase)) throw new ReverseExportError("EXPORT_PROTECTED_PATH");
        if (File.Exists(path) || Directory.Exists(path)) throw new ReverseExportError("EXPORT_TARGET_EXISTS");
        if (!Directory.Exists(Path.GetDirectoryName(path))) throw new ReverseExportError("EXPORT_PARENT_MISSING");
    }
    string Snapshot(Profile p) {
        // Reflection-backed object serialization can change field order when
        // metadata caches are rebuilt. Hash a fixed sequence of all profile
        // values so unchanged settings retain the same review identity.
        var values = new List<string> { json.Serialize(new object[] { p.id, p.name, p.sshHost, p.sshPort, p.user, p.keyPath, p.knownHostsPath, p.remotePort, p.environment, p.bindingMode, p.logEncoding }) };
        foreach (string name in new[] { "agent.json", "client.json" }) values.Add(Hash(ConfigBytes(name)));
        foreach (string name in new[] { "xshell_agent.js", "remote_bootstrap.js", "log_parser.js", "XshLogCapture.exe" }) values.Add(Hash(Source(name)));
        return Hash(Encoding.UTF8.GetBytes(json.Serialize(values)));
    }
    public PreparedExport PrepareExport(string id, string destinationJS) {
        lock (gate) { try {
            var p = ExportProfile(id); string destination;
            try { destination = Absolute(destinationJS); } catch { throw new ReverseExportError("EXPORT_PATH_INVALID"); }
            Destination(destination);
            if (p.bindingMode == "window_auto") {
                IReverseProcess running;
                if (owned.TryGetValue(id, out running) && !running.HasExited) throw new ReverseExportError("EXPORT_LEGACY_ACTIVE");
                var rows = Load(); p = rows.First(x => x.id == id); p.bindingMode = "manual_terminal"; Save(rows);
            }
            string snapshot = Snapshot(p); var client = ReadConfig("client.json"); var agent = ReadConfig("agent.json"); if (client.endpoint != agent.endpoint) throw new ReverseExportError("EXPORT_PAIRING_MISMATCH");
            var preview = new ExportPreview { path = destination, profile = p.name, source_id = p.id, mode = p.bindingMode, endpoint = "http://127.0.0.1:" + p.remotePort, environment = p.environment, log_encoding = p.logEncoding, warning = "Contains agent credential; transfer privately and protect this file." };
            if (snapshot != Snapshot(ExportProfile(id))) throw new ReverseExportError("EXPORT_SNAPSHOT_CHANGED");
            return new PreparedExport(this, id, destination, snapshot, preview);
        } catch (Exception error) { throw ReverseExportError.At(error, "preparation", destinationJS); } }
    }
    static string Ascii(string text) {
        var b = new StringBuilder(); foreach (char c in text) { if (c > 126 || c < 32 || c == '<' || c == '>') b.Append("\\u").Append(((int)c).ToString("x4")); else b.Append(c); } return b.ToString();
    }
    // Shared by local installation and remote export; no credential is returned through IPC.
    internal string PairingEndpoint() {
        var client = ReadConfig("client.json", true); var agent = ReadConfig("agent.json", true);
        if (client.endpoint != agent.endpoint) throw new ReverseExportError("EXPORT_PAIRING_MISMATCH");
        return agent.endpoint;
    }
    internal string LocalSnapshot() {
        var values = new List<string> { PairingEndpoint() };
        foreach (string name in new[] { "agent.json", "client.json" }) values.Add(Hash(ConfigBytes(name)));
        foreach (string name in new[] { "xshell_agent.js", "remote_bootstrap.js", "log_parser.js", "XshLogCapture.exe" }) values.Add(Hash(Source(name)));
        return Hash(Encoding.UTF8.GetBytes(json.Serialize(values)));
    }
    internal IEnumerable<string> LocalInputs() {
        foreach (string name in new[] { "agent.json", "client.json" }) yield return Path.Combine(runtime, name);
        foreach (string name in new[] { "xshell_agent.js", "remote_bootstrap.js", "log_parser.js", "XshLogCapture.exe" }) yield return Path.Combine(bundle, name);
    }
    internal string LocalOwnershipMac(string value) {
        using (var h = new HMACSHA256(Encoding.UTF8.GetBytes(ReadConfig("agent.json", true).token)))
            return BitConverter.ToString(h.ComputeHash(Encoding.UTF8.GetBytes("xshell-bridge-local-owner-v1:" + value))).Replace("-", "").ToLowerInvariant();
    }
    internal byte[] BuildAgent(string sourceID, string endpoint, string mode, string environment, string encoding, bool local = false) {
        var agent = ReadConfig("agent.json", local);
        string scoped;
        using (var h = new HMACSHA256(Encoding.UTF8.GetBytes(agent.token))) scoped = "xsh-agent-v1." + sourceID + "." + BitConverter.ToString(h.ComputeHash(Encoding.UTF8.GetBytes("xshell-bridge-source-v1:" + sourceID))).Replace("-", "").ToLowerInvariant();
        byte[] bootstrap = Source("remote_bootstrap.js"), script = Source("xshell_agent.js"), parser = Source("log_parser.js"), helper = Source("XshLogCapture.exe");
        var data = new { helper_base64 = Convert.ToBase64String(helper), helper_sha256 = Hash(helper), helper_size = helper.Length, config = new { endpoint = endpoint, token = scoped, source_id = sourceID, binding_mode = mode, environment = environment, log_encoding = encoding } };
        var utf8 = new UTF8Encoding(false, true);
        try { return utf8.GetBytes("var XSH_REMOTE_BUNDLE = " + Ascii(json.Serialize(data)) + ";\n" + utf8.GetString(bootstrap) + "\n" + utf8.GetString(parser) + "\n" + utf8.GetString(script)); }
        catch (DecoderFallbackException) { throw new ReverseExportError("EXPORT_SOURCE_INVALID"); }
    }
    public ExportPreview ConfirmExport(PreparedExport plan) {
        lock (gate) { try {
            Active(); if (plan == null || plan.owner != this || plan.consumed) throw new ReverseExportError("EXPORT_CONFIRMATION_INVALID"); plan.consumed = true;
            var p = ExportProfile(plan.id); Destination(plan.destination); if (Snapshot(p) != plan.snapshot) throw new ReverseExportError("EXPORT_SNAPSHOT_CHANGED");
            var agent = ReadConfig("agent.json"); if (agent.endpoint != ReadConfig("client.json").endpoint) throw new ReverseExportError("EXPORT_PAIRING_MISMATCH");
            byte[] output = BuildAgent(p.id, "http://127.0.0.1:" + p.remotePort, p.bindingMode, p.environment, p.logEncoding);
            if (Snapshot(ExportProfile(plan.id)) != plan.snapshot) throw new ReverseExportError("EXPORT_SNAPSHOT_CHANGED"); Destination(plan.destination);
            WritePrivate(plan.destination, output, false); return plan.PublicPreview();
        } catch (Exception error) { throw ReverseExportError.At(error, "confirmation", plan != null && plan.owner == this ? plan.destination : null); } }
    }
    public void Dispose() {
        lock (gate) {
            if (disposed && owned.Count == 0) return;
            disposed = true;
            foreach (string id in owned.Keys.ToArray()) { try { Release(id); } catch { } }
            // New work remains disabled, but callers can retry cleanup with Dispose.
            if (owned.Count != 0) throw Failure();
        }
    }
}
}
