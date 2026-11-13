using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Threading.Tasks;
using System.Web.Script.Serialization;

namespace XshellDesktop {
internal static class BackendHostReverseTests {
    static readonly JavaScriptSerializer json = new JavaScriptSerializer();
    static void Check(bool value, string message) { if (!value) throw new Exception(message); }
    static Dictionary<string,object> Map(object value) { return (Dictionary<string,object>)value; }
    static object Clone(object value) { return json.DeserializeObject(json.Serialize(value)); }
    static void Set(Backend backend, string field, object value) {
        typeof(Backend).GetField(field, BindingFlags.Instance | BindingFlags.NonPublic).SetValue(backend, value);
    }
    sealed class FakeProcess : IReverseProcess {
        internal bool Exited, ProbeThrows;
        internal int Stops, Disposals;
        public bool HasExited { get { if (ProbeThrows) throw new InvalidOperationException("fake probe failure"); return Exited; } }
        public int ExitCode { get { return 17; } }
        public bool WaitForExit(int milliseconds) { return Exited; }
        public void Stop() { Stops++; Exited = true; }
        public void Dispose() { Disposals++; }
    }
    sealed class Fixture : IDisposable {
        internal readonly string Root, Runtime, Bundle, Key, Known;
        internal readonly List<FakeProcess> Processes = new List<FakeProcess>();
        internal readonly FakeProcess Unowned = new FakeProcess();
        internal readonly List<string> Calls = new List<string>();
        internal object[] Sessions = new object[0];
        internal bool Capability = true, FailSessions, AsyncHealth;
        internal bool Preserve;
        internal int Dialogs;
        internal string Selection;
        internal Backend Backend;
        internal Fixture(bool observer = false, string mode = "Local", bool original = true, bool preserve = false) {
            Preserve = preserve;
            Root = Path.Combine(preserve ? (Environment.GetEnvironmentVariable("REPORT_DIR") ?? Path.Combine(Environment.CurrentDirectory, "dist", "_test-artifacts", "export-native", "fixtures")) : Path.GetTempPath(), "backend-reverse-" + Guid.NewGuid().ToString("N"));
            Runtime = Path.Combine(Root, "runtime"); Bundle = Path.Combine(Root, "bundle");
            Directory.CreateDirectory(Runtime); Directory.CreateDirectory(Bundle);
            Key = Path.Combine(Root, "key"); Known = Path.Combine(Root, "known_hosts");
            File.WriteAllText(Key, "PRIVATE KEY MUST NOT APPEAR IN IPC");
            File.WriteAllText(Known, "host-key");
            WriteConfig("client.json", 17890, 'c'); WriteConfig("agent.json", 17890, 'a');
            File.WriteAllText(Path.Combine(Bundle, "xshell_agent.js"), "// test agent\n");
            File.WriteAllText(Path.Combine(Bundle, "remote_bootstrap.js"), "// test bootstrap\n");
            File.WriteAllText(Path.Combine(Bundle, "log_parser.js"), "// test parser\n");
            var helperFixture = new byte[2048]; new Random(42).NextBytes(helperFixture);
            File.WriteAllBytes(Path.Combine(Bundle, "XshLogCapture.exe"), helperFixture);
            var manager = new HostReverse(Path.Combine(Root, "catalog"), Bundle, Runtime, delegate(ProcessStartInfo start) {
                Check(!start.UseShellExecute && start.Arguments.Contains("-R"), "not fixed SSH forwarding");
                Check(!start.Arguments.Contains(new string('a', 48)) && !start.Arguments.Contains(new string('c', 48)), "credential in SSH arguments");
                var process = new FakeProcess(); Processes.Add(process); return process;
            });
            Backend = new Backend(Call, observer, mode, original ? Runtime : null, "http://127.0.0.1:17890/", manager, delegate(string kind) {
                Dialogs++; return Selection;
            });
        }
        internal void WriteConfig(string name, int port, char token) {
            File.WriteAllText(Path.Combine(Runtime, name), json.Serialize(new { endpoint = "http://127.0.0.1:" + port, token = new string(token, 48) }));
        }
        Task<object> Call(string method, string path, object body) {
            Calls.Add(method + " " + path);
            Check(method == "GET", "reverse adapter issued a POST");
            if (path == "/v1/health") return AsyncHealth ? Task.Run(() => Clone(new { source_agents = Capability })) : Task.FromResult(Clone(new { source_agents = Capability }));
            if (path == "/v1/sessions") {
                if (FailSessions) throw new Exception("private transport detail");
                return Task.FromResult(Clone(Sessions));
            }
            throw new Exception("unexpected transport request");
        }
        internal Dictionary<string,object> Request(object request, bool ok) {
            var response = Map(Clone(Backend.Handle(json.Serialize(request)).GetAwaiter().GetResult()));
            Check((bool)response["ok"] == ok, json.Serialize(response));
            string wire = json.Serialize(response);
            Check(!wire.Contains(new string('a', 48)) && !wire.Contains(new string('c', 48)), "token leaked");
            Check(!wire.Contains("PRIVATE SELECTOR") && !wire.Contains("PRIVATE ACL") && !wire.Contains("PRIVATE IO DETAIL") && !wire.Contains("private-invalid") && !wire.Contains("private-broken-json"), "private diagnostic detail leaked");
            Check(!wire.Contains("PRIVATE KEY MUST NOT") && !wire.Contains("ReverseExportPlan") && !wire.Contains("snapshot"), "native state leaked");
            return response;
        }
        internal string Add() {
            return (string)Map(Request(new { id = 1, op = "reverseAdd", name = "Host A", ssh_host = "host-a.example", ssh_port = 22, user = "operator", key_path = Key, known_hosts_path = Known, remote_port = 18888, environment = "test", binding_mode = "manual_terminal" }, true)["data"])["profile_id"];
        }
        internal string Prepare(string id, string path) {
            var data = Map(Request(new { id = 2, op = "reverseExportPrepare", profile_id = id, path = path }, true)["data"]);
            Check(data.Count == 3 && data.ContainsKey("ticket") && data.ContainsKey("expires") && data.ContainsKey("preview"), "unsafe prepare DTO");
            return (string)data["ticket"];
        }
        internal Dictionary<string,object> ListedProfile() {
            var data = Map(Request(new { id = 1, op = "reverseList" }, true)["data"]);
            return Map(((object[])data["profiles"])[0]);
        }
        public void Dispose() {
            Backend.Dispose();
            Check(Unowned.Stops == 0 && Unowned.Disposals == 0, "unowned process affected");
            if (!Preserve) Directory.Delete(Root, true);
        }
    }
    static object Session(string source, string state, int age, string name) {
        return new { fingerprint = json.Serialize(new { SourceID = source }), state = state, last_seen = DateTime.UtcNow.AddSeconds(-age).ToString("o"), name = name, environment = "untrusted-label" };
    }
    static void Main(string[] args) {
        try { if (args.Length >= 1 && args[0] == "--export-diagnostics") ExportDiagnostics(args.Length > 1 ? args[1] : null); else Run(); Console.WriteLine("PASS: native host reverse backend isolation"); }
        catch (Exception error) { Console.Error.WriteLine(error.Message); Environment.ExitCode = 1; }
    }
    // No ACL writes, SSH or dialogs. Keep synthetic fixtures in the requested artifact directory.
    static void ExportDiagnostics(string reparseRoot = null) {
        using (var f = new Fixture(preserve: true)) {
            string id = new string('b', 32), catalog = Path.Combine(f.Root, "catalog", "host-reverse.json");
            Directory.CreateDirectory(Path.GetDirectoryName(catalog));
            File.WriteAllText(catalog, json.Serialize(new[] { new { id = id, name = "synthetic", sshHost = "example.invalid", sshPort = 22, user = "operator", keyPath = f.Key, knownHostsPath = f.Known, remotePort = 18888, environment = "test", bindingMode = "manual_terminal", logEncoding = "auto" } }));
            string chosen = Path.Combine(f.Root, "safe & export.js");
            f.AsyncHealth = true;
            Action<Dictionary<string,object>,string,string,string> error = delegate(Dictionary<string,object> r, string code, string stage, string path) {
                Check(r.ContainsKey("code") && (string)r["code"] == code, "missing/wrong code: " + json.Serialize(r));
                Check((string)r["stage"] == stage && (string)r["path"] == path, "lost stage/path");
                Check(((string)r["error"]).Contains(code) && !((string)r["error"]).Contains("旧窗口"), "unactionable error");
            };
            Func<string,Dictionary<string,object>> prepare = path => f.Request(new { id = 8, op = "reverseExportPrepare", profile_id = id, path = path }, false);
            f.Backend.SetReversePathSelector(delegate(string kind) { throw new InvalidOperationException("PRIVATE SELECTOR " + new string('a', 48)); });
            error(f.Request(new { id = 7, op = "reverseChoosePath", kind = "export" }, false), "EXPORT_UNEXPECTED", "selection", null);
            f.Backend.SetReversePathSelector(delegate(string kind) { throw new TargetInvocationException(new UnauthorizedAccessException("PRIVATE ACL")); });
            error(f.Request(new { id = 7, op = "reverseChoosePath", kind = "export" }, false), "EXPORT_ACCESS_DENIED", "selection", null);
            f.Backend.SetReversePathSelector(delegate(string kind) { return chosen; });
            Check((string)Map(f.Request(new { id = 7, op = "reverseChoosePath", kind = "export" }, true)["data"])["path"] == chosen, "chooser changed path");
            Check(!File.Exists(chosen) && f.Processes.Count == 0, "selection had effects");
            error(prepare("relative.js"), "EXPORT_PATH_INVALID", "preparation", null);
            if (reparseRoot != null) {
                string linked = Path.Combine(reparseRoot, "out.js");
                error(prepare(linked), "EXPORT_REPARSE_POINT", "preparation", linked);
                string constructorCode = null;
                try { using (var unsafeManager = new HostReverse(Path.Combine(f.Root, "unsafe-catalog"), reparseRoot, f.Runtime)) { } }
                catch (ReverseExportError e) { constructorCode = e.Code; }
                catch (InvalidOperationException) { }
                Check(constructorCode == "EXPORT_REPARSE_POINT", "manager creation lost path diagnostic");
            }
            f.Capability = false; error(prepare(chosen), "EXPORT_CAPABILITY_UNAVAILABLE", "preparation", chosen); f.Capability = true;
            f.Backend.SetReversePathSelector(delegate(string kind) { throw new IOException("PRIVATE IO DETAIL"); });
            error(f.Request(new { id = 7, op = "reverseChoosePath", kind = "export" }, false), "EXPORT_IO_UNAVAILABLE", "selection", null);
            string existing = Path.Combine(f.Root, "existing.js"); File.WriteAllText(existing, "keep");
            error(prepare(existing), "EXPORT_TARGET_EXISTS", "preparation", existing);
            string badExt = Path.Combine(f.Root, "out.txt"); error(prepare(badExt), "EXPORT_EXTENSION", "preparation", badExt);
            string missing = Path.Combine(f.Root, "absent", "out.js"); error(prepare(missing), "EXPORT_PARENT_MISSING", "preparation", missing);
            foreach (string root in new[] { f.Runtime, f.Bundle }) {
                string protectedPath = Path.Combine(root, "out.js"); error(prepare(protectedPath), "EXPORT_PROTECTED_PATH", "preparation", protectedPath);
            }
            foreach (string name in new[] { "xshell_agent.js", "remote_bootstrap.js", "log_parser.js", "XshLogCapture.exe" }) {
                string file = Path.Combine(f.Bundle, name), parked = file + ".parked"; File.Move(file, parked);
                error(prepare(chosen), "EXPORT_SOURCE_MISSING", "preparation", chosen); File.Move(parked, file);
            }
            string source = Path.Combine(f.Bundle, "log_parser.js");
            string helper = Path.Combine(f.Bundle, "XshLogCapture.exe"); byte[] helperBytes = File.ReadAllBytes(helper);
            File.WriteAllBytes(helper, new byte[1]); error(prepare(chosen), "EXPORT_SOURCE_INVALID", "preparation", chosen); File.WriteAllBytes(helper, helperBytes);
            f.WriteConfig("agent.json", 17891, 'a'); error(prepare(chosen), "EXPORT_PAIRING_MISMATCH", "preparation", chosen);
            File.WriteAllText(Path.Combine(f.Runtime, "agent.json"), "{private-invalid"); error(prepare(chosen), "EXPORT_CONFIG_INVALID", "preparation", chosen);
            f.WriteConfig("agent.json", 17890, 'a');
            // Runtime configurations written with a UTF-8 BOM retain the prior decoding behavior.
            File.WriteAllText(Path.Combine(f.Runtime, "agent.json"), File.ReadAllText(Path.Combine(f.Runtime, "agent.json")), new System.Text.UTF8Encoding(true));
            var manager = (HostReverse)typeof(Backend).GetField("reverse", BindingFlags.Instance | BindingFlags.NonPublic).GetValue(f.Backend);
            foreach (string changed in new[] { Path.Combine(f.Runtime, "client.json"), Path.Combine(f.Runtime, "agent.json"), Path.Combine(f.Bundle, "xshell_agent.js"), Path.Combine(f.Bundle, "remote_bootstrap.js"), Path.Combine(f.Bundle, "log_parser.js"), helper, catalog }) {
                byte[] before = File.ReadAllBytes(changed);
                var plan = manager.PrepareExport(id, Path.Combine(f.Root, Guid.NewGuid().ToString("N") + ".js"));
                if (changed == catalog) File.WriteAllText(changed, File.ReadAllText(changed).Replace("\"logEncoding\":\"auto\"", "\"logEncoding\":\"gbk\""));
                else File.AppendAllText(changed, " ");
                string diagnosticCode = null;
                try { manager.ConfirmExport(plan); } catch (ReverseExportError e) { diagnosticCode = e.Code; }
                Check(diagnosticCode == "EXPORT_SNAPSHOT_CHANGED" && plan.consumed && !File.Exists(plan.destination), "native snapshot changed/consumption failed");
                File.WriteAllBytes(changed, before);
            }
            // Rebuilding reflection metadata must not invalidate unchanged settings.
            for (int attempt = 0; attempt < 12; attempt++) {
                string stablePath = Path.Combine(f.Root, "stable-" + attempt + ".js");
                var stable = manager.PrepareExport(id, stablePath);
                GC.Collect(); GC.WaitForPendingFinalizers();
                manager.ConfirmExport(stable);
                Check(File.Exists(stablePath), "unchanged snapshot failed after metadata collection");
            }
            var immutable = manager.PrepareExport(id, Path.Combine(f.Root, "immutable.js"));
            var projection = immutable.PublicPreview(); projection.path = Path.Combine(f.Root, "wrong.js"); projection.source_id = new string('f', 32);
            File.WriteAllText(immutable.destination, "keep immutable");
            try { manager.ConfirmExport(immutable); throw new Exception("native destination not checked"); }
            catch (ReverseExportError e) { Check(e.Code == "EXPORT_TARGET_EXISTS" && e.ChosenPath == immutable.destination, "preview mutated native plan"); }
            try { manager.ConfirmExport(immutable); throw new Exception("native plan reused"); }
            catch (ReverseExportError e) { Check(e.Code == "EXPORT_CONFIRMATION_INVALID", "native plan not consumed"); }
            string ticket = f.Prepare(id, chosen);
            File.AppendAllText(source, "// changed");
            error(f.Request(new { id = 9, op = "reverseExportConfirm", ticket = ticket }, false), "EXPORT_SNAPSHOT_CHANGED", "confirmation", chosen);
            error(f.Request(new { id = 9, op = "reverseExportConfirm", ticket = ticket }, false), "EXPORT_CONFIRMATION_INVALID", "confirmation", null);
            ticket = f.Prepare(id, chosen); File.WriteAllText(chosen, "do not replace");
            error(f.Request(new { id = 9, op = "reverseExportConfirm", ticket = ticket }, false), "EXPORT_TARGET_EXISTS", "confirmation", chosen);
            Check(File.ReadAllText(chosen) == "do not replace" && File.ReadAllText(existing) == "keep", "overwrote destination");
            string contextPath = Path.Combine(f.Root, "context.js"); ticket = f.Prepare(id, contextPath);
            f.Capability = false;
            error(f.Request(new { id = 9, op = "reverseExportConfirm", ticket = ticket }, false), "EXPORT_CAPABILITY_UNAVAILABLE", "confirmation", contextPath);
            f.Capability = true;
            error(f.Request(new { id = 9, op = "reverseExportConfirm", ticket = ticket }, false), "EXPORT_CONFIRMATION_INVALID", "confirmation", null);
            ticket = f.Prepare(id, contextPath); Set(f.Backend, "connectionEndpoint", "http://127.0.0.1:17892/");
            error(f.Request(new { id = 9, op = "reverseExportConfirm", ticket = ticket }, false), "EXPORT_CONTEXT_CHANGED", "confirmation", contextPath);
            Set(f.Backend, "connectionEndpoint", "http://127.0.0.1:17890/");
            error(f.Request(new { id = 9, op = "reverseExportConfirm", ticket = ticket }, false), "EXPORT_CONFIRMATION_INVALID", "confirmation", null);
            string other = Path.Combine(f.Root, "other.js"); ticket = f.Prepare(id, other);
            File.AppendAllText(catalog, "private-broken-json");
            error(f.Request(new { id = 9, op = "reverseExportConfirm", ticket = ticket }, false), "EXPORT_PROFILE_INVALID", "confirmation", other);
            Check(!File.Exists(other) && f.Processes.Count == 0, "diagnostics exported or connected");
        }
        Console.WriteLine("PASS: export diagnostics, redaction, chosen path, consumed tickets and snapshots");
    }
    static void CatalogLossKeepsContextGuarded() {
        foreach (string change in new[] { "remove", "delete", "corrupt" }) using (var f = new Fixture()) {
            string id = f.Add();
            f.Request(new { id = 1, op = "reverseEncodingSave", profile_id = id, log_encoding = "gb18030" }, true);
            f.Request(new { id = 1, op = "reverseEncodingSave", profile_id = id, log_encoding = "unsafe" }, false);
            f.Request(new { id = 1, op = "reverseConnect", profile_id = id }, true);
            string catalog = Path.Combine(f.Root, "catalog", "host-reverse.json");
            if (change == "remove") {
                using (var peer = new HostReverse(Path.Combine(f.Root, "catalog"), f.Bundle, f.Runtime, delegate(ProcessStartInfo start) {
                    throw new Exception("peer must not start SSH");
                })) peer.Remove(id);
            } else if (change == "delete") File.Delete(catalog);
            else File.WriteAllText(catalog, "{bad JSON");
            Check(f.Backend.HasReverseTunnels, "catalog loss bypassed ownership guard");
            if (change == "corrupt") f.Request(new { id = 1, op = "reverseList" }, false);
            else {
                var row = f.ListedProfile();
                Check((string)row["id"] == id && (string)row["name"] == "Host A" && (string)row["status"] == HostReverse.AwaitingAgent, "owned SSH omitted after catalog loss");
            }
            f.Request(new { id = 1, op = "connectionPrepare", profile_id = new string('b', 32), sync_mcp = false }, false);
            f.Request(new { id = 1, op = "connectionSwitch", profile_id = new string('b', 32) }, false);
            f.Request(new { id = 1, op = "connectionRestore" }, false);
            f.Request(new { id = 1, op = "updatePrepare" }, false);
            // Assert the shared guard itself, rather than relying on an unknown profile being rejected.
            bool guarded = false;
            try {
                typeof(Backend).GetMethod("GuardReverseContextChange", BindingFlags.Instance | BindingFlags.NonPublic).Invoke(f.Backend, null);
            } catch (TargetInvocationException) { guarded = true; }
            Check(guarded && f.Processes[0].Stops == 0, "context guard failed or stopped SSH");
            f.Processes[0].ProbeThrows = true;
            try {
                Check(f.Backend.HasReverseTunnels, "observation failure must fail closed");
                f.Request(new { id = 1, op = "updatePrepare" }, false);
            } finally { f.Processes[0].ProbeThrows = false; }
            f.Request(new { id = 1, op = "reverseDisconnect", profile_id = id }, true);
            Check(!f.Backend.HasReverseTunnels && f.Processes[0].Stops == 1 && f.Processes[0].Disposals == 1, "disconnect without catalog failed");
            f.Request(new { id = 1, op = "reverseDisconnect", profile_id = id }, false);
        }
    }
    static void Run() {
        CatalogLossKeepsContextGuarded();
        // These denial tests must fail before the adapter and its native-only injection constructor exist.
        foreach (string variant in new[] { "observer", "vm", "no-original", "selected" }) using (var f = new Fixture(variant == "observer", variant == "vm" ? "Vm" : "Local", variant != "no-original")) {
            if (variant == "selected") Set(f.Backend, "selectedProfile", new string('b', 32));
            File.Delete(Path.Combine(f.Runtime, "client.json")); File.Delete(Path.Combine(f.Runtime, "agent.json"));
            var listed = Map(f.Request(new { id = 1, op = "reverseList" }, true)["data"]);
            Check(!(bool)listed["available"] && ((object[])listed["profiles"]).Length == 0, "unavailable list exposed profiles");
            f.Request(new { id = 1, op = "reverseAdd", name = "x", ssh_host = "host", ssh_port = 22, user = "u", key_path = f.Key, known_hosts_path = f.Known, remote_port = 18000, environment = "", binding_mode = "manual_terminal" }, false);
            f.Request(new { id = 1, op = "reverseConnect", profile_id = new string('b', 32) }, false);
            f.Request(new { id = 1, op = "reverseDisconnect", profile_id = new string('b', 32) }, false);
            f.Request(new { id = 1, op = "reverseDelete", profile_id = new string('b', 32) }, false);
            f.Request(new { id = 1, op = "reverseExportPrepare", profile_id = new string('b', 32), path = Path.Combine(f.Root, "denied.js") }, false);
            f.Request(new { id = 1, op = "reverseChoosePath", kind = "key" }, false);
            Check(f.Calls.Count == 0 && f.Dialogs == 0 && f.Processes.Count == 0, "denied operation performed I/O");
        }
        using (var f = new Fixture()) {
            string id = f.Add();
            File.Delete(Path.Combine(f.Runtime, "client.json")); File.Delete(Path.Combine(f.Runtime, "agent.json"));
            Check((string)f.ListedProfile()["status"] == HostReverse.Disconnected, "listing read runtime credentials");
            f.Request(new { id = 1, op = "reverseConnect", profile_id = "bad" }, false);
            f.Request(new { id = 1, op = "reverseDelete", profile_id = id, extra = true }, false);
            f.Request(new { id = 1, op = "reverseChoosePath", kind = "admin" }, false);
            f.Request(new { id = 1, op = "reverseAdd", name = 7, ssh_host = "host", ssh_port = 22, user = "u", key_path = f.Key, known_hosts_path = f.Known, remote_port = 18000, environment = "", binding_mode = "manual_terminal" }, false);
            Check(f.Processes.Count == 0 && f.Dialogs == 0, "malformed request had effects");
        }
        using (var f = new Fixture()) {
            string id = f.Add();
            f.Capability = false;
            f.Request(new { id = 1, op = "reverseConnect", profile_id = id }, false);
            f.Capability = true; f.WriteConfig("agent.json", 17891, 'a');
            f.Request(new { id = 1, op = "reverseConnect", profile_id = id }, false);
            f.WriteConfig("agent.json", 17890, 'a'); Set(f.Backend, "connectionEndpoint", "http://127.0.0.1:17892/");
            f.Request(new { id = 1, op = "reverseConnect", profile_id = id }, false);
            Check(f.Processes.Count == 0, "endpoint or capability mismatch launched SSH");
        }
        using (var f = new Fixture()) {
            string id = f.Add();
            f.Request(new { id = 1, op = "reverseConnect", profile_id = id }, true);
            Check(f.Backend.HasReverseTunnels && f.Processes.Count == 1, "owned tunnel missing");
            f.Sessions = new[] { Session(id, "active", 1, "WRONG HOST LABEL"), Session(id, "pending", 1, "x"), Session(id, "detached", 1, "x"), Session(id, "active", 60, "x"), Session(new string('f', 32), "active", 1, "Host A"), (object)new { fingerprint = "{bad JSON", state = "active", last_seen = DateTime.UtcNow.ToString("o") } };
            var profile = f.ListedProfile();
            Check((int)profile["agent_count"] == 2 && (string)profile["name"] == "Host A" && (string)profile["agent_status"] == "online", "source grouping or labels incorrect");
            f.Sessions = new object[] { new { fingerprint = json.Serialize(new { source_id = id }), state = "active", last_seen = DateTime.UtcNow.ToString("o"), name = "Host A" } };
            profile = f.ListedProfile();
            Check((int)profile["agent_count"] == 0 && profile["last_seen"] == null && (string)profile["agent_status"] == "offline", "lowercase local claim appeared as remote heartbeat");
            f.Sessions = new object[] { new { fingerprint = json.Serialize(new { SourceID = id, source_id = new string('f', 32) }), state = "active", last_seen = DateTime.UtcNow.ToString("o") } };
            profile = f.ListedProfile();
            Check((int)profile["agent_count"] == 1, "exact SourceID was not authoritative");
            f.FailSessions = true; profile = f.ListedProfile();
            Check((int)profile["agent_count"] == 0 && (string)profile["agent_status"] == "offline", "failed sessions claimed online");
            f.FailSessions = false; f.Processes[0].Exited = true; profile = f.ListedProfile();
            Check((string)profile["status"] == HostReverse.Exited && (string)profile["agent_status"] == "offline", "dead SSH claimed connected");
            f.Request(new { id = 1, op = "reverseConnect", profile_id = id }, true);
            f.Request(new { id = 1, op = "connectionPrepare", profile_id = new string('b', 32), sync_mcp = false }, false);
            f.Request(new { id = 1, op = "connectionSwitch", profile_id = new string('b', 32) }, false);
            f.Request(new { id = 1, op = "connectionRestore" }, false);
            f.Request(new { id = 1, op = "updatePrepare" }, false);
            Check(f.Backend.HasReverseTunnels && !f.Processes[1].Exited, "context guard killed owned tunnel");
            f.Request(new { id = 1, op = "reverseDisconnect", profile_id = id }, true);
            Check(!f.Backend.HasReverseTunnels && f.Processes[1].Stops == 1, "disconnect ownership failure");
            Check(f.Calls.All(c => c.StartsWith("GET ", StringComparison.Ordinal)), "reverse POST");
        }
        foreach (string change in new[] { "cancel", "context", "digest", "destination" }) using (var f = new Fixture()) {
            string id = f.Add(), path = Path.Combine(f.Root, "export.js"), ticket = f.Prepare(id, path);
            if (change == "cancel") f.Request(new { id = 1, op = "cancelReview" }, true);
            if (change == "context") Set(f.Backend, "connectionName", "changed");
            if (change == "digest") File.AppendAllText(Path.Combine(f.Bundle, "remote_bootstrap.js"), "changed");
            if (change == "destination") File.WriteAllText(path, "existing destination");
            f.Request(new { id = 1, op = "reverseExportConfirm", ticket = ticket }, false);
            f.Request(new { id = 1, op = "reverseExportConfirm", ticket = ticket }, false);
            Check(change == "destination" ? File.ReadAllText(path) == "existing destination" : !File.Exists(path), "failed export wrote destination");
        }
        foreach (string invalid in new[] { "null", "12", "\"invalid\"" }) using (var f = new Fixture()) {
            string id = f.Add();
            string catalog = Path.Combine(f.Root, "catalog", "host-reverse.json");
            string original = File.ReadAllText(catalog);
            File.WriteAllText(catalog, original.Replace("\"logEncoding\":\"auto\"", "\"logEncoding\":" + invalid));
            f.Request(new { id = 1, op = "reverseList" }, false);
            f.Request(new { id = 1, op = "reverseExportPrepare", profile_id = id, path = Path.Combine(f.Root, "invalid.js") }, false);
            Check(!File.Exists(Path.Combine(f.Root, "invalid.js")), "invalid encoding exported");
        }
        using (var f = new Fixture()) {
            string id = f.Add();
            Check((string)f.ListedProfile()["log_encoding"] == "auto", "missing default encoding");
            f.Request(new { id = 1, op = "reverseEncodingSave", profile_id = id, log_encoding = "gb18030" }, true);
            Check((string)f.ListedProfile()["log_encoding"] == "gb18030", "encoding was not saved");
            string path = Path.Combine(f.Root, "export.js"), ticket = f.Prepare(id, path);
            var confirmed = Map(f.Request(new { id = 1, op = "reverseExportConfirm", ticket = ticket }, true)["data"]);
            Check(confirmed.Count == 3 && (bool)confirmed["exported"] && (string)confirmed["path"] == path && (string)Map(confirmed["preview"])["path"] == path, "export result lost path or preview");
            Check(File.Exists(path), "export not written");
            Check(File.ReadAllText(path).Contains("\"log_encoding\":\"gb18030\""), "encoding missing from embedded script");
            f.Request(new { id = 1, op = "reverseExportConfirm", ticket = ticket }, false);
            f.Selection = Path.Combine(f.Root, "chosen.js");
            f.Request(new { id = 1, op = "reverseChoosePath", kind = "export" }, true);
            Check(f.Dialogs == 1 && !File.Exists(f.Selection), "selector wrote a file");
            f.Request(new { id = 1, op = "reverseConnect", profile_id = id }, true);
            f.Backend.Dispose();
            Check(f.Processes[0].Stops == 1 && f.Processes[0].Disposals == 1, "dispose did not release owned SSH");
            f.Backend.Dispose();
            Check(f.Processes[0].Stops == 1, "dispose repeated effects");
        }
    }
}
}
