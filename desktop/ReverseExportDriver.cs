// Fixture-only native export driver. Compile together with HostReverse.cs and
// /r:System.Web.Extensions.dll. Never install this executable as a desktop host.
// Usage: ReverseExportDriver runtime bundle catalog destination remotePort [mode]
// The first five arguments are absolute fixture paths (except remotePort).
// Only manual_terminal is accepted.
using System;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Text;
using System.Web.Script.Serialization;
using XshellDesktop;

public static class ReverseExportDriver {
    static string Absolute(string value) {
        if (String.IsNullOrWhiteSpace(value) || !Path.IsPathRooted(value) ||
            value.StartsWith(@"\\") || value.IndexOf(':', 2) >= 0)
            throw new InvalidOperationException("Absolute local fixture path required.");
        string result = Path.GetFullPath(value).TrimEnd(Path.DirectorySeparatorChar);
        for (string p = result; !String.IsNullOrEmpty(p); p = Path.GetDirectoryName(p)) {
            try {
                if ((File.GetAttributes(p) & FileAttributes.ReparsePoint) != 0)
                    throw new InvalidOperationException("Fixture reparse point refused.");
            } catch (FileNotFoundException) { }
              catch (DirectoryNotFoundException) { }
        }
        return result;
    }

    static void Exact(string actual, string expected) {
        if (!String.Equals(Absolute(actual), Absolute(expected), StringComparison.OrdinalIgnoreCase))
            throw new InvalidOperationException("Unexpected fixture layout.");
    }

    static void CreateFakeFile(string path, string body) {
        // Never overwrite even another fixture file. These are intentionally not
        // parseable SSH credentials; no Connect operation is performed.
        using (var stream = new FileStream(path, FileMode.CreateNew, FileAccess.Write, FileShare.None)) {
            byte[] bytes = Encoding.ASCII.GetBytes(body);
            stream.Write(bytes, 0, bytes.Length);
        }
    }

    public static int Main(string[] args) {
        try {
            if (args.Length != 5 && args.Length != 6)
                throw new ArgumentException("runtime bundle catalog destination remotePort [mode]");
            string runtime = Absolute(args[0]);
            string bundle = Absolute(args[1]);
            string catalog = Absolute(args[2]);
            string destination = Absolute(args[3]);
            string root = Path.GetDirectoryName(runtime);
            if (String.IsNullOrEmpty(root) ||
                !Path.GetFileName(root).StartsWith("xsh-reverse-smoke-", StringComparison.Ordinal))
                throw new InvalidOperationException("Private smoke fixture root required.");
            Exact(runtime, Path.Combine(root, "runtime"));
            Exact(bundle, Path.Combine(root, "bundle"));
            Exact(catalog, Path.Combine(root, "catalog"));
            Exact(Path.GetDirectoryName(destination), Path.Combine(root, "vm"));
            if (!Directory.Exists(runtime) || !Directory.Exists(bundle) ||
                !Directory.Exists(catalog) || !Directory.Exists(Path.GetDirectoryName(destination)))
                throw new InvalidOperationException("Fixture directories must already exist.");
            if (File.Exists(Path.Combine(root, "vm", "agent.json")))
                throw new InvalidOperationException("Script VM must not contain agent.json.");
            int port;
            if (!Int32.TryParse(args[4], NumberStyles.None, CultureInfo.InvariantCulture, out port) ||
                port < 1024 || port > 65535)
                throw new ArgumentException("Invalid fixture remote port.");
            string mode = args.Length == 6 ? args[5] : "manual_terminal";
            if (mode != "manual_terminal")
                throw new ArgumentException("Invalid fixture binding mode.");
            string suffix = Guid.NewGuid().ToString("N");
            string key = Absolute(Path.Combine(root, "fake-key-" + suffix));
            string known = Absolute(Path.Combine(root, "fake-known-" + suffix));
            CreateFakeFile(key, "NOT AN SSH PRIVATE KEY - ISOLATED SMOKE FIXTURE\n");
            CreateFakeFile(known, "# ISOLATED SMOKE FIXTURE - NO HOST KEYS\n");
            // Fail closed if any future change accidentally attempts Connect.
            // There is no real SSH factory and no started child process here.
            Func<ProcessStartInfo, IReverseProcess> forbidden = delegate(ProcessStartInfo ignored) {
                throw new InvalidOperationException("SSH forbidden in export smoke driver.");
            };
            using (var host = new HostReverse(catalog, bundle, runtime, forbidden)) {
                string id = host.Add("Isolated reverse smoke " + mode, "fixture.invalid", 22,
                    "fixture", key, known, port, "ISOLATED TEST ONLY", mode);
                HostReverse.PreparedExport plan = host.PrepareExport(id, destination);
                HostReverse.ExportPreview before = plan.PublicPreview();
                if (before.source_id != id || before.mode != mode ||
                    before.endpoint != "http://127.0.0.1:" + port.ToString(CultureInfo.InvariantCulture))
                    throw new InvalidOperationException("Unexpected export preview.");
                HostReverse.ExportPreview exported = host.ConfirmExport(plan);
                if (!File.Exists(destination) || exported.source_id != id)
                    throw new InvalidOperationException("Export missing or source changed.");
                bool refused = false;
                try { host.ConfirmExport(plan); }
                catch (InvalidOperationException) { refused = true; }
                if (!refused)
                    throw new InvalidOperationException("Export confirmation was reusable.");
                if (host.Get(id).status != HostReverse.Disconnected)
                    throw new InvalidOperationException("Export unexpectedly connected SSH.");
                Console.OutputEncoding = new UTF8Encoding(false);
                Console.WriteLine(new JavaScriptSerializer().Serialize(exported));
            }
            return 0;
        } catch (Exception) {
            // Do not disclose exported credentials, runtime contents, or native
            // exception details. Compiler diagnostics remain the parent's job.
            Console.Error.WriteLine("Reverse export fixture failed; no external output disclosed.");
            return 1;
        }
    }
}
