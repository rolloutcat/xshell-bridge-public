using System;
using System.IO;
using System.Linq;
using System.Collections.Generic;
using System.Web.Script.Serialization;
using XshellDesktop;

public static class SkillFilesTests {
    static readonly JavaScriptSerializer J = new JavaScriptSerializer();
    static void Check(bool value, string message) { if (!value) throw new Exception(message); }
    static void Reject(Action action) { try { action(); } catch (InvalidOperationException) { return; } throw new Exception("Expected rejection"); }
    static Dictionary<string, object> Map(object o) { return (Dictionary<string, object>)J.DeserializeObject(J.Serialize(o)); }
    static Dictionary<string, object> Row(Integrations m) { return ((object[])Map(m.Status())["components"]).Cast<Dictionary<string, object>>().Single(x => (string)x["component"] == "skill"); }
    public static int Main() {
        string root = Path.Combine(Path.GetTempPath(), "skill-files-" + Guid.NewGuid().ToString("N"));
        try {
            string b = Path.Combine(root, "bundle"), h = Path.Combine(root, "home"), d = Path.Combine(root, "managed"), c = Path.Combine(root, "config.json");
            string src = Path.Combine(b, "skills", "xshctl"), dst = Path.Combine(h, ".agents", "skills", "xshctl");
            Directory.CreateDirectory(Path.Combine(src, "references")); Directory.CreateDirectory(h);
            File.WriteAllText(Path.Combine(src, "SKILL.md"), "skill");
            File.WriteAllText(Path.Combine(src, "references", "commands.md"), "commands");
            File.WriteAllText(Path.Combine(src, "references", "inspection.md"), "inspection");
            File.WriteAllText(c, J.Serialize(new { endpoint = "http://127.0.0.1/", token = new string('x', 32) }));
            var m = new Integrations(b, d, h, c, null, delegate(string[] a) { throw new Exception("External invocation forbidden"); });
            string commands = Path.Combine(dst, "references", "commands.md"), inspection = Path.Combine(dst, "references", "inspection.md"), manifest = Path.Combine(d, "ownership-skill.json");
            string sourceCommands = Path.Combine(src, "references", "commands.md");
            File.WriteAllBytes(sourceCommands, new byte[] { 0xff }); Reject(delegate { m.Preview("install", "skill"); });
            File.WriteAllBytes(sourceCommands, new byte[] { 0 }); Reject(delegate { m.Preview("install", "skill"); }); File.WriteAllText(sourceCommands, "commands");
            var preview = m.Preview("install", "skill");
            Check(preview.paths.Contains(commands) && preview.paths.Contains(inspection), "preview omitted reference files");
            Check(preview.files.Length == 3 && preview.files.Any(f => f.target == commands && f.content == "commands"), "preview missing full content");
            Check(((object[])Map(m.Manual("skill"))["files"]).Length == 3, "manual omitted files");
            m.Apply(preview.ticket);
            Check(File.ReadAllText(commands) == "commands" && File.ReadAllText(inspection) == "inspection", "references not installed");
            var owner = J.Deserialize<Integrations.Ownership>(File.ReadAllText(manifest));
            Check(owner.hashes.Count == 4, "ownership must cover each file");
            File.AppendAllText(Path.Combine(src, "references", "commands.md"), " v2");
            Check((bool)Row(m)["needs_update"], "same-version content change must be detected");
            preview = m.Preview("update", "skill"); File.AppendAllText(Path.Combine(src, "references", "inspection.md"), " changed"); Reject(delegate { m.Apply(preview.ticket); });
            File.WriteAllText(Path.Combine(src, "references", "inspection.md"), "inspection");
            preview = m.Preview("update", "skill"); File.AppendAllText(commands, " user"); Reject(delegate { m.Apply(preview.ticket); });
            Reject(delegate { m.Preview("update", "skill"); }); Reject(delegate { m.Preview("uninstall", "skill"); });
            File.WriteAllText(commands, "commands");
            // Synthesize the exact historical ownership set without touching unrelated paths.
            owner.hashes.Remove(commands); owner.hashes.Remove(inspection); File.Delete(commands); File.Delete(inspection);
            // Windows path spelling can change case across launches without changing ownership.
            owner.hashes = owner.hashes.ToDictionary(p => p.Key.ToLowerInvariant(), p => p.Value);
            File.WriteAllText(manifest, J.Serialize(owner));
            Check((string)Row(m)["status"] == "installed", "legacy path case should still identify owned files");
            Check((bool)Row(m)["needs_update"], "legacy installation must offer update");
            string main = Path.Combine(dst, "SKILL.md"); File.AppendAllText(main, " user");
            Reject(delegate { m.Preview("update", "skill"); }); Reject(delegate { m.Preview("uninstall", "skill"); }); File.WriteAllText(main, "skill");
            File.WriteAllText(commands, "foreign"); Reject(delegate { m.Preview("update", "skill"); });
            Check(File.ReadAllText(commands) == "foreign", "foreign file overwritten"); File.Delete(commands);
            preview = m.Preview("update", "skill"); File.WriteAllText(inspection, "foreign after preview"); Reject(delegate { m.Apply(preview.ticket); }); File.Delete(inspection);
            m.Apply(m.Preview("update", "skill").ticket);
            Check(File.ReadAllText(commands) == "commands v2" && File.Exists(inspection), "legacy upgrade failed");
            string unrelated = Path.Combine(dst, "references", "user.md"); File.WriteAllText(unrelated, "keep");
            preview = m.Preview("uninstall", "skill"); Check(preview.files.Any(f => f.target == commands && f.content == "commands v2"), "uninstall must preview installed content");
            m.Apply(m.Preview("uninstall", "skill").ticket);
            Check(File.Exists(unrelated) && !File.Exists(commands) && !File.Exists(inspection) && !File.Exists(manifest), "uninstall ownership failure");
            // A legacy uninstall must leave an unowned future reference alone.
            File.Delete(unrelated); m.Apply(m.Preview("install", "skill").ticket);
            owner = J.Deserialize<Integrations.Ownership>(File.ReadAllText(manifest)); owner.hashes.Remove(commands); owner.hashes.Remove(inspection);
            File.Delete(commands); File.Delete(inspection); File.WriteAllText(manifest, J.Serialize(owner)); File.WriteAllText(commands, "foreign");
            m.Apply(m.Preview("uninstall", "skill").ticket); Check(File.ReadAllText(commands) == "foreign", "legacy uninstall removed foreign file"); File.Delete(commands);
            m.Apply(m.Preview("install", "skill").ticket);
            owner = J.Deserialize<Integrations.Ownership>(File.ReadAllText(manifest)); owner.hashes.Remove(commands); owner.hashes.Remove(inspection);
            File.Delete(commands); File.Delete(inspection); File.WriteAllText(manifest, J.Serialize(owner));
            m.Apply(m.Preview("uninstall", "skill").ticket); Check(Directory.Exists(Path.Combine(dst, "references")), "legacy uninstall removed unowned directory");
            // Malformed and external ownership paths cannot authorize deletion.
            m.Apply(m.Preview("install", "skill").ticket); owner = J.Deserialize<Integrations.Ownership>(File.ReadAllText(manifest));
            owner.hashes[Path.Combine(root, "outside")] = new string('0', 64); File.WriteAllText(manifest, J.Serialize(owner)); Reject(delegate { m.Preview("uninstall", "skill"); });
            owner.hashes.Remove(Path.Combine(root, "outside")); owner.hashes[commands] = "invalid"; File.WriteAllText(manifest, J.Serialize(owner)); Reject(delegate { m.Preview("uninstall", "skill"); });
            File.Delete(manifest); // fixture-only cleanup before the rollback scenario
            foreach (string p in new[] { Path.Combine(dst, "SKILL.md"), Path.Combine(dst, ".xshell-bridge-version"), commands, inspection }) File.Delete(p);
            Directory.CreateDirectory(manifest); preview = m.Preview("install", "skill"); Reject(delegate { m.Apply(preview.ticket); });
            Check(!File.Exists(commands) && !File.Exists(inspection) && !File.Exists(Path.Combine(dst, "SKILL.md")), "failed install not rolled back");
            Check(Directory.Exists(Path.Combine(dst, "references")), "rollback removed a pre-existing directory"); Directory.Delete(manifest);
            // A junction under the fixed target cannot redirect writes outside the skill.
            string outside = Path.Combine(root, "outside-dir"), link = Path.Combine(dst, "references");
            Directory.CreateDirectory(outside); Directory.CreateDirectory(dst);
            if (Directory.Exists(link)) Directory.Delete(link); // fixture owns this now-empty directory
            var start = new System.Diagnostics.ProcessStartInfo("cmd.exe", "/c mklink /J \"" + link + "\" \"" + outside + "\"") { UseShellExecute = false, CreateNoWindow = true, RedirectStandardOutput = true, RedirectStandardError = true };
            using (var process = System.Diagnostics.Process.Start(start)) { process.StandardOutput.ReadToEnd(); process.StandardError.ReadToEnd(); process.WaitForExit(); Check(process.ExitCode == 0, "junction fixture creation failed"); }
            Reject(delegate { m.Preview("install", "skill"); }); Check(!Directory.EnumerateFileSystemEntries(outside).Any(), "junction target modified"); Directory.Delete(link);
            m.Apply(m.Preview("install", "skill").ticket);
            File.Delete(Path.Combine(src, "references", "inspection.md"));
            try { m.Preview("update", "skill"); throw new Exception("missing source accepted"); }
            catch (InvalidOperationException e) { Check(e.Message.Contains("inspection.md"), "missing file must be named"); }
            Check((string)Row(m)["status"] == "unavailable" && ((string)Row(m)["diagnostic"]).Contains("不完整"), "missing bundle diagnostic");
            File.WriteAllText(Path.Combine(src, "references", "inspection.md"), "inspection");
            string sourceRefs = Path.Combine(src, "references"), savedRefs = Path.Combine(root, "saved-refs"); Directory.Move(sourceRefs, savedRefs);
            start = new System.Diagnostics.ProcessStartInfo("cmd.exe", "/c mklink /J \"" + sourceRefs + "\" \"" + savedRefs + "\"") { UseShellExecute = false, CreateNoWindow = true, RedirectStandardOutput = true, RedirectStandardError = true };
            using (var process = System.Diagnostics.Process.Start(start)) { process.StandardOutput.ReadToEnd(); process.StandardError.ReadToEnd(); process.WaitForExit(); Check(process.ExitCode == 0, "source junction fixture creation failed"); }
            Reject(delegate { m.Preview("update", "skill"); }); Directory.Delete(sourceRefs); Directory.Move(savedRefs, sourceRefs);
            Console.WriteLine("isolated skill lifecycle passed"); return 0;
        } catch (Exception e) { Console.Error.WriteLine(e.GetType().Name + ": " + e.Message); return 1; }
        finally { if (Directory.Exists(root)) Directory.Delete(root, true); }
    }
}
