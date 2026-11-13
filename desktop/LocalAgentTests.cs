using System;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using System.Web.Script.Serialization;

namespace XshellDesktop {
internal static class LocalAgentTests {
    static void Check(bool ok, string message) { if (!ok) throw new Exception(message); }
    static void Reject(Action action) { bool rejected = false; try { action(); } catch { rejected = true; } Check(rejected, "operation should fail closed"); }
    static void Main(string[] args) {
        try { Run(); if (args.Length != 0) Reject(() => HostReverse.Safe(Path.Combine(args[0], "child"))); Console.WriteLine("PASS: local agent isolated lifecycle"); }
        catch (Exception e) { Console.Error.WriteLine(e.ToString()); Environment.ExitCode = 1; }
    }
    static void Run() {
        string root = Path.Combine(Path.GetTempPath(), "local-agent-fixture-" + Guid.NewGuid().ToString("N"));
        string runtime = Path.Combine(root, "runtime"), bundle = Path.Combine(root, "bundle");
        Directory.CreateDirectory(runtime); Directory.CreateDirectory(bundle);
        var json = new JavaScriptSerializer();
        string agentToken = new string('a', 48), clientToken = new string('c', 48);
        File.WriteAllText(Path.Combine(runtime, "agent.json"), json.Serialize(new { endpoint = "http://127.0.0.1:17890", token = agentToken }));
        File.WriteAllText(Path.Combine(runtime, "client.json"), json.Serialize(new { endpoint = "http://127.0.0.1:17890", token = clientToken }));
        foreach (string name in new[] { "xshell_agent.js", "remote_bootstrap.js", "log_parser.js" }) File.WriteAllText(Path.Combine(bundle, name), "// fixture " + name);
        File.WriteAllBytes(Path.Combine(bundle, "XshLogCapture.exe"), Enumerable.Repeat((byte)42, 2048).ToArray());
        string legacy = Path.Combine(runtime, "xshell_agent.js"); File.WriteAllText(legacy, "old user script");
        using (var reverse = new HostReverse(Path.Combine(root, "catalog"), bundle, runtime)) {
            var manager = new LocalAgent(runtime, reverse, "0.8.5");
            var initial = manager.Status(); Check(initial.status == "absent" && initial.legacy_exists, "initial status");
            Check(new LocalAgent(runtime + Path.DirectorySeparatorChar, reverse, "0.8.5").Status().path == initial.path, "runtime trailing separator changed managed destination");
            var plan = manager.Prepare("hospital", "gbk");
            Check(!Directory.Exists(Path.Combine(root, "local-agent")), "preview wrote files");
            Check(!json.Serialize(plan.PublicPreview()).Contains(agentToken), "preview leaked token");
            Directory.CreateDirectory(Path.Combine(root, "local-agent")); File.WriteAllText(initial.path, "unknown after preview");
            Reject(() => manager.Confirm(plan)); Check(File.ReadAllText(initial.path) == "unknown after preview", "unknown target overwritten"); File.Delete(initial.path); Directory.Delete(Path.Combine(root, "local-agent"));
            bool firstWriteReached = false;
            var firstFailure = manager.Prepare("hospital", "gbk"); manager.BeforeManifestWrite = () => { firstWriteReached = true; throw new IOException("synthetic first write failure"); };
            Reject(() => manager.Confirm(firstFailure)); manager.BeforeManifestWrite = null;
            Check(firstWriteReached, "first write fault injection was not reached; status=" + manager.Status().status);
            Check(manager.Status().status == "absent" && !File.Exists(initial.path), "first write rollback");
            plan = manager.Prepare("hospital", "gbk");
            manager.Confirm(plan);
            Check(File.ReadAllText(legacy)=="old user script", "legacy script changed");
            Reject(() => manager.Confirm(plan));
            var current = manager.Status(); Check(current.status == "current" && current.source_id == plan.PublicPreview().source_id, "current/source id: " + current.status);
            string script = File.ReadAllText(current.path);
            string mac; using (var h = new HMACSHA256(Encoding.UTF8.GetBytes(agentToken))) mac = BitConverter.ToString(h.ComputeHash(Encoding.UTF8.GetBytes("xshell-bridge-source-v1:" + current.source_id))).Replace("-", "").ToLowerInvariant();
            Check(script.Contains("xsh-agent-v1." + current.source_id + "." + mac) && script.Contains("http://127.0.0.1:17890") && script.Contains("manual_terminal"), "local protocol");
            Check(!script.Contains(agentToken) && !script.Contains(clientToken), "master token leaked");
            Check(script.Contains(Convert.ToBase64String(Enumerable.Repeat((byte)42,2048).ToArray())), "helper missing");
            File.AppendAllText(Path.Combine(bundle, "xshell_agent.js"), "// changed same version");
            Check(manager.Status().status == "update_available", "source change not detected");
            var update = manager.Prepare("hospital", "gbk"); manager.Confirm(update);
            Check(manager.Status().status == "current" && manager.Status().source_id == current.source_id, "update identity");
            var newer = new LocalAgent(runtime, reverse, "0.8.6"); Check(newer.Status().status == "update_available", "version change not detected");
            var settings = manager.Prepare("new group", "utf-8"); manager.Confirm(settings);
            Check(manager.Status().environment == "new group" && manager.Status().log_encoding == "utf-8", "settings not installed");
            var peer = new LocalAgent(runtime, reverse, "0.8.5");
            var first = manager.Prepare("first", "auto"); var second = peer.Prepare("second", "auto");
            using (var entered = new ManualResetEventSlim()) using (var release = new ManualResetEventSlim()) {
                manager.BeforeManifestWrite = () => { entered.Set(); if (!release.Wait(5000)) throw new Exception("fixture timeout"); };
                var one = Task.Run(() => manager.Confirm(first)); Check(entered.Wait(5000), "first confirmation did not start");
                var two = Task.Run(() => { try { peer.Confirm(second); return false; } catch { return true; } });
                release.Set(); Task.WaitAll(one, two); manager.BeforeManifestWrite = null;
                Check(two.Result && manager.Status().environment == "first", "concurrent stale confirmation accepted");
            }
            string manifest = Path.Combine(Path.GetDirectoryName(current.path), "manifest.json");
            var lockedWrite = manager.Prepare("locked write", "auto");
            manager.BeforeManifestWrite = () => {
                Reject(() => File.AppendAllText(current.path, "unexpected concurrent edit"));
                Reject(() => File.AppendAllText(manifest, "unexpected manifest edit"));
                Reject(() => Directory.Move(Path.GetDirectoryName(current.path), Path.Combine(root, "moved-agent")));
            };
            manager.Confirm(lockedWrite); manager.BeforeManifestWrite = null;
            Check(manager.Status().status == "current", "locked write failed");
            byte[] beforeScript = File.ReadAllBytes(current.path), beforeManifest = File.ReadAllBytes(manifest);
            var beforeManifestAcl = File.GetAccessControl(manifest);
            var editedDuringWrite = manager.Prepare("edit during write", "auto"); manager.BeforeScriptCommit = () => File.AppendAllText(current.path,"// user edit during staging");
            Reject(() => manager.Confirm(editedDuringWrite)); manager.BeforeScriptCommit = null;
            Check(manager.Status().status == "modified" && File.ReadAllText(current.path).Contains("// user edit during staging"), "concurrent edit overwritten"); File.WriteAllBytes(current.path,beforeScript);
            var failure = manager.Prepare("after failure", "gb18030"); manager.BeforeManifestWrite = () => { throw new IOException("synthetic"); };
            Reject(() => manager.Confirm(failure)); manager.BeforeManifestWrite = null;
            Check(File.ReadAllBytes(current.path).SequenceEqual(beforeScript) && File.ReadAllBytes(manifest).SequenceEqual(beforeManifest) && manager.Status().status == "current", "write failure rollback");
            Reject(() => manager.Confirm(failure));
            string changingSource=Path.Combine(bundle,"log_parser.js"), originalSource=File.ReadAllText(changingSource);
            var changing = manager.Prepare("change during write", "auto"); manager.BeforeManifestWrite = () => File.AppendAllText(changingSource,"// changed during write");
            Reject(() => manager.Confirm(changing)); manager.BeforeManifestWrite = null;
            Check(File.ReadAllBytes(current.path).SequenceEqual(beforeScript) && File.ReadAllBytes(manifest).SequenceEqual(beforeManifest), "source change rollback"); File.WriteAllText(changingSource,originalSource);
            var collision = manager.Prepare("conflict", "auto"); manager.BeforeManifestWrite = () => File.WriteAllText(manifest, "user change");
            Reject(() => manager.Confirm(collision)); manager.BeforeManifestWrite = null;
            Check(manager.Status().status == "current" && File.ReadAllBytes(manifest).SequenceEqual(beforeManifest) && File.ReadAllBytes(current.path).SequenceEqual(beforeScript), "locked manifest edit must be refused and rolled back");
            File.WriteAllBytes(current.path,beforeScript); File.WriteAllBytes(manifest,beforeManifest);
            var staleSource = manager.Prepare("new group", "utf-8"); File.AppendAllText(Path.Combine(bundle, "log_parser.js"), "// changed");
            Reject(() => manager.Confirm(staleSource));
            var staleManifest = manager.Prepare("new group", "utf-8"); File.AppendAllText(manifest, " ");
            Reject(() => manager.Confirm(staleManifest)); File.WriteAllBytes(manifest, beforeManifest);
            var staleConfig = manager.Prepare("new group", "utf-8"); string client = Path.Combine(runtime, "client.json"), originalConfig = File.ReadAllText(client);
            File.WriteAllText(client, json.Serialize(new { endpoint = "http://127.0.0.1:17890", token = new string('d', 48) })); Reject(() => manager.Confirm(staleConfig)); File.WriteAllText(client, originalConfig);
            File.Delete(manifest); Check(manager.Status().status == "conflict", "missing manifest accepted"); Reject(() => manager.Prepare("hospital", "auto")); HostReverse.WritePrivate(manifest, beforeManifest, false); File.SetAccessControl(manifest, beforeManifestAcl);
            Check(manager.Status().status != "conflict", "restored fixture must preserve manifest ownership and ACL");
            File.WriteAllText(manifest, "broken"); Check(manager.Status().status == "conflict", "broken manifest accepted"); File.WriteAllBytes(manifest, beforeManifest);
            string sourcePath=Path.Combine(bundle,"remote_bootstrap.js"), source=File.ReadAllText(sourcePath); File.Delete(sourcePath);
            Reject(() => manager.Prepare("hospital", "auto")); File.WriteAllText(sourcePath,source);
            var stale = manager.Prepare("hospital", "utf-8"); File.AppendAllText(current.path, "// user edit");
            Reject(() => manager.Confirm(stale)); Check(manager.Status().status == "modified", "modified file not detected");
            Reject(() => manager.Prepare("hospital", "gbk"));
        }
        string unknownRoot=Path.Combine(root,"unknown"), unknownRuntime=Path.Combine(unknownRoot,"runtime"); Directory.CreateDirectory(unknownRuntime);
        File.Copy(Path.Combine(runtime,"agent.json"),Path.Combine(unknownRuntime,"agent.json")); File.Copy(Path.Combine(runtime,"client.json"),Path.Combine(unknownRuntime,"client.json"));
        Directory.CreateDirectory(Path.Combine(unknownRoot,"local-agent")); string unknown=Path.Combine(unknownRoot,"local-agent","xshell_agent.js"); File.WriteAllText(unknown,"user file");
        using(var reverse=new HostReverse(Path.Combine(unknownRoot,"catalog"),bundle,unknownRuntime)) { var manager=new LocalAgent(unknownRuntime,reverse,"0.8.5"); Check(manager.Status().status=="conflict", "unknown file accepted"); Reject(() => manager.Prepare("local","auto")); Check(File.ReadAllText(unknown)=="user file", "unknown file modified"); }
    }
}
}
