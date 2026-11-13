using System;
using System.IO;
using System.Linq;
using System.Diagnostics;
using System.Text;
using System.Threading;

namespace XshellDesktop {
// Read-only executable discovery. No CWD lookup, PATH mutation or WindowsApps scan.
public sealed class CodexLocator {
    public sealed class Result {
        public string path, source, status, detail;
    }
    public const string Instructions = "未找到 Codex。请安装 Codex CLI，或确认当前用户已安装 Microsoft Store OpenAI.Codex；安装后重启桌面程序以刷新 PATH，再重新检查。";
    readonly Func<string> pathDiscovery;
    readonly Func<string[]> packageDiscovery;
    readonly Func<string, bool> exists;
    public CodexLocator() : this(delegate { return Environment.GetEnvironmentVariable("PATH"); }, RegisteredPackages, File.Exists) { }
    public CodexLocator(Func<string> pathDiscovery, Func<string[]> packageDiscovery, Func<string, bool> exists) {
        this.pathDiscovery = pathDiscovery; this.packageDiscovery = packageDiscovery; this.exists = exists;
    }
    static string Absolute(string value) {
        if (String.IsNullOrWhiteSpace(value) || value.Any(char.IsControl) || value.IndexOfAny(Path.GetInvalidPathChars()) >= 0 || value.Contains("\"")) return null;
        bool drive = value.Length >= 3 && Char.IsLetter(value[0]) && value[1] == ':' && (value[2] == '\\' || value[2] == '/');
        bool unc = value.StartsWith(@"\\", StringComparison.Ordinal) && !value.StartsWith(@"\\?\", StringComparison.Ordinal) && !value.StartsWith(@"\\.\", StringComparison.Ordinal);
        if (!drive && !unc) return null;
        if (unc && value.Substring(2).Split(new[] { '\\', '/' }, StringSplitOptions.RemoveEmptyEntries).Length < 2) return null;
        try { return Path.GetFullPath(value); } catch { return null; }
    }
    bool Exists(string path) { try { return exists(path); } catch { return false; } }
    static Result Found(string path, string source) { return new Result { path = path, source = source, status = "found", detail = "已检测到 Codex 执行入口。" }; }
    public Result Locate() {
        string path = null;
        try { path = pathDiscovery(); } catch { }
        foreach (string entry in (path ?? "").Split(';')) {
            string dir = entry.Trim();
            if (dir.Length >= 2 && dir[0] == '"' && dir[dir.Length - 1] == '"') dir = dir.Substring(1, dir.Length - 2);
            dir = Absolute(dir);
            if (dir == null) continue;
            try {
                string candidate = Path.Combine(dir, "codex.exe");
                if (Exists(candidate)) return Found(candidate, "path");
            } catch { }
        }
        try {
            foreach (string location in packageDiscovery() ?? new string[0]) {
                string root = Absolute(location);
                if (root == null) continue;
                // Only this exact bundled suffix is eligible; no recursive search.
                string candidate = Path.Combine(root, "app", "resources", "codex.exe");
                if (Exists(candidate)) return Found(candidate, "package");
            }
            return new Result { status = "not_found", detail = Instructions };
        } catch {
            return new Result { status = "discovery_failed", detail = "当前用户的 Codex 包查询失败或超时。" + Instructions };
        }
    }
    static string[] RegisteredPackages() {
        // Fixed system executable and fixed command. No user/config/PATH values enter PowerShell.
        string system = Environment.GetFolderPath(Environment.SpecialFolder.System);
        if (Absolute(system) == null) throw new InvalidOperationException();
        string powershell = Path.Combine(system, "WindowsPowerShell", "v1.0", "powershell.exe");
        const string command = "$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); Get-AppxPackage -Name OpenAI.Codex | ForEach-Object { $_.InstallLocation }";
        using (var p = new Process()) {
            p.StartInfo = new ProcessStartInfo(powershell, "-NoLogo -NoProfile -NonInteractive -EncodedCommand " + Convert.ToBase64String(Encoding.Unicode.GetBytes(command))) {
                UseShellExecute = false, CreateNoWindow = true, WindowStyle = ProcessWindowStyle.Hidden,
                RedirectStandardOutput = true, RedirectStandardError = true, StandardOutputEncoding = Encoding.UTF8
            };
            var output = new StringBuilder(); int total = 0, failed = 0;
            p.Start();
            ThreadStart stdout = delegate {
                try {
                    char[] buffer = new char[1024]; int count;
                    while ((count = p.StandardOutput.Read(buffer, 0, buffer.Length)) > 0) {
                        if (Interlocked.Add(ref total, count) <= 32768) output.Append(buffer, 0, count);
                        else Interlocked.Exchange(ref failed, 1);
                    }
                } catch { Interlocked.Exchange(ref failed, 1); }
            };
            ThreadStart stderr = delegate {
                try {
                    char[] buffer = new char[1024]; int count;
                    while ((count = p.StandardError.Read(buffer, 0, buffer.Length)) > 0)
                        if (Interlocked.Add(ref total, count) > 32768) Interlocked.Exchange(ref failed, 1);
                } catch { Interlocked.Exchange(ref failed, 1); }
            };
            var a = new Thread(stdout) { IsBackground = true }; var b = new Thread(stderr) { IsBackground = true };
            a.Start(); b.Start();
            bool done = p.WaitForExit(5000);
            if (!done) { try { p.Kill(); } catch { } }
            bool joined = a.Join(1000) & b.Join(1000);
            if (!done || !joined || failed != 0 || p.ExitCode != 0) throw new InvalidOperationException();
            // Accept only absolute path lines, never diagnostics or arbitrary stderr.
            return output.ToString().Split(new[] { '\r', '\n' }, StringSplitOptions.RemoveEmptyEntries).Select(Absolute).Where(s => s != null).ToArray();
        }
    }
}
}
