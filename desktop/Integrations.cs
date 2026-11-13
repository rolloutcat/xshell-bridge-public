using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Diagnostics;
using System.Threading;
using System.Security.Cryptography;
using System.Text;
using System.Web.Script.Serialization;

namespace XshellDesktop {
// Local client-machine capabilities, deliberately independent of Bridge operator authority.
// External mutations require a fresh preview ticket and verified postconditions.
public sealed class Integrations {
    public static readonly string Version = Updates.ReadVersion();
    readonly string bundle, managed, home, config;
    readonly Func<DateTime> clock;
    readonly Func<string[], ExternalResult> runner;
    readonly CodexLocator locator;
    readonly Func<string,string> readEnvironment;
    readonly Action<string,string> writeEnvironment;
    CodexLocator.Result detection;
    public sealed class ExternalResult { public int ExitCode; public string Output; public bool TimedOut; }
    ExternalResult RealRunner(string[] args) {
        try {
            detection = locator.Locate();
            string exe = detection.path;
            if (exe == null) return new ExternalResult { ExitCode = -1 };
            using (var p = new Process()) {
                p.StartInfo = new ProcessStartInfo(exe, String.Join(" ", args.Select(Quote))) { UseShellExecute = false, CreateNoWindow = true, WindowStyle = ProcessWindowStyle.Hidden, RedirectStandardOutput = true, RedirectStandardError = true };
                p.Start();
                var output = new StringBuilder(); int total = 0, overflow = 0;
                ThreadStart readOut = delegate { try { char[] buf = new char[4096]; int n; while ((n = p.StandardOutput.Read(buf, 0, buf.Length)) > 0) { if (Interlocked.Add(ref total, n) <= 524288) output.Append(buf, 0, n); else Interlocked.Exchange(ref overflow, 1); } } catch { Interlocked.Exchange(ref overflow, 1); } };
                ThreadStart readErr = delegate { try { char[] buf = new char[4096]; int n; while ((n = p.StandardError.Read(buf, 0, buf.Length)) > 0) if (Interlocked.Add(ref total, n) > 524288) Interlocked.Exchange(ref overflow, 1); } catch { Interlocked.Exchange(ref overflow, 1); } };
                var a = new Thread(readOut) { IsBackground = true }; var b = new Thread(readErr) { IsBackground = true }; a.Start(); b.Start();
                bool done = p.WaitForExit(30000);
                if (!done) { try { p.Kill(); } catch { } }
                bool joined = a.Join(1000) & b.Join(1000);
                if (!done || !joined || overflow != 0) return new ExternalResult { ExitCode = -1, TimedOut = !done || !joined };
                return new ExternalResult { ExitCode = p.ExitCode, Output = output.ToString() };
            }
        } catch { return new ExternalResult { ExitCode = -1 }; }
    }
    ExternalResult Run(params string[] args) {
        ExternalResult r;
        try { r = runner(args); } catch { throw Error("Codex 本地配置操作失败或不可用；未显示外部输出。"); }
        if (r != null && r.TimedOut) throw Error("Codex 操作超时；外部状态需要重新检查。");
        if (r == null || r.ExitCode != 0 || (r.Output != null && r.Output.Length > 524288)) throw Error("Codex 本地配置操作失败或不可用；未显示外部输出。");
        return r;
    }
    object Query(params string[] args) { try { return json.DeserializeObject(Run(args).Output); } catch { throw Error("Codex 状态不可用。"); } }
    static Dictionary<string, object> Obj(object v) { var d = v as Dictionary<string, object>; if (d == null) throw Error("Codex JSON schema unavailable"); return d; }
    static object Get(Dictionary<string, object> d, string k) { object v; if (!d.TryGetValue(k, out v)) throw Error("Codex JSON schema unavailable"); return v; }
    static string Text(object v) { if (!(v is string)) throw Error("Codex JSON schema unavailable"); return (string)v; }
    static bool Flag(object v) { if (!(v is bool)) throw Error("Codex JSON schema unavailable"); return (bool)v; }
    static object[] ArrayValue(object v) { var a = v as object[]; if (a == null) throw Error("Codex JSON schema unavailable"); return a; }
    sealed class ExternalState { public bool Present, Enabled; public string Command, Url, TokenEnv; public string[] Args; public bool EmptyEnv; }
    static bool EmptyField(Dictionary<string, object> d, string key) {
        object value;
        if (!d.TryGetValue(key, out value) || value == null) return true;
        var map = value as Dictionary<string, object>;
        if (map != null) return map.Count == 0;
        var array = value as object[];
        return array != null && array.Length == 0;
    }
    static string NormalPath(string value) {
        if (String.IsNullOrEmpty(value)) return "";
        if (value.StartsWith(@"\\?\UNC\", StringComparison.OrdinalIgnoreCase)) value = @"\\" + value.Substring(8);
        else if (value.StartsWith(@"\\?\", StringComparison.Ordinal)) value = value.Substring(4);
        return Absolute(value);
    }
    static bool SamePath(string a, string b) { return String.Equals(NormalPath(a), NormalPath(b), StringComparison.OrdinalIgnoreCase); }
    bool SafeTransport(Dictionary<string, object> d) {
        object cwd;
        return EmptyField(d, "env") && EmptyField(d, "env_vars") &&
            (!d.TryGetValue("cwd", out cwd) || cwd == null || (cwd is string && Path.IsPathRooted((string)cwd) && SamePath((string)cwd, Path.GetDirectoryName(Cli))));
    }
    ExternalState Inspect(string component) {
        Component(component);
        var s = new ExternalState();
        object raw = Query("mcp", "list", "--json");
        foreach (object item in ArrayValue(raw)) {
            var d = Obj(item); string name = Text(Get(d, "name"));
            if (name != "xshell-bridge") continue;
            if (s.Present) throw Error("Codex duplicate registration");
            s.Present = true;
            if (component == "mcp") {
                var detail = Obj(Query("mcp", "get", "xshell-bridge", "--json"));
                if (Text(Get(detail, "name")) != name) throw Error("Codex registration changed");
                s.Enabled = Flag(Get(detail, "enabled")); var t = Obj(Get(detail, "transport"));
                if(Text(Get(t,"type"))=="streamable_http") {
                    s.Url=Text(Get(t,"url"));s.TokenEnv=Text(Get(t,"bearer_token_env_var"));
                    s.EmptyEnv=EmptyField(t,"http_headers")&&EmptyField(t,"env_http_headers")&&EmptyField(t,"http_headers_helper");
                    continue;
                }
                if (Text(Get(t, "type")) != "stdio") { s.Command = ""; s.Args = new string[0]; continue; }
                s.Command = Text(Get(t, "command")); s.Args = ArrayValue(Get(t, "args")).Select(Text).ToArray();
                s.EmptyEnv = SafeTransport(t) && SafeTransport(detail);
            }
        }
        return s;
    }
    string Cli { get { return Path.Combine(bundle, "xshctl.exe"); } }
    string[] McpArgs { get { return new[] { "--config", config, "mcp" }; } }
    string McpUrl {get {return new Uri(ConnectionProfiles.ReadClient(config).Endpoint,"mcp").AbsoluteUri;}}
    string TokenEnvironment {get{return "XSHELL_BRIDGE_TOKEN_"+Hash(Encoding.UTF8.GetBytes(config.ToUpperInvariant())).Substring(0,16).ToUpperInvariant();}}
    string EnvironmentHash(string name) {string value=readEnvironment(name);return value==null?"absent":Hash(Encoding.UTF8.GetBytes(value));}
    bool Matches(ExternalState s, Ownership o) {
        if (!s.Present || !s.Enabled) return false;
        if(o.url!=null)return s.EmptyEnv && s.Url==o.url && s.TokenEnv==o.token_env;
        return s.EmptyEnv && String.Equals(s.Command, o.command, StringComparison.Ordinal) && s.Args.SequenceEqual(o.args ?? new string[0]);
    }
    string[][] Commands(string action, string component) {
        var commands = new List<string[]>();
        if (component == "mcp") {
            if (action != "install") commands.Add(new[] { "mcp", "remove", "xshell-bridge" });
            if (action != "uninstall") commands.Add(new[] { "mcp", "add", "xshell-bridge", "--url", McpUrl, "--bearer-token-env-var", TokenEnvironment });
        }
        return commands.ToArray();
    }
    readonly JavaScriptSerializer json = new JavaScriptSerializer();
    readonly object sync = new object();
    readonly Dictionary<string, Ticket> tickets = new Dictionary<string, Ticket>();
    sealed class Ticket { public string Action, Component, Snapshot; public DateTime Expires; }
    public sealed class PreviewResult {
        public string ticket, action, component, source_hash, config_path, expires, notice;
        public string[] paths, commands;
        public SkillFile[] files;
    }
    public sealed class SkillFile { public string source, target, sha256, content; }
    public sealed class Ownership {
        public string component, version, command, config_identity, source_hash, url, token_env, token_hash;
        public string[] args;
        public Dictionary<string, string> hashes;
    }
    public Integrations(string bundleRoot, string managedRoot, string userRoot, string clientConfig, Func<DateTime> utcNow, Func<string[], ExternalResult> runner = null, Func<string,string> readEnv=null, Action<string,string> writeEnv=null) {
        readEnvironment=readEnv ?? (name=>Environment.GetEnvironmentVariable(name,EnvironmentVariableTarget.User));
        writeEnvironment=writeEnv ?? ((name,value)=>Environment.SetEnvironmentVariable(name,value,EnvironmentVariableTarget.User));
        // Injected runners must never trigger real machine discovery, even during Status.
        if (runner == null) {
            locator = new CodexLocator();
            this.runner = RealRunner;
        } else {
            this.runner = runner;
            detection = new CodexLocator.Result { status = "injected", source = "injected", detail = "使用注入的执行入口；未进行本机检测。" };
        }
        bundle = Absolute(bundleRoot); managed = Absolute(managedRoot); home = Absolute(userRoot); config = Absolute(clientConfig);
        clock = utcNow ?? delegate { return DateTime.UtcNow; };
        if (managed == bundle || managed == home || managed.StartsWith(bundle + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase)) throw Error("接入目录不能位于程序资源目录内。");
    }
    static InvalidOperationException Error(string text) { return new InvalidOperationException(text); }
    static string Absolute(string value) {
        if (String.IsNullOrWhiteSpace(value) || value.Any(char.IsControl)) throw Error("路径无效。");
        return Path.GetFullPath(value).TrimEnd(Path.DirectorySeparatorChar);
    }
    public static string Quote(string value) {
        if (value == null || value.Any(char.IsControl)) throw Error("命令参数不能包含控制字符。");
        var result = new StringBuilder("\""); int slashes = 0;
        foreach (char c in value) {
            if (c == '\\') { slashes++; continue; }
            if (c == '"') result.Append('\\', slashes * 2 + 1); else result.Append('\\', slashes);
            result.Append(c); slashes = 0;
        }
        result.Append('\\', slashes * 2); return result.Append('"').ToString();
    }
    static void Safe(string path) {
        string current = Path.GetFullPath(path);
        while (!String.IsNullOrEmpty(current)) {
            if ((File.Exists(current) || Directory.Exists(current)) && (File.GetAttributes(current) & FileAttributes.ReparsePoint) != 0) throw Error("拒绝重解析点路径。");
            current = Path.GetDirectoryName(current);
        }
    }
    static string Hash(byte[] bytes) { using (var sha = SHA256.Create()) return BitConverter.ToString(sha.ComputeHash(bytes)).Replace("-", "").ToLowerInvariant(); }
    static string FileHash(string path) { Safe(path); return File.Exists(path) ? Hash(File.ReadAllBytes(path)) : "absent"; }
    static void Component(string value) { if (value != "mcp" && value != "skill") throw Error("不支持的组件。"); }
    string Manifest(string component) { return Path.Combine(managed, "ownership-" + component + ".json"); }
    static readonly string[] SkillPaths = { "SKILL.md", "references/commands.md", "references/inspection.md" };
    string SkillRoot { get { return Path.Combine(home, ".agents", "skills", "xshctl"); } }
    string SkillSource(string name) { return Path.Combine(bundle, "skills", "xshctl", name.Replace('/', Path.DirectorySeparatorChar)); }
    string SkillTarget(string name) { return Path.Combine(SkillRoot, name.Replace('/', Path.DirectorySeparatorChar)); }
    static string SkillText(string name, byte[] bytes) {
        string text;
        try { text = new UTF8Encoding(false, true).GetString(bytes); }
        catch (DecoderFallbackException) { throw Error("Skill 文件不是有效的 UTF-8：" + name); }
        if (text.Any(c => Char.IsControl(c) && c != '\n' && c != '\r' && c != '\t')) throw Error("Skill 文件包含无法预览的控制字符：" + name);
        return text;
    }
    Dictionary<string, byte[]> Sources(string component) {
        var files = new Dictionary<string, byte[]>(StringComparer.OrdinalIgnoreCase);
        Component(component);
        if (component == "skill") {
            foreach (string name in SkillPaths) {
                string source = SkillSource(name); Safe(source);
                if (!File.Exists(source)) throw Error("程序包缺少必需的 Skill 文件：" + name);
                byte[] content = File.ReadAllBytes(source); SkillText(name, content);
                files.Add(SkillTarget(name), content);
            }
            files.Add(Path.Combine(SkillRoot, ".xshell-bridge-version"), Encoding.UTF8.GetBytes(Version + "\n"));
        }
        return files;
    }
    Ownership ReadOwnership(string component) {
        string path = Manifest(component); Safe(path);
        if (!File.Exists(path)) return null;
        var expected = Sources(component);
        try {
            var record = json.Deserialize<Ownership>(File.ReadAllText(path));
            if (record == null || record.component != component || record.hashes == null || String.IsNullOrEmpty(record.version)) throw Error("所有权记录无效。");
            var keys = new HashSet<string>(record.hashes.Keys, StringComparer.OrdinalIgnoreCase);
            bool legacy = component == "skill" && keys.Count == 2 && keys.Contains(SkillTarget("SKILL.md")) && keys.Contains(Path.Combine(SkillRoot, ".xshell-bridge-version"));
            if (keys.Count != record.hashes.Count || (!legacy && keys.Count != expected.Count) || keys.Any(k => !expected.ContainsKey(k)) || record.hashes.Any(p => p.Value == null || p.Value.Length != 64 || p.Value.Any(c => !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f'))))) throw Error("所有权记录无效。");
            // Canonicalize only after exact whitelist matching; Windows path case is not identity.
            record.hashes = record.hashes.ToDictionary(p => expected.Keys.First(k => String.Equals(k, p.Key, StringComparison.OrdinalIgnoreCase)), p => p.Value, StringComparer.OrdinalIgnoreCase);
            return record;
        } catch { throw Error("所有权记录无效；不会修改文件。"); }
    }
    string State(string component, out string version) {
        version = null;
        Ownership owner = ReadOwnership(component);
        if (component == "mcp") {
            var external = Inspect(component);
            if (owner == null) return external.Present ? "conflict" : "absent";
            version = owner.version;
            if(owner.url!=null) {
                if(owner.token_env==null || !System.Text.RegularExpressions.Regex.IsMatch(owner.token_env,"\\AXSHELL_BRIDGE_TOKEN_[0-9A-F]{16}\\z") || owner.token_hash!=EnvironmentHash(owner.token_env))return "modified";
                return Matches(external,owner) ? "installed" : "modified";
            }
            if (String.IsNullOrEmpty(owner.command) || !Path.IsPathRooted(owner.command) || owner.args == null || owner.args.Length != 3 || owner.args[0] != "--config" || !Path.IsPathRooted(owner.args[1]) || owner.args[2] != "mcp" || owner.config_identity != Hash(Encoding.UTF8.GetBytes(owner.args[1]))) return "modified";
            return Matches(external, owner) && !owner.hashes.Any(p => FileHash(p.Key) != p.Value) ? "installed" : "modified";
        }
        var files = Sources(component);
        foreach (string path in files.Keys) Safe(path);
        if (owner == null) return files.Keys.Any(p => File.Exists(p) || Directory.Exists(p)) ? "conflict" : "absent";
        version = owner.version;
        return owner.hashes.Any(p => FileHash(p.Key) != p.Value) ? "modified" : "installed";
    }
    void ValidateConfig() {
        Safe(config);
        try {
            ConnectionProfiles.ReadClient(config);
        } catch { throw Error("客户端配置必须包含数字回环端点及有效配对凭据。"); }
    }
    string RuntimeVersion() {
        if (FileHash(Cli) == "absent") throw Error("程序内置 CLI 缺失，请更新完整程序包。");
        return Version;
    }
    bool NeedsUpdate(string component, string installed, string state) {
        if (state != "installed") return false;
        if (installed != Version) return true;
        var owner = ReadOwnership(component);
        if (component == "skill") return Sources(component).Any(p => !owner.hashes.ContainsKey(p.Key) || owner.hashes[p.Key] != Hash(p.Value));
        return owner == null || owner.url!=McpUrl || owner.token_env!=TokenEnvironment || owner.config_identity!=Hash(Encoding.UTF8.GetBytes(config)) || owner.token_hash!=Hash(Encoding.UTF8.GetBytes(ConnectionProfiles.ReadClient(config).Token));
    }
    public object Manual(string component) {
        Component(component);
        var commands = new List<string>();
        if (component == "mcp" && File.Exists(config)) {
            try {
            foreach (string action in new[] { "install", "update", "uninstall" })
                foreach (var command in Commands(action, component)) {
                    string line = "codex " + String.Join(" ", command.Select(Quote));
                    if (!commands.Contains(line)) commands.Add(line);
                }
            } catch(InvalidOperationException) {commands.Clear();}
        }
        string source = component == "skill" ? Path.Combine(bundle, "skills", "xshctl", "SKILL.md") : "";
        string target = component == "skill" ? Path.Combine(home, ".agents", "skills", "xshctl", "SKILL.md") : "";
        var files = component == "skill" ? SkillPaths.Select(name => new { source = SkillSource(name), target = SkillTarget(name) }).ToArray() : new[] { new { source = "", target = "" } }.Take(0).ToArray();
        return new { component = component, commands = commands.ToArray(), copy_source = source, copy_target = target, files = files, config_path = config, notice = "只读手动说明。HTTP MCP 复用工作台 Bridge；命令中的凭据环境变量需从 client.json 配置为当前用户环境变量，值不在此显示。建议使用预览安装完成配置，再完全退出并重新启动 Codex。普通 CLI 执行完即退出。" };
    }
    public object Status() {
        lock (sync) {
            var rows = new List<object>();
            foreach (string component in new[] { "mcp", "skill" }) {
                string installed = null, state;
                try { state = State(component, out installed); } catch { state = "unavailable"; }
                bool needsUpdate = false;
                try { needsUpdate = NeedsUpdate(component, installed, state); } catch { state = "unavailable"; }
                string diagnostic = null;
                CodexLocator.Result detected = null;
                if (component == "mcp") {
                    detected = detection;
                    if (state == "unavailable") {
                        diagnostic = detected == null ? "Codex 状态检查失败；请重新检查。" :
                            detected.path == null && detected.status != "injected" ? detected.detail :
                            "已检测到执行入口，但 MCP 查询失败；请检查 Codex 是否支持 mcp 命令，然后重试。";
                    }
                }
                if (component == "skill" && state == "unavailable") diagnostic = "Skill 程序包不完整、路径不可用或所有权记录异常；请核对必需文件及托管记录。";
                rows.Add(new { component = component, installed_version = installed, packaged_version = Version, status = state, needs_update = needsUpdate, detection = detected, diagnostic = diagnostic, manual_commands = Manual(component) });
            }
            string builtinStatus;
            try { builtinStatus = FileHash(Cli) == "absent" ? "missing" : "bundled"; } catch { builtinStatus = "missing"; }
            return new { builtin_cli = new { path = Cli, version = Version, version_source = "declared-bundled-version", status = builtinStatus }, components = rows, managed_root = managed, config_path = config, skill_path = Path.Combine(home, ".agents", "skills", "xshctl"), notice = "MCP 通过 HTTP 复用工作台 Bridge，新对话无需启动常驻 xshctl。安装或迁移会同步当前用户的专用凭据环境变量；完成后请完全退出并重新启动 Codex。工作台需保持打开，关闭时 Bridge 一起停止。普通 CLI 执行完即退出。" };
        }
    }
    string Snapshot(string component) {
        var values = new List<string> { component, config, FileHash(config), FileHash(Manifest(component)) };
        foreach (var file in Sources(component).OrderBy(p => p.Key, StringComparer.Ordinal)) { values.Add(file.Key); values.Add(Hash(file.Value)); values.Add(FileHash(file.Key)); }
        if (component == "mcp") values.Add(json.Serialize(Inspect(component)));
        if(component=="mcp") {
            values.Add(EnvironmentHash(TokenEnvironment));
            var owner=ReadOwnership(component);if(owner!=null && owner.token_env!=null)values.Add(EnvironmentHash(owner.token_env));
        }
        if (component == "mcp") { values.Add(Cli); values.Add(FileHash(Cli)); }
        return Hash(Encoding.UTF8.GetBytes(json.Serialize(values)));
    }
    void Check(string action, string component) {
        Component(component);
        if (action != "install" && action != "update" && action != "uninstall") throw Error("不支持的操作。");
        Safe(managed); Safe(home);
        if (component == "mcp" && action != "uninstall") ValidateConfig();
        // Preserve validation for existing file installers, but never require credentials to remove skills.
        if (component == "skill" && action != "uninstall") ValidateConfig();
        if (component == "mcp" && action != "uninstall") RuntimeVersion();
        string version; string state = State(component, out version);
        if ((action == "install" && state != "absent") || (action != "install" && state != "installed")) throw Error("安装状态冲突或文件已修改；不会覆盖或删除。");
        if(component=="mcp" && action!="uninstall") {
            var owner=ReadOwnership(component);
            if((owner==null || owner.token_env!=TokenEnvironment) && readEnvironment(TokenEnvironment)!=null)
                throw Error("凭据环境变量已有未知内容，不会覆盖。");
        }
        if (component == "skill" && action == "update") {
            Ownership owner = ReadOwnership(component);
            foreach (string path in Sources(component).Keys.Where(p => !owner.hashes.ContainsKey(p)))
                if (File.Exists(path) || Directory.Exists(path)) throw Error("新增 Skill 路径已有未知内容；不会覆盖。");
        }
    }
    public PreviewResult Preview(string action, string component) {
        lock (sync) {
            Check(action, component);
            string snapshot = Snapshot(component), ticket;
            byte[] bytes = new byte[32]; using (var rng = RandomNumberGenerator.Create()) rng.GetBytes(bytes);
            ticket = Convert.ToBase64String(bytes);
            DateTime expires = clock().AddMinutes(2);
            tickets.Clear(); tickets.Add(ticket, new Ticket { Action = action, Component = component, Snapshot = snapshot, Expires = expires });
            var sources = Sources(component); var owner = ReadOwnership(component);
            IEnumerable<string> paths = action == "uninstall" && owner != null ? (IEnumerable<string>)owner.hashes.Keys : sources.Keys;
            var files = component == "skill" ? SkillPaths.Where(name => action != "uninstall" || owner.hashes.ContainsKey(SkillTarget(name))).Select(name => {
                string target = SkillTarget(name); byte[] content = action == "uninstall" ? File.ReadAllBytes(target) : sources[target];
                return new SkillFile { source = SkillSource(name), target = target, sha256 = Hash(content), content = SkillText(name, content) };
            }).ToArray() : new SkillFile[0];
            return new PreviewResult { notice = component=="mcp" ? "本次同步 HTTP 接入与当前用户凭据环境变量；不会显示令牌。完成后完全退出并重新启动 Codex。" : "仅同步 Skill 文件。", ticket = ticket, action = action, component = component, source_hash = snapshot, config_path = config, expires = expires.ToString("o"), paths = paths.Concat(new[] { Manifest(component) }).ToArray(), files = files, commands = Commands(action, component).Select(a => "codex " + String.Join(" ", a.Select(Quote))).ToArray() };
        }
    }
    static void AtomicWrite(string path, byte[] bytes) {
        Safe(path);
        if (bytes == null) { File.Delete(path); return; }
        Directory.CreateDirectory(Path.GetDirectoryName(path));
        string temp = Path.Combine(Path.GetDirectoryName(path), ".xshell-" + Guid.NewGuid().ToString("N") + ".tmp");
        try { using (var stream = new FileStream(temp, FileMode.CreateNew, FileAccess.Write, FileShare.None)) { stream.Write(bytes, 0, bytes.Length); stream.Flush(true); }
            Safe(path); if (File.Exists(path)) File.Replace(temp, path, null); else File.Move(temp, path);
        } finally { if (File.Exists(temp)) File.Delete(temp); }
    }
    void Clean(IEnumerable<string> paths, ISet<string> preserve = null) {
        foreach (string file in paths) {
            string dir = Path.GetDirectoryName(file);
            while (dir != null && (dir.StartsWith(managed + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase) || dir.StartsWith(Path.Combine(home, ".agents", "skills", "xshctl") + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase) || dir == Path.Combine(home, ".agents", "skills", "xshctl"))) {
                if (preserve != null && preserve.Contains(dir)) break;
                try { Safe(dir); if (!Directory.Exists(dir) || Directory.EnumerateFileSystemEntries(dir).Any()) break; Directory.Delete(dir); } catch { break; }
                dir = Path.GetDirectoryName(dir);
            }
        }
    }
    public object Apply(string ticket) {
        using (var mutex = new Mutex(false, "Local\\XshellIntegrations-" + Hash(Encoding.UTF8.GetBytes(managed.ToUpperInvariant())))) {
            bool acquired = false;
            try { try { acquired = mutex.WaitOne(30000); } catch (AbandonedMutexException) { acquired = true; }
                if (!acquired) throw Error("接入操作繁忙。"); return ApplyLocked(ticket);
            } finally { if (acquired) mutex.ReleaseMutex(); }
        }
    }
    object ApplyLocked(string ticket) {
        lock (sync) {
            Ticket review;
            if (ticket == null || !tickets.TryGetValue(ticket, out review)) throw Error("确认已失效。");
            tickets.Remove(ticket);
            if (clock() >= review.Expires) throw Error("确认已过期。");
            Check(review.Action, review.Component);
            if (Snapshot(review.Component) != review.Snapshot) throw Error("来源、配置或安装状态已变化，请重新预览。");
            var sources = Sources(review.Component);
            var writes = new Dictionary<string, byte[]>(sources, StringComparer.OrdinalIgnoreCase);
            string manifest = Manifest(review.Component);
            Ownership previous = ReadOwnership(review.Component);
            if (review.Action == "uninstall") { writes.Clear(); foreach (string path in previous.hashes.Keys) writes[path] = null; }
            string cliVersion = review.Component == "mcp" && review.Action != "uninstall" ? RuntimeVersion() : Version;
            ExternalState externalBefore = review.Component == "mcp" ? Inspect(review.Component) : null;
            string externalBeforeJson = externalBefore == null ? null : json.Serialize(externalBefore);
            var owner = new Ownership { component = review.Component, version = review.Component == "mcp" ? cliVersion : Version, command = review.Component == "mcp" ? Cli : "", args = review.Component == "mcp" ? McpArgs : new string[0], source_hash = review.Component == "mcp" ? FileHash(Cli) : null, config_identity = Hash(Encoding.UTF8.GetBytes(config)), hashes = sources.ToDictionary(p => p.Key, p => Hash(p.Value)) };
            if(review.Component=="mcp" && review.Action!="uninstall") {
                owner.command=null;owner.args=null;owner.url=McpUrl;owner.token_env=TokenEnvironment;
                owner.token_hash=Hash(Encoding.UTF8.GetBytes(ConnectionProfiles.ReadClient(config).Token));
            }
            writes.Add(manifest, review.Action == "uninstall" ? null : Encoding.UTF8.GetBytes(json.Serialize(owner)));
            var prior = new Dictionary<string, byte[]>(); var changed = new List<string>();
            var existingDirs = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
            foreach (string path in writes.Keys) {
                string dir = Path.GetDirectoryName(path);
                while (dir != null) { if (Directory.Exists(dir)) existingDirs.Add(dir); dir = Path.GetDirectoryName(dir); }
            }
            string manifestBefore = FileHash(manifest);
            bool externalAttempted = false;
            ExternalState expectedExternal = externalBefore;
            var environmentBefore=new Dictionary<string,string>();
            var environmentAfter=new Dictionary<string,string>();
            if(review.Component=="mcp") {
                if(previous!=null && previous.token_env!=null)environmentAfter[previous.token_env]=null;
                if(review.Action!="uninstall")environmentAfter[TokenEnvironment]=ConnectionProfiles.ReadClient(config).Token;
                foreach(string name in environmentAfter.Keys)environmentBefore[name]=readEnvironment(name);
            }
            var changedEnvironment=new List<string>();
            try {
                foreach(var entry in environmentAfter) {
                    if(readEnvironment(entry.Key)!=environmentBefore[entry.Key])throw Error("凭据环境变量已变化。");
                    changedEnvironment.Add(entry.Key);writeEnvironment(entry.Key,entry.Value);
                    if(readEnvironment(entry.Key)!=entry.Value)throw Error("凭据环境变量写入失败。");
                }
                foreach (var entry in writes.Where(p => p.Key != manifest)) {
                    Safe(entry.Key);
                    prior.Add(entry.Key, File.Exists(entry.Key) ? File.ReadAllBytes(entry.Key) : null);
                    string expectedBefore = previous != null && previous.hashes.ContainsKey(entry.Key) ? previous.hashes[entry.Key] : "absent";
                    string actualBefore = prior[entry.Key] == null ? "absent" : Hash(prior[entry.Key]);
                    if (actualBefore != expectedBefore || FileHash(entry.Key) != expectedBefore) throw Error("文件在写入前发生变化。");
                    changed.Add(entry.Key);
                    AtomicWrite(entry.Key, entry.Value);
                }
                foreach (string[] command in Commands(review.Action, review.Component)) {
                    if (json.Serialize(Inspect(review.Component)) != json.Serialize(expectedExternal)) throw Error("Codex registration changed during operation");
                    externalAttempted = true;
                    Run(command);
                    expectedExternal = Inspect(review.Component);
                }
                foreach (var entry in writes.Where(p => p.Key != manifest))
                    if (FileHash(entry.Key) != (entry.Value == null ? "absent" : Hash(entry.Value))) throw Error("写入后的文件发生变化。");
                if (review.Component == "mcp") {
                    var actual = Inspect(review.Component);
                    if (review.Action == "uninstall" ? actual.Present : !Matches(actual, owner)) throw Error("Codex verification failed");
                }
                prior.Add(manifest, File.Exists(manifest) ? File.ReadAllBytes(manifest) : null);
                if ((prior[manifest] == null ? "absent" : Hash(prior[manifest])) != manifestBefore || FileHash(manifest) != manifestBefore) throw Error("所有权记录在写入前发生变化。");
                changed.Add(manifest);
                AtomicWrite(manifest, writes[manifest]);
                if (FileHash(manifest) != (writes[manifest] == null ? "absent" : Hash(writes[manifest]))) throw Error("所有权记录验证失败。");
                foreach (var entry in writes.Where(p => p.Key != manifest))
                    if (FileHash(entry.Key) != (entry.Value == null ? "absent" : Hash(entry.Value))) throw Error("文件验证失败。");
            } catch {
                bool rollbackFailed = false;
                // Never reverse an unknown external edit. Only restore a precisely recognized
                // intermediate state.
                if (externalAttempted) {
                    try {
                        var currentExternal = Inspect(review.Component);
                        if (json.Serialize(currentExternal) != externalBeforeJson) {
                            if (review.Component == "mcp" && previous != null && !currentExternal.Present) {
                                Run(previous.url!=null ? new[]{"mcp","add","xshell-bridge","--url",previous.url,"--bearer-token-env-var",previous.token_env} : new[] { "mcp", "add", "xshell-bridge", "--", previous.command }.Concat(previous.args).ToArray());
                            } else rollbackFailed = true;
                        }
                        if (json.Serialize(Inspect(review.Component)) != externalBeforeJson) rollbackFailed = true;
                    } catch { rollbackFailed = true; }
                }
                foreach (string path in changed.AsEnumerable().Reverse()) {
                    try {
                        Safe(path);
                        // Do not overwrite a concurrent edit during recovery.
                        string expected = writes[path] == null ? "absent" : Hash(writes[path]);
                        string before = prior[path] == null ? "absent" : Hash(prior[path]);
                        string current = FileHash(path);
                        if (current == before) continue;
                        if (current != expected) { rollbackFailed = true; continue; }
                        AtomicWrite(path, prior[path]);
                        if (FileHash(path) != before) rollbackFailed = true;
                    } catch { rollbackFailed = true; }
                }
                foreach(string name in changedEnvironment.AsEnumerable().Reverse()) {
                    try {
                        string current=readEnvironment(name);
                        if(current==environmentBefore[name])continue;
                        if(current!=environmentAfter[name]){rollbackFailed=true;continue;}
                        writeEnvironment(name,environmentBefore[name]);
                        if(readEnvironment(name)!=environmentBefore[name])rollbackFailed=true;
                    } catch {rollbackFailed=true;}
                }
                Clean(changed, existingDirs);
                throw Error(rollbackFailed ? "操作部分完成且恢复不完整；需要手动修复。请检查 Codex 注册及预览中的路径；未覆盖未知外部更改。" : "写入失败；已验证恢复到操作前的文件及外部状态。");
            }
            Clean(writes.Keys.Where(p => p != manifest));
            return new { changed = true, component = review.Component, version = review.Action == "uninstall" ? null : owner.version };
        }
    }
    public void Invalidate() { lock (sync) tickets.Clear(); }
}
}
