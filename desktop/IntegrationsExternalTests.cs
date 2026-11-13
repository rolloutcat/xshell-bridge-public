using System;
using System.IO;
using System.Linq;
using System.Collections.Generic;
using System.Web.Script.Serialization;
using XshellDesktop;

public static class IntegrationsExternalTests {
    static readonly JavaScriptSerializer J = new JavaScriptSerializer();
    static void Assert(bool ok) { if (!ok) throw new Exception("Assertion failed"); }
    static string Reject(Action action) { try { action(); } catch (InvalidOperationException e) { Assert(!e.Message.Contains("SECRET")); return e.Message; } throw new Exception("Expected rejection"); }
    static object Registration(string command, string[] args) { return new { name = "xshell-bridge", enabled = true, transport = new { type = "stdio", command = command, args = args, env = (object)null, env_vars = (object)null, cwd = (string)null } }; }
    static object HttpRegistration(string url,string variable) {return new{name="xshell-bridge",enabled=true,transport=new{type="streamable_http",url=url,bearer_token_env_var=variable}};}
    sealed class Codex {
        public object Mcp;
        public bool FailList, FailAdd, ForeignOnAdd, BadJson;
        public readonly List<string[]> Calls = new List<string[]>();
        public readonly Dictionary<string,string> Env=new Dictionary<string,string>();
        public string ReadEnv(string name){string v;return Env.TryGetValue(name,out v)?v:null;}
        public void WriteEnv(string name,string value){if(value==null)Env.Remove(name);else Env[name]=value;}
        static Integrations.ExternalResult Ok(object value) { return new Integrations.ExternalResult { ExitCode = 0, Output = J.Serialize(value) }; }
        public Integrations.ExternalResult Run(string[] a) {
            Calls.Add((string[])a.Clone());
            Assert(a[0] == "mcp");
            if (a.SequenceEqual(new[] { "mcp", "list", "--json" })) {
                if (FailList) return new Integrations.ExternalResult { ExitCode = 1, Output = "SECRET" };
                if (BadJson) return new Integrations.ExternalResult { ExitCode = 0, Output = "not JSON SECRET" };
                return Ok(Mcp == null ? new object[0] : new[] { Mcp });
            }
            if (a.SequenceEqual(new[] { "mcp", "get", "xshell-bridge", "--json" })) return Ok(Mcp);
            if (a.SequenceEqual(new[] { "mcp", "remove", "xshell-bridge" })) { Mcp = null; return Ok(null); }
            Assert(a[1]=="add" && a[2]=="xshell-bridge");
            if (ForeignOnAdd) { ForeignOnAdd = false; Mcp = Registration("C:\\unknown.exe", new[] { "foreign" }); return new Integrations.ExternalResult { ExitCode = 1, Output = "SECRET" }; }
            if (FailAdd) { FailAdd = false; return new Integrations.ExternalResult { ExitCode = 1, Output = "SECRET" }; }
            Mcp = a[3]=="--url" ? HttpRegistration(a[4],a[6]) : Registration(a[4], a.Skip(5).ToArray()); return Ok(null);
        }
        public int Writes() { return Calls.Count(a => a[1] == "add" || a[1] == "remove"); }
    }
    static void Apply(Integrations m, string action) { m.Apply(m.Preview(action, "mcp").ticket); }
    static Dictionary<string, object> Row(Integrations m) {
        var status = (Dictionary<string, object>)J.DeserializeObject(J.Serialize(m.Status()));
        return ((object[])status["components"]).Cast<Dictionary<string, object>>().Single(r => (string)r["component"] == "mcp");
    }
    public static int Main() {
        string root = Path.Combine(Path.GetTempPath(), "external-" + Guid.NewGuid().ToString("N"));
        try {
            string b = Path.Combine(root, "bundle"), old = Path.Combine(root, "old bundle"), d = Path.Combine(root, "managed"), h = Path.Combine(root, "home"), c = Path.Combine(root, "client.json");
            foreach (string dir in new[] { b, old }) { Directory.CreateDirectory(Path.Combine(dir, "skills", "xshctl", "references")); File.WriteAllText(Path.Combine(dir, "xshctl.exe"), "fixture " + dir); File.WriteAllText(Path.Combine(dir, "skills", "xshctl", "SKILL.md"), "skill"); File.WriteAllText(Path.Combine(dir, "skills", "xshctl", "references", "commands.md"), "commands"); File.WriteAllText(Path.Combine(dir, "skills", "xshctl", "references", "inspection.md"), "inspection"); }
            Directory.CreateDirectory(h); File.WriteAllText(c, J.Serialize(new { endpoint = "http://127.0.0.1/", token = new string('s', 32) }));
            var fake = new Codex(); var m = new Integrations(b, d, h, c, null, fake.Run, fake.ReadEnv, fake.WriteEnv);
            string exe = Path.Combine(b, "xshctl.exe"), manifest = Path.Combine(d, "ownership-mcp.json");
            fake.Mcp = Registration(exe, new[] { "--config", c, "mcp" });
            foreach (string action in new[] { "install", "update", "uninstall" }) Reject(delegate { m.Preview(action, "mcp"); }); Assert(fake.Writes() == 0);
            fake.Mcp = null; fake.FailList = true; Reject(delegate { m.Preview("install", "mcp"); });
            var unreachable = Row(m);
            Assert((string)unreachable["status"] == "unavailable");
            Assert((string)unreachable["diagnostic"] == "已检测到执行入口，但 MCP 查询失败；请检查 Codex 是否支持 mcp 命令，然后重试。");
            Assert((string)((Dictionary<string, object>)unreachable["detection"])["status"] == "injected");
            fake.FailList = false;
            fake.BadJson = true; Reject(delegate { m.Preview("install", "mcp"); }); fake.BadJson = false;
            var p = m.Preview("install", "mcp"); File.AppendAllText(exe, " changed"); Reject(delegate { m.Apply(p.ticket); }); Assert(fake.Writes() == 0);
            p = m.Preview("install", "mcp"); File.AppendAllText(c, " "); Reject(delegate { m.Apply(p.ticket); }); Assert(fake.Writes() == 0);
            p = m.Preview("install", "mcp"); fake.Mcp = Registration("C:\\unknown.exe", new[] { "foreign" }); Reject(delegate { m.Apply(p.ticket); }); Assert(fake.Writes() == 0); fake.Mcp = null;
            fake.FailAdd = true; Reject(delegate { Apply(m, "install"); }); Assert(fake.Mcp == null && !File.Exists(manifest));
            Apply(m, "install"); object saved = fake.Mcp; string owner = File.ReadAllText(manifest);
            fake.Mcp = Registration(exe, new[] { "different" }); Reject(delegate { m.Preview("update", "mcp"); }); Reject(delegate { m.Preview("uninstall", "mcp"); }); fake.Mcp = saved;
            fake.FailAdd = true; Reject(delegate { Apply(m, "update"); }); Assert(J.Serialize(fake.Mcp) == J.Serialize(saved)); Assert(File.ReadAllText(manifest) == owner);
            string otherConfig = Path.Combine(root, "other.json"); File.Copy(c, otherConfig);
            var other = new Integrations(b, d, h, otherConfig, null, fake.Run, fake.ReadEnv, fake.WriteEnv); Assert((bool)Row(other)["needs_update"]); Apply(other, "update"); Apply(m, "update");
            Apply(m, "uninstall");
            // Seed genuine ownership using the old package, then remove its executable.
            string oldExe = Path.Combine(old, "xshctl.exe");
            using(var sha=System.Security.Cryptography.SHA256.Create()) {
                Func<string,string> hash=v=>BitConverter.ToString(sha.ComputeHash(System.Text.Encoding.UTF8.GetBytes(v))).Replace("-","").ToLowerInvariant();
                fake.Mcp=Registration(oldExe,new[]{"--config",c,"mcp"});
                File.WriteAllText(manifest,J.Serialize(new Integrations.Ownership{component="mcp",version="0.8.6",command=oldExe,args=new[]{"--config",c,"mcp"},config_identity=hash(c),hashes=new Dictionary<string,string>()}));
            }
            File.Delete(oldExe);
            int before = fake.Writes(); Assert((string)Row(m)["status"] == "installed" && (bool)Row(m)["needs_update"]); Assert(fake.Writes() == before);
            p = m.Preview("update", "mcp"); Assert(fake.Writes() == before && !File.Exists(oldExe)); Assert(p.commands.Any(line => line.Contains("--url")));
            m.Apply(p.ticket); Reject(delegate { m.Apply(p.ticket); });
            var repaired = J.Deserialize<Integrations.Ownership>(File.ReadAllText(manifest));
            Assert(J.Serialize(fake.Mcp)==J.Serialize(HttpRegistration("http://127.0.0.1/mcp",repaired.token_env)));
            Assert(repaired.url=="http://127.0.0.1/mcp" && fake.ReadEnv(repaired.token_env)==new string('s',32));Assert(!(bool)Row(m)["needs_update"]);
            fake.WriteEnv(repaired.token_env,"foreign");Reject(delegate{m.Preview("update","mcp");});fake.WriteEnv(repaired.token_env,new string('s',32));
            saved = fake.Mcp; fake.Mcp = Registration(Path.Combine(root, "unknown", "xshctl.exe"), new[] { "--config", c, "mcp" }); before = fake.Writes();
            Reject(delegate { m.Preview("update", "mcp"); }); Reject(delegate { m.Preview("uninstall", "mcp"); }); Assert(fake.Writes() == before); fake.Mcp = saved;
            // Failed update must never overwrite an unrelated concurrent external registration.
            fake.ForeignOnAdd = true; before = fake.Writes(); string message = Reject(delegate { Apply(m, "update"); }); Assert(message.Contains("恢复不完整")); Assert(fake.Writes() == before + 2); Assert(J.Serialize(fake.Mcp).Contains("unknown.exe")); Assert(File.ReadAllText(manifest).Contains("token_env"));
            string statusText = J.Serialize(m.Status()); Assert(!statusText.Contains(new string('s', 32)) && !statusText.Contains("SECRET"));
            Assert(!File.Exists(Path.Combine(d, "ownership-cli.json")) && !File.Exists(Path.Combine(d, "ownership-plugin.json")));
            Console.WriteLine("External MCP integration tests passed"); return 0;
        } finally { if (Directory.Exists(root)) Directory.Delete(root, true); }
    }
}
