// .NET Framework 4.8 / C# 5. No Xshell, network, command runner or target access.
using System;
using System.IO;
using System.Text;
using System.Threading;
using System.Diagnostics;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Web.Script.Serialization;
using Microsoft.Win32.SafeHandles;

internal sealed class LogCapture : IDisposable {
    const long Limit = 8 * 1024 * 1024;
    readonly JavaScriptSerializer json = new JavaScriptSerializer { MaxJsonLength = 32768, RecursionLimit = 8 };
    readonly List<SafeFileHandle> parents = new List<SafeFileHandle>();
    FileStream log;
    string directory, path, identity, directoryIdentity;
    bool prepared, bound, bindReady;
    long largest;
    int sequence;
    string choice = "auto";
    Decoder decoder;
    readonly Encoder utf8 = new UTF8Encoding(false, true).GetEncoder();
    readonly List<byte> prefix = new List<byte>();
    long normalized, finalSize;
    bool finished;
    bool keep = true, disposed, faulted;
    static long touched = Stopwatch.GetTimestamp();
    static readonly long born = touched;
    [StructLayout(LayoutKind.Sequential)] struct Info {
        public uint attributes; public System.Runtime.InteropServices.ComTypes.FILETIME created, accessed, written;
        public uint volume, highSize, lowSize, links, highId, lowId;
    }
    [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
    static extern SafeFileHandle CreateFile(string p, uint access, uint share, IntPtr security, uint mode, uint flags, IntPtr template);
    [DllImport("kernel32.dll", SetLastError=true)] static extern bool GetFileInformationByHandle(SafeFileHandle h, out Info i);
    [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern uint GetFinalPathNameByHandle(SafeFileHandle h, StringBuilder p, uint size, uint flags);
    [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)] static extern uint GetFileAttributes(string p);
    static void Require(bool value, string message) { if (!value) throw new IOException(message); }
    static string Full(string p) {
        Require(!String.IsNullOrEmpty(p) && p.Length < 230 && p.Length > 3 && Char.IsLetter(p[0]) && p[1] == ':' && p[2] == '\\' && p.IndexOf(':', 2) < 0 && !System.Text.RegularExpressions.Regex.IsMatch(p, "[\\x00-\\x1f<>\"|?*]"), "unsafe local path");
        string full = Path.GetFullPath(p);
        Require(String.Equals(full, p, StringComparison.OrdinalIgnoreCase), "noncanonical path");
        foreach (string s in p.Split('\\')) Require(!s.EndsWith(".") && !s.EndsWith(" "), "unsafe path component");
        return full;
    }
    static void Safe(string p) {
        for (string v = Full(p); !String.IsNullOrEmpty(v); v = Path.GetDirectoryName(v))
            Require((File.GetAttributes(v) & FileAttributes.ReparsePoint) == 0, "reparse path refused");
    }
    static string Identity(SafeFileHandle h, string expected, bool isDirectory = false) {
        Info i; Require(GetFileInformationByHandle(h, out i), "file identity unavailable");
        Require((i.attributes & (uint)FileAttributes.ReparsePoint) == 0 && (isDirectory || i.links == 1), "reparse/hardlink refused");
        Require(((i.attributes & (uint)FileAttributes.Directory) != 0) == isDirectory, "unexpected file type");
        var b = new StringBuilder(1024); uint n = GetFinalPathNameByHandle(h, b, 1024, 0);
        Require(n > 0 && n < 1024 && String.Equals(b.ToString(), @"\\?\" + expected, StringComparison.OrdinalIgnoreCase), "final path changed");
        return i.volume.ToString("x8") + i.highId.ToString("x8") + i.lowId.ToString("x8");
    }
    static DirectorySecurity Private() {
        var a = new DirectorySecurity(); a.SetAccessRuleProtection(true, false);
        foreach (var sid in new[] { WindowsIdentity.GetCurrent().User, new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null) })
            a.AddAccessRule(new FileSystemAccessRule(sid, FileSystemRights.FullControl, InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit, PropagationFlags.None, AccessControlType.Allow));
        return a;
    }
    void HoldParents(string p) {
        // Every existing ancestor is held without FILE_SHARE_DELETE, preventing
        // ordinary directory rename/replacement for this process lifetime.
        var paths = new List<string>();
        for (string v = p; !String.IsNullOrEmpty(v); v = Path.GetDirectoryName(v)) paths.Add(v);
        paths.Reverse();
        foreach (string v in paths) {
            Require((File.GetAttributes(v) & FileAttributes.ReparsePoint) == 0, "reparse ancestor refused");
            var h = CreateFile(v, 0, 3, IntPtr.Zero, 3, 0x02000000 | 0x00200000, IntPtr.Zero);
            if (h.IsInvalid) { h.Dispose(); throw new IOException("cannot protect log parent"); }
            Info i; if (!GetFileInformationByHandle(h, out i) || (i.attributes & 1024) != 0) { h.Dispose(); throw new IOException("unsafe parent handle"); }
            parents.Add(h);
        }
    }
    void Prepare(string nonce) {
        Require(System.Text.RegularExpressions.Regex.IsMatch(nonce ?? "", "\\A[A-Za-z0-9_-]{1,96}\\z"), "invalid job nonce");
        string local = Full(Environment.GetEnvironmentVariable("LOCALAPPDATA")); Safe(local);
        string root = Full(Path.Combine(local, "XshellBridge-agent"));
        if (!Directory.Exists(root)) Directory.CreateDirectory(root, Private());
        Safe(root); HoldParents(root);
        // The verified executable was extracted into this operator's profile.
        // Protect its private extraction directory before accepting job input.
        string exeDir = Full(Path.GetDirectoryName(typeof(LogCapture).Assembly.Location)); Safe(exeDir);
        Require(exeDir.StartsWith(root + "\\", StringComparison.OrdinalIgnoreCase), "helper must run from private extraction directory");
        Directory.SetAccessControl(exeDir, Private());
        directory = Full(Path.Combine(root, "job-" + Guid.NewGuid().ToString("N")));
        Require(!Directory.Exists(directory) && !File.Exists(directory), "private job directory collision");
        Directory.CreateDirectory(directory, Private()); Safe(directory); HoldParents(directory);
        path = Full(Path.Combine(directory, "capture.log"));
        directoryIdentity = Identity(parents[parents.Count-1], directory, true);
        prepared = true;
        Prepared();
    }
    void DirectoryOwned() {
        Require(prepared && !disposed, "directory lease unavailable"); Safe(directory);
        Require(Identity(parents[parents.Count-1], directory, true) == directoryIdentity, "held directory identity changed");
        using (var probe = CreateFile(directory, 0, 3, IntPtr.Zero, 3, 0x02000000 | 0x00200000, IntPtr.Zero)) {
            Require(!probe.IsInvalid && Identity(probe, directory, true) == directoryIdentity, "private directory replaced");
        }
    }
    void Prepared() {
        Require(prepared && !bound && !faulted, "capture is not awaiting bind");
        DirectoryOwned();
        uint attrs = GetFileAttributes(path);
        Require(attrs == 0xffffffff && Marshal.GetLastWin32Error() == 2, "expected log already exists or cannot be checked");
    }
    void Bind() {
        Require(prepared && !bound && bindReady && !faulted, "bind requires checked unbound capture");
        bindReady = false;
        DirectoryOwned();
        var handle = CreateFile(path, 0x80000000, 3, IntPtr.Zero, 3, 0x00200000 | 0x08000000, IntPtr.Zero);
        if (handle.IsInvalid) { int error=Marshal.GetLastWin32Error(); handle.Dispose(); throw new IOException("cannot open expected log for reading (Windows error " + error + ")"); }
        try {
            identity = Identity(handle, path);
            log = new FileStream(handle, FileAccess.Read, 4096, false); handle = null;
            bound = true;
            Check();
        } finally { if (handle != null) handle.Dispose(); }
    }
    void Owned() {
        Require(bound && log != null && !disposed, "log not bound"); DirectoryOwned(); Safe(path);
        Require(Identity(log.SafeFileHandle, path) == identity, "held log identity changed");
        using (var probe = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite))
            Require(Identity(probe.SafeFileHandle, path) == identity, "log replaced");
    }
    void Check() {
        Owned();
        long length = log.Length;
        Require(length >= largest && length >= log.Position, "log truncated");
        Require(length <= Limit, "log exceeds 8 MiB limit"); largest = length;
    }
    static bool ValidEncoding(string value) {
        return value == "auto" || value == "utf-8" || value == "utf-16le" || value == "utf-16be" || value == "gbk" || value == "gb18030";
    }
    byte[] Decode(byte[] input, int count, bool final) {
        try {
            int offset = 0;
            if (decoder == null) {
                // Hold only a possible split BOM, never infer locale encoding.
                while (offset < count && prefix.Count < 3) prefix.Add(input[offset++]);
                if (!final && (prefix.Count == 0 || (prefix.Count == 1 && (prefix[0] == 0xef || prefix[0] == 0xff || prefix[0] == 0xfe)) || (prefix.Count == 2 && prefix[0] == 0xef && prefix[1] == 0xbb))) return new byte[0];
                string bom = null; int skip = 0;
                if (prefix.Count >= 3 && prefix[0] == 0xef && prefix[1] == 0xbb && prefix[2] == 0xbf) { bom = "utf-8"; skip = 3; }
                else if (prefix.Count >= 2 && prefix[0] == 0xff && prefix[1] == 0xfe) { bom = "utf-16le"; skip = 2; }
                else if (prefix.Count >= 2 && prefix[0] == 0xfe && prefix[1] == 0xff) { bom = "utf-16be"; skip = 2; }
                Require(bom == null || choice == "auto" || choice == bom, "log BOM conflicts with selected encoding");
                string selected = choice == "auto" ? (bom ?? "utf-8") : choice;
                int cp = selected == "utf-8" ? 65001 : selected == "utf-16le" ? 1200 : selected == "utf-16be" ? 1201 : selected == "gbk" ? 936 : 54936;
                decoder = Encoding.GetEncoding(cp, EncoderFallback.ExceptionFallback, DecoderFallback.ExceptionFallback).GetDecoder();
                var joined = new byte[prefix.Count - skip + count - offset];
                for (int i=skip; i<prefix.Count; i++) joined[i-skip]=prefix[i];
                Buffer.BlockCopy(input, offset, joined, prefix.Count-skip, count-offset);
                prefix.Clear(); input=joined; offset=0; count=joined.Length;
            }
            int length = count - offset;
            var chars = new char[8192];
            int charCount = decoder.GetChars(input, offset, length, chars, 0, final);
            var bytes = new byte[16384];
            int byteCount = utf8.GetBytes(chars, 0, charCount, bytes, 0, final);
            normalized += byteCount; Require(normalized <= Limit, "normalized log exceeds 8 MiB limit");
            var result = new byte[byteCount]; Buffer.BlockCopy(bytes, 0, result, 0, byteCount); return result;
        } catch (DecoderFallbackException) { throw new IOException("invalid or incomplete log encoding; select the correct source encoding"); }
          catch (EncoderFallbackException) { throw new IOException("invalid or incomplete Unicode log"); }
    }
    object Handle(Dictionary<string, object> r) {
        if (!r.ContainsKey("seq") || !(r["seq"] is int) || (int)r["seq"] != ++sequence) throw new InvalidOperationException("protocol sequence mismatch");
        string op = r.ContainsKey("op") ? r["op"] as string : null;
        if (op == "prepare") { Require(!prepared && sequence == 1, "prepare only once"); choice = r.ContainsKey("encoding") ? r["encoding"] as string : "auto"; Require(ValidEncoding(choice), "invalid log encoding"); Prepare(r.ContainsKey("nonce") ? r["nonce"] as string : null); return new { seq=sequence, ok=true, path=path }; }
        if (op == "prepared") { Prepared(); bindReady=true; return new { seq=sequence, ok=true }; }
        if (op == "bind") { Bind(); return new { seq=sequence, ok=true, identity=identity }; }
        if (op == "owned") { Owned(); return new { seq=sequence, ok=true, identity=identity }; }
        if (op == "check") { Require(!faulted, "log capture previously failed"); Check(); return new { seq=sequence, ok=true, size=largest, identity=identity }; }
        if (op == "read") {
            Require(!faulted, "log capture previously failed");
            bool final = r.ContainsKey("final") && r["final"] is bool && (bool)r["final"];
            Require(!r.ContainsKey("final") || r["final"] is bool, "invalid final flag");
            Check();
            if (finished) { Require(final && largest == finalSize, "log changed after finalization"); return new { seq=sequence, ok=true, data="", eof=true }; }
            var bytes = new byte[4096]; int n = log.Read(bytes, 0, (int)Math.Min(bytes.Length, largest - log.Position)); Check();
            bool flush = final && log.Position == largest;
            byte[] data = Decode(bytes, n, flush);
            if (flush) { finished=true; finalSize=largest; }
            return new { seq=sequence, ok=true, data=Convert.ToBase64String(data), eof=log.Position == largest };
        }
        if (op == "close") { Require(prepared && r.ContainsKey("keep") && r["keep"] is bool, "invalid close"); if (bound) { if ((bool)r["keep"]) Owned(); else { Require(!faulted, "failed capture must be retained"); Check(); } } else { Require((bool)r["keep"], "unbound capture must be retained"); DirectoryOwned(); } keep=(bool)r["keep"]; Dispose(); return new { seq=sequence, ok=true, retained=keep ? directory : "" }; }
        throw new IOException("unsupported protocol operation");
    }
    public void Dispose() {
        if (disposed) return;
        try {
            if (log != null) {
                if (!keep) Check();
                log.Dispose(); log=null;
                if (!keep) {
                    Safe(path);
                    using (var probe = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite)) Require(Identity(probe.SafeFileHandle, path) == identity, "cleanup identity changed");
                    File.Delete(path);
                }
            }
        } finally { disposed=true; if (log != null) { log.Dispose(); log=null; } foreach (var h in parents) h.Dispose(); parents.Clear(); }
        if (!keep && directory != null) { Safe(directory); Directory.Delete(directory, false); }
    }
    void Reply(StreamWriter output, object value) {
        // WSH TextStream uses the machine ANSI code page. Protocol JSON is ASCII,
        // including Unicode private paths; log bytes themselves are base64.
        string encoded=json.Serialize(value); var ascii=new StringBuilder();
        foreach(char c in encoded) { if(c>126) ascii.Append("\\u").Append(((int)c).ToString("x4")); else ascii.Append(c); }
        output.WriteLine(ascii.ToString());
    }
    public static int Main(string[] args) {
        if (args.Length != 1 || args[0] != "--stdio-v1") return 2;
        // /target:winexe still inherits WScript.Shell.Exec standard pipe handles.
        // Explicit standard streams avoid relying on an allocated console.
        using (var input = new StreamReader(Console.OpenStandardInput(), new UTF8Encoding(false, true), false, 1024))
        using (var output = new StreamWriter(Console.OpenStandardOutput(), new UTF8Encoding(false)) { AutoFlush=true })
        using (var helper = new LogCapture())
        using (var timer = new Timer(delegate {
            long t=Stopwatch.GetTimestamp();
            if ((t-Interlocked.Read(ref touched))/(double)Stopwatch.Frequency > 15 || (t-born)/(double)Stopwatch.Frequency > 7200) Environment.Exit(3);
        }, null, 1000, 1000)) {
            try {
                while (true) {
                    var line = new StringBuilder(); int c;
                    while ((c=input.Read()) >= 0 && c != 10) { Require(((c >= 32 && c <= 126) || c == 13) && line.Length < 1024, "protocol line limit/encoding"); line.Append((char)c); }
                    if (c < 0) return 0;
                    Interlocked.Exchange(ref touched, Stopwatch.GetTimestamp());
                    var request=helper.json.Deserialize<Dictionary<string, object>>(line.ToString().TrimEnd('\r'));
                    object reply;
                    try { reply=helper.Handle(request); }
                    catch (IOException e) {
                        string op=request.ContainsKey("op") ? request["op"] as string : null;
                        // Keep the held identity available after capture failure so
                        // the script can stop only its own log and retain evidence.
                        if (helper.log == null || (op != "check" && op != "read")) throw;
                        helper.faulted=true;
                        helper.Reply(output, new { seq=helper.sequence, ok=false, error=e.Message.Length <= 240 ? e.Message : "native log read failed", retained=helper.path });
                        continue;
                    }
                    helper.Reply(output, reply);
                    if ((string)request["op"] == "close") return 0;
                }
            } catch (Exception e) {
                // No file contents or arbitrary exception text on stdout.
                string reason = e is IOException ? e.Message : "native log protocol failed";
                if (reason.Length > 240) reason="native log I/O failed (check logging/sharing and private paths)";
                helper.Reply(output, new { seq=helper.sequence, ok=false, error=reason, retained=helper.path ?? "" }); return 1;
            }
        }
    }
}
