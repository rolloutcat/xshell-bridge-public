using System;
using System.IO;
using System.Collections.Generic;
using System.Web.Script.Serialization;
using XshellDesktop;

public static class IntegrationsTests {
    static readonly JavaScriptSerializer J = new JavaScriptSerializer();
    static void Assert(bool value) { if (!value) throw new Exception("Assertion failed"); }
    static void Reject(Action action) { try { action(); } catch (InvalidOperationException) { return; } throw new Exception("Expected rejection"); }
    static Dictionary<string, object> Map(object value) { return (Dictionary<string, object>)J.DeserializeObject(J.Serialize(value)); }
    static void Config(string path, char token) { File.WriteAllText(path, J.Serialize(new { endpoint = "http://127.0.0.1:18765/", token = new string(token, 32) })); }
    public static int Main() {
        string root = Path.Combine(Path.GetTempPath(), "integrations-" + Guid.NewGuid().ToString("N"));
        try {
            string b = Path.Combine(root, "bundle"), d = Path.Combine(root, "managed"), h = Path.Combine(root, "home"), c = Path.Combine(root, "client.json");
            Directory.CreateDirectory(Path.Combine(b, "skills", "xshctl")); Directory.CreateDirectory(h);
            string exe = Path.Combine(b, "xshctl.exe"), source = Path.Combine(b, "skills", "xshctl", "SKILL.md");
            File.WriteAllText(exe, "fixture; never executed"); File.WriteAllText(source, "skill"); Config(c, 'x');
            Directory.CreateDirectory(Path.Combine(b, "skills", "xshctl", "references"));
            File.WriteAllText(Path.Combine(b, "skills", "xshctl", "references", "commands.md"), "commands");
            File.WriteAllText(Path.Combine(b, "skills", "xshctl", "references", "inspection.md"), "inspection");
            var assembly = typeof(Integrations).Assembly;
            string resourceVersion = null;
            foreach (string name in assembly.GetManifestResourceNames()) {
                if (name == "VERSION" || name.EndsWith(".VERSION", StringComparison.OrdinalIgnoreCase)) {
                    using (var reader = new StreamReader(assembly.GetManifestResourceStream(name))) resourceVersion = reader.ReadToEnd().Trim();
                }
            }
            Assert(!String.IsNullOrWhiteSpace(resourceVersion) && resourceVersion != "unknown" && resourceVersion == Integrations.Version);
            DateTime now = DateTime.UtcNow;
            int mutations = 0;
            var m = new Integrations(b, d, h, c, delegate { return now; }, delegate(string[] args) {
                if (String.Join("|", args) == "mcp|list|--json") return new Integrations.ExternalResult { ExitCode = 0, Output = "[]" };
                mutations++; throw new Exception("Unexpected external operation");
            });
            Reject(delegate { Integrations.Quote("bad\npath"); });
            Assert(Integrations.Quote("a b\\") == "\"a b\\\\\"");
            foreach (string component in new[] { "cli", "plugin", "other" }) {
                foreach (string action in new[] { "install", "update", "uninstall" }) Reject(delegate { m.Preview(action, component); });
                Reject(delegate { m.Manual(component); });
            }
            Reject(delegate { m.Preview("other", "skill"); }); Reject(delegate { m.Apply("invented"); });
            var status = Map(m.Status());
            var builtin = (Dictionary<string, object>)status["builtin_cli"];
            Assert((string)builtin["path"] == exe && (string)builtin["version"] == Integrations.Version && (string)builtin["status"] == "bundled");
            var rows = (object[])status["components"];
            Assert(rows.Length == 2);
            Assert((string)((Dictionary<string, object>)rows[0])["component"] == "mcp");
            Assert((string)((Dictionary<string, object>)rows[1])["component"] == "skill");
            var p = m.Preview("install", "skill"); File.AppendAllText(source, " changed"); Reject(delegate { m.Apply(p.ticket); }); Reject(delegate { m.Apply(p.ticket); });
            p = m.Preview("install", "skill"); Config(c, 'y'); Reject(delegate { m.Apply(p.ticket); });
            p = m.Preview("install", "skill"); m.Invalidate(); Reject(delegate { m.Apply(p.ticket); });
            p = m.Preview("install", "skill"); var newer = m.Preview("install", "skill"); Reject(delegate { m.Apply(p.ticket); });
            now = now.AddMinutes(2); Reject(delegate { m.Apply(newer.ticket); });
            string skill = Path.Combine(h, ".agents", "skills", "xshctl", "SKILL.md");
            Directory.CreateDirectory(Path.GetDirectoryName(skill)); File.WriteAllText(skill, "unowned");
            Reject(delegate { m.Preview("install", "skill"); }); Reject(delegate { m.Preview("update", "skill"); }); Reject(delegate { m.Preview("uninstall", "skill"); }); Assert(File.ReadAllText(skill) == "unowned"); File.Delete(skill);
            File.WriteAllText(c, "{}"); Reject(delegate { m.Preview("install", "skill"); }); Config(c, 'y');
            p = m.Preview("install", "skill"); m.Apply(p.ticket); Reject(delegate { m.Apply(p.ticket); });
            Assert(File.ReadAllText(skill) == File.ReadAllText(source));
            string manifest = Path.Combine(d, "ownership-skill.json");
            var owner = J.Deserialize<Integrations.Ownership>(File.ReadAllText(manifest)); Assert(owner.component == "skill" && owner.hashes.ContainsKey(skill));
            p = m.Preview("update", "skill"); File.AppendAllText(manifest, " "); Reject(delegate { m.Apply(p.ticket); });
            File.AppendAllText(skill, " user edit"); Reject(delegate { m.Preview("update", "skill"); }); Reject(delegate { m.Preview("uninstall", "skill"); }); Assert(File.ReadAllText(skill).EndsWith(" user edit"));
            File.WriteAllText(skill, File.ReadAllText(source));
            File.WriteAllText(c, "{}"); p = m.Preview("uninstall", "skill"); m.Apply(p.ticket); Assert(!File.Exists(skill) && !File.Exists(manifest)); Config(c, 'y');
            Assert(File.ReadAllText(exe) == "fixture; never executed"); Assert(!File.Exists(Path.Combine(d, "ownership-cli.json"))); Assert(!Directory.Exists(Path.Combine(d, "cli"))); Assert(mutations == 0);
            var backend = new Backend(delegate(string method, string path, object body) { throw new Exception("Server must not be contacted"); }, true, "Local", m);
            try {
                var response = Map(backend.Handle("{\"id\":1,\"op\":\"integrationStatus\"}").Result); Assert((bool)response["ok"]);
                response = Map(backend.Handle("{\"id\":2,\"op\":\"prepare\",\"action\":\"activate\",\"target\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\"}").Result); Assert(!(bool)response["ok"]);
            } finally { backend.Dispose(); }
            Console.WriteLine("Integration file lifecycle tests passed"); return 0;
        } finally { if (Directory.Exists(root)) Directory.Delete(root, true); }
    }
}
