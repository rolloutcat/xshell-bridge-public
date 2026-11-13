using System;
using System.IO;
using XshellDesktop;

public static class CodexLocatorTests {
    static void Assert(bool value) { if (!value) throw new Exception("Codex locator assertion failed"); }
    public static int Main() {
        string root = @"C:\Program Files\WindowsApps\OpenAI.Codex_99.42_x64__fixture";
        string packaged = Path.Combine(root, "app", "resources", "codex.exe");
        int queries = 0;
        Func<string[]> packages = delegate { queries++; return new[] { root }; };
        Func<string, bool> exists = delegate(string p) { return p == packaged || p == @"C:\CLI tools\codex.exe"; };
        // Root failure: Explorer's PATH need not contain the Store desktop executable.
        var result = new CodexLocator(delegate { return null; }, packages, exists).Locate();
        Assert(result.path == packaged && result.status == "found" && result.source == "package" && queries == 1);
        queries = 0;
        result = new CodexLocator(delegate { return "\"C:\\CLI tools\";C:\\missing"; }, packages, exists).Locate();
        Assert(result.path == @"C:\CLI tools\codex.exe" && result.source == "path" && queries == 0);
        int probes = 0;
        result = new CodexLocator(delegate { return ";.;relative;C:relative;\\rooted;\"broken;bad\0entry"; }, packages, delegate(string p) { probes++; return p == packaged; }).Locate();
        Assert(result.path == packaged && probes == 1);
        result = new CodexLocator(delegate { return ""; }, delegate { return new[] { "relative", @"C:relative", @"\rooted", "bad\0root", root }; }, exists).Locate();
        Assert(result.path == packaged);
        result = new CodexLocator(delegate { return null; }, delegate { return new string[0]; }, exists).Locate();
        Assert(result.status == "not_found" && result.path == null);
        result = new CodexLocator(delegate { return null; }, packages, delegate { return false; }).Locate();
        Assert(result.status == "not_found");
        result = new CodexLocator(delegate { throw new Exception("SECRET"); }, packages, exists).Locate();
        Assert(result.path == packaged);
        result = new CodexLocator(delegate { return null; }, delegate { throw new Exception("SECRET"); }, exists).Locate();
        Assert(result.status == "discovery_failed" && !result.detail.Contains("SECRET"));
        result = new CodexLocator(delegate { return @"C:\CLI tools"; }, packages, delegate { throw new Exception("SECRET"); }).Locate();
        Assert(result.path == null && !result.detail.Contains("SECRET"));
        Console.WriteLine("Codex locator tests passed");
        return 0;
    }
}
