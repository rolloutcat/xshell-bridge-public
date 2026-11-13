using System;
using System.IO;
using System.Linq;
using System.Text;
using System.Diagnostics;
using System.Security.Cryptography;
using System.Web.Script.Serialization;

namespace XshellDesktop {
/// <summary>Standalone Windows tests. Uses temporary fixtures and fake SSH only.</summary>
public static class HostReverseTests {
    public static int Main() {
        try { RunAll(); Console.WriteLine("HostReverse tests passed."); return 0; }
        catch (Exception error) { Console.Error.WriteLine(error); return 1; }
    }
    sealed class Fake : IReverseProcess {
        public bool Dead, Stopped, Disposed;
        public bool HasExited { get { return Dead; } }
        public int ExitCode { get { return 17; } }
        public bool WaitForExit(int milliseconds) { return Dead; }
        public void Stop() { Stopped = true; Dead = true; }
        public void Dispose() { Disposed = true; }
    }
    static void Check(bool value) { if (!value) throw new Exception("Assertion failed"); }
    static void Reject(Action action) {
        bool rejected = false;
        try { action(); } catch (InvalidOperationException) { rejected = true; }
        Check(rejected);
    }
    public static void RunAll() {
        HostReverseLifecycleTests.RunAll();
        string root = Path.Combine(Path.GetTempPath(), "reverse-" + Guid.NewGuid().ToString("N"));
        Directory.CreateDirectory(root);
        try {
            string bundle = Path.Combine(root, "bundle"), runtime = Path.Combine(root, "runtime");
            Directory.CreateDirectory(bundle); Directory.CreateDirectory(runtime);
            string master = new string('M', 40), client = new string('C', 40), admin = new string('A', 40);
            File.WriteAllText(Path.Combine(runtime, "agent.json"), "{\"endpoint\":\"http://127.0.0.1:18888\",\"token\":\"" + master + "\"}");
            File.WriteAllText(Path.Combine(runtime, "client.json"), "{\"endpoint\":\"http://127.0.0.1:18888\",\"token\":\"" + client + "\"}");
            File.WriteAllText(Path.Combine(runtime, "admin.json"), admin);
            File.WriteAllText(Path.Combine(bundle, "xshell_agent.js"), "// trusted agent\nvar agentFixture = 1;\n", Encoding.ASCII);
            File.WriteAllText(Path.Combine(bundle, "remote_bootstrap.js"), "// trusted bootstrap\nvar bootstrapFixture = 1;\n", Encoding.ASCII);
            File.WriteAllText(Path.Combine(bundle, "log_parser.js"), "// test parser\n");
            var helperFixture = new byte[2048]; new Random(42).NextBytes(helperFixture);
            File.WriteAllBytes(Path.Combine(bundle, "XshLogCapture.exe"), helperFixture);
            string key = Path.Combine(root, "private key"), known = Path.Combine(root, "known hosts");
            File.WriteAllText(key, "DO NOT READ KEY CONTENTS"); File.WriteAllText(known, "fixture");
            Fake fake = null; ProcessStartInfo start = null;
            using (var host = new HostReverse(Path.Combine(root, "catalog"), bundle, runtime, delegate(ProcessStartInfo p) { start = p; fake = new Fake(); return fake; })) {
                Reject(delegate { host.Add("VM", "vm.example", 22, "operator", key, known, 18889, "test", "window_auto"); });
                Func<string> add = delegate { return host.Add("VM", "vm.example", 22, "operator", key, known, 18889, "test", "manual_terminal"); };
                string id = add(), second = add(); Check(id != second && id.Length == 32);
                string catalogPath = Path.Combine(root, "catalog", "host-reverse.json");
                string catalogBefore = File.ReadAllText(catalogPath);
                int modeAt = catalogBefore.IndexOf("manual_terminal", StringComparison.Ordinal);
                Check(modeAt >= 0);
                File.WriteAllText(catalogPath, catalogBefore.Substring(0, modeAt) + "window_auto" + catalogBefore.Substring(modeAt + "manual_terminal".Length));
                Reject(delegate { host.Connect(id); });
                Check(fake == null);
                var migrated = host.PrepareExport(id, Path.Combine(root, "migrated.js"));
                Check(migrated.PublicPreview().mode == "manual_terminal" && fake == null);
                foreach (string environment in new[] { "", "   ", "\t " }) {
                    string defaulted = host.Add("Default environment", "vm.example", 22, "operator", key, known, 18890, environment, "manual_terminal");
                    Check(host.Get(defaulted).environment == "Default environment");
                    Check(host.PrepareExport(defaulted, Path.Combine(root, Guid.NewGuid().ToString("N") + ".js")).PublicPreview().environment == "Default environment");
                    host.Remove(defaulted);
                }
                Reject(delegate { host.Add(" ", "vm.example", 22, "operator", key, known, 18890, "", "manual_terminal"); });
                Reject(delegate { host.Add("VM", "vm.example", 22, "operator", key, known, 18890, "bad\nenvironment", "manual_terminal"); });
                Reject(delegate { host.Add("VM", "vm.example", 22, "operator", key + "\"", known, 18890, "test", "manual_terminal"); });
                Reject(delegate { host.Add("VM", "vm.example", 22, "operator", key, known + "\"", 18890, "test", "manual_terminal"); });
                Reject(delegate { host.Add("VM", "-oProxyCommand=x", 22, "operator", key, known, 18889, "", "manual_terminal"); });
                Reject(delegate { host.Add("VM", "vm", 22, "bad user", key, known, 80, "", "manual_terminal"); });
                Reject(delegate { host.Add("VM", "vm", 22, "user", key + "missing", known, 18889, "", "manual_terminal"); });
                Check(host.Connect(id).status == "awaiting_agent");
                Check(!start.UseShellExecute && start.CreateNoWindow && start.RedirectStandardError);
                Check(start.FileName.EndsWith(@"System32\OpenSSH\ssh.exe", StringComparison.OrdinalIgnoreCase));
                foreach (string argument in new[] { "-F", "NUL", "-N", "-T", "BatchMode=yes", "IdentitiesOnly=yes", "StrictHostKeyChecking=yes", "GlobalKnownHostsFile=NUL", "UserKnownHostsFile=\"" + known.Replace('\\', '/') + "\"", "ExitOnForwardFailure=yes", "ServerAliveInterval=15", "ServerAliveCountMax=3", "ConnectTimeout=10", "127.0.0.1:18889:127.0.0.1:18888", "operator@vm.example", key }) Check(start.Arguments.Contains(HostReverse.Quote(argument)));
                Check(start.Arguments.Contains(HostReverse.Quote("UserKnownHostsFile=\"" + known.Replace('\\', '/') + "\"")));
                Check(!start.Arguments.Contains(HostReverse.Quote("UserKnownHostsFile=" + known)));
                Reject(delegate { host.Remove(id); });
                host.Disconnect(second); Check(!fake.Stopped);
                host.Disconnect(id); Check(fake.Stopped && fake.Disposed);
                host.Connect(id); fake.Dead = true; Check(host.Get(id).status == "exited");
                host.Connect(id); Check(host.Get(id).status == "awaiting_agent");
                string output = Path.Combine(root, "remote.js");
                var plan = host.PrepareExport(id, output);
                string preview = new JavaScriptSerializer().Serialize(plan.PublicPreview());
                Check(!preview.Contains(master) && !preview.Contains(client));
                Check(!File.Exists(output)); host.ConfirmExport(plan);
                string script = File.ReadAllText(output);
                Check(!script.Contains(master) && !script.Contains(client) && !script.Contains(admin));
                Check(script.Contains("helper_base64") && script.Contains("helper_sha256") && script.Contains("helper_size"));
                string expected;
                using (var h = new HMACSHA256(Encoding.UTF8.GetBytes(master))) expected = BitConverter.ToString(h.ComputeHash(Encoding.UTF8.GetBytes("xshell-bridge-source-v1:" + id))).Replace("-", "").ToLowerInvariant();
                Check(script.Contains("xsh-agent-v1." + id + "." + expected));
                Reject(delegate { host.ConfirmExport(plan); });
                Reject(delegate { host.PrepareExport(id, output); });
                Reject(delegate { host.PrepareExport(id, Path.Combine(runtime, "new.js")); });
                foreach (string changed in new[] { Path.Combine(runtime, "client.json"), Path.Combine(runtime, "agent.json"), Path.Combine(bundle, "xshell_agent.js"), Path.Combine(bundle, "remote_bootstrap.js"), Path.Combine(bundle, "log_parser.js"), Path.Combine(bundle, "XshLogCapture.exe") }) {
                    byte[] before = File.ReadAllBytes(changed);
                    var stale = host.PrepareExport(id, Path.Combine(root, Guid.NewGuid().ToString("N") + ".js"));
                    File.AppendAllText(changed, " ");
                    Reject(delegate { host.ConfirmExport(stale); }); File.WriteAllBytes(changed, before);
                }
                var collision = host.PrepareExport(id, Path.Combine(root, "collision.js"));
                File.WriteAllText(collision.PublicPreview().path, "keep");
                Reject(delegate { host.ConfirmExport(collision); });
                Check(File.ReadAllText(collision.PublicPreview().path) == "keep");
                var removed = host.PrepareExport(second, Path.Combine(root, "removed.js")); host.Remove(second);
                Reject(delegate { host.ConfirmExport(removed); });
                string catalog = File.ReadAllText(Path.Combine(root, "catalog", "host-reverse.json"));
                Check(!catalog.Contains(master) && !catalog.Contains(client) && !catalog.Contains(admin));
            }
            Check(fake.Stopped && fake.Disposed);
        } finally { Directory.Delete(root, true); }
    }
}
}
