using System;
using System.IO;
using System.Linq;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Web.Script.Serialization;
using System.Collections.Generic;
using System.ComponentModel;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;

namespace XshellDesktop {
// Only the fixed sibling of the original runtime is managed. This class never starts a process.
public sealed class LocalAgent {
    readonly string runtime, directory, path, manifestPath, version;
    readonly HostReverse builder;
    readonly JavaScriptSerializer json = new JavaScriptSerializer();
    readonly object gate = new object();
    // Fault injection for isolated native tests only; never exposed over IPC.
    internal Action BeforeManifestWrite = null;
    internal Action BeforeScriptCommit = null;
    sealed class Manifest {
        public string owner, version, source_id, script_hash, environment, log_encoding, mac;
    }
    public sealed class State {
        public string status, path, installed_version, packaged_version, source_id, environment, log_encoding, endpoint;
        public bool legacy_exists;
    }
    public sealed class Prepared {
        internal LocalAgent owner; internal bool consumed; internal string snapshot, id, environment, encoding, manifestHash, manifestAcl, scriptAcl, directoryAcl, sources;
        internal State preview;
        public State PublicPreview() { return Copy(preview); }
    }
    static State Copy(State s) { return new State { status=s.status, path=s.path, installed_version=s.installed_version, packaged_version=s.packaged_version, source_id=s.source_id, environment=s.environment, log_encoding=s.log_encoding, endpoint=s.endpoint, legacy_exists=s.legacy_exists }; }
    public LocalAgent(string runtime, HostReverse builder, string version) {
        this.runtime = Path.GetFullPath(runtime).TrimEnd(Path.DirectorySeparatorChar, Path.AltDirectorySeparatorChar); this.builder = builder; this.version = version;
        if (this.runtime.Length < 3) throw new InvalidOperationException("运行目录不能是磁盘根目录。");
        string parent = Path.GetDirectoryName(this.runtime);
        if (String.IsNullOrEmpty(parent) || builder == null || String.IsNullOrWhiteSpace(version)) throw new InvalidOperationException();
        directory = Path.Combine(parent, "local-agent"); path = Path.Combine(directory, "xshell_agent.js"); manifestPath = Path.Combine(directory, "manifest.json");
        HostReverse.Safe(this.runtime); HostReverse.Safe(path); HostReverse.Safe(manifestPath);
    }
    static void Settings(string environment, string encoding) {
        if (String.IsNullOrWhiteSpace(environment) || environment.Any(char.IsControl) || Encoding.UTF8.GetByteCount(environment) > 200 || !HostReverse.ValidEncoding(encoding)) throw new InvalidOperationException("本机脚本分组或日志编码无效。");
    }
    string Canonical(Manifest m) { return json.Serialize(new object[] { m.owner, m.version, m.source_id, m.script_hash, m.environment, m.log_encoding }); }
    Manifest ReadManifest() {
        HostReverse.Safe(manifestPath);
        if (!File.Exists(manifestPath) || new FileInfo(manifestPath).Length > 16384) return null;
        try {
            string text;
            using (var stream = new FileStream(manifestPath, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete))
            using (var reader = new StreamReader(stream)) text = reader.ReadToEnd();
            var raw = json.DeserializeObject(text) as System.Collections.Generic.Dictionary<string,object>;
            if (raw == null || raw.Count != 7 || new[] { "owner", "version", "source_id", "script_hash", "environment", "log_encoding", "mac" }.Any(k => !raw.ContainsKey(k) || !(raw[k] is string))) return null;
            var m = json.Deserialize<Manifest>(json.Serialize(raw));
            Settings(m.environment, m.log_encoding);
            if (m.owner != "xshell-bridge-local-v1" || String.IsNullOrWhiteSpace(m.version) || !Regex.IsMatch(m.source_id, "\\A[0-9a-f]{32}\\z") || !Regex.IsMatch(m.script_hash, "\\A[0-9a-f]{64}\\z") || m.mac != builder.LocalOwnershipMac(Canonical(m))) return null;
            return m;
        } catch (ReverseExportError) { throw; } catch { return null; }
    }
    string FileHash(string file) {
        HostReverse.Safe(file); if (!File.Exists(file)) return "absent";
        using (var stream = new FileStream(file, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete)) {
            if (stream.Length > 67108864) throw new InvalidOperationException();
            using (var hash = System.Security.Cryptography.SHA256.Create()) return BitConverter.ToString(hash.ComputeHash(stream)).Replace("-", "").ToLowerInvariant();
        }
    }
    string AclHash(string file) { HostReverse.Safe(file); return File.Exists(file) ? HostReverse.Hash(File.GetAccessControl(file).GetSecurityDescriptorBinaryForm()) : "absent"; }
    string DirectoryAcl() { HostReverse.Safe(directory); return Directory.Exists(directory) ? HostReverse.Hash(Directory.GetAccessControl(directory).GetSecurityDescriptorBinaryForm()) : "absent"; }
    bool PrivateAcl(FileSystemSecurity acl) {
        if (!acl.AreAccessRulesProtected || !WindowsIdentity.GetCurrent().User.Equals(acl.GetOwner(typeof(SecurityIdentifier)))) return false;
        var current=WindowsIdentity.GetCurrent().User; var system=new SecurityIdentifier(WellKnownSidType.LocalSystemSid,null);
        foreach (FileSystemAccessRule rule in acl.GetAccessRules(true,true,typeof(SecurityIdentifier)))
            if (rule.IsInherited || (!rule.IdentityReference.Equals(current) && !rule.IdentityReference.Equals(system))) return false;
        return true;
    }
    bool PrivateFile(string file) { HostReverse.Safe(file); return PrivateAcl(File.GetAccessControl(file)); }
    bool PrivateDirectory() { HostReverse.Safe(directory); return PrivateAcl(Directory.GetAccessControl(directory)); }
    State Inspect() {
        HostReverse.Safe(path); HostReverse.Safe(manifestPath);
        string endpoint = builder.PairingEndpoint();
        bool script = File.Exists(path), manifest = File.Exists(manifestPath);
        var s = new State { path=path, packaged_version=version, endpoint=endpoint, environment="local", log_encoding="auto", legacy_exists=File.Exists(Path.Combine(runtime, "xshell_agent.js")) };
        if (File.Exists(directory) || (Directory.Exists(directory) && !PrivateDirectory())) { s.status="conflict"; return s; }
        if (!script && !manifest && !Directory.Exists(path) && !Directory.Exists(manifestPath)) { s.status="absent"; return s; }
        var m = ReadManifest();
        if (m == null || !script || Directory.Exists(path) || Directory.Exists(manifestPath) || !PrivateFile(path) || !PrivateFile(manifestPath)) { s.status="conflict"; return s; }
        s.installed_version=m.version; s.source_id=m.source_id; s.environment=m.environment; s.log_encoding=m.log_encoding;
        if (FileHash(path) != m.script_hash) { s.status="modified"; return s; }
        string snapshot = builder.LocalSnapshot();
        string current = HostReverse.Hash(builder.BuildAgent(m.source_id, endpoint, "manual_terminal", m.environment, m.log_encoding, true));
        if (snapshot != builder.LocalSnapshot()) throw new InvalidOperationException("程序包或配对在检查期间变化，请重新检查。");
        s.status = m.version == version && m.script_hash == current ? "current" : "update_available";
        return s;
    }
    public State Status() { lock (gate) return Inspect(); }
    string Snapshot(string id, string environment, string encoding) {
        return HostReverse.Hash(Encoding.UTF8.GetBytes(json.Serialize(new object[] { runtime, directory, path, manifestPath, version, id, environment, encoding, builder.LocalSnapshot(), FileHash(path), FileHash(manifestPath), AclHash(path), AclHash(manifestPath), DirectoryAcl() })));
    }
    public Prepared Prepare(string environment, string encoding) {
        lock (gate) {
            Settings(environment, encoding); var state = Inspect();
            if (state.status != "absent" && state.status != "current" && state.status != "update_available") throw new InvalidOperationException("受管脚本已修改或归属不明，拒绝覆盖；请人工检查文件。");
            string id = state.source_id ?? Guid.NewGuid().ToString("N");
            string snapshot = Snapshot(id, environment, encoding);
            state.source_id=id; state.environment=environment; state.log_encoding=encoding;
            if (snapshot != Snapshot(id, environment, encoding)) throw new InvalidOperationException("文件或配对已变化，请重新预览。");
            return new Prepared { owner=this, snapshot=snapshot, id=id, environment=environment, encoding=encoding, manifestHash=FileHash(manifestPath), manifestAcl=AclHash(manifestPath), scriptAcl=AclHash(path), directoryAcl=DirectoryAcl(), sources=builder.LocalSnapshot(), preview=state };
        }
    }
    static void CreatePrivateDirectory(string dir) {
        HostReverse.Safe(dir);
        if (Directory.Exists(dir)) return;
        if (!Directory.Exists(Path.GetDirectoryName(dir))) throw new IOException();
        var acl = new DirectorySecurity(); acl.SetAccessRuleProtection(true, false); acl.SetOwner(WindowsIdentity.GetCurrent().User);
        foreach (var sid in new[] { WindowsIdentity.GetCurrent().User, new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null) }) acl.AddAccessRule(new FileSystemAccessRule(sid, FileSystemRights.FullControl, InheritanceFlags.ContainerInherit | InheritanceFlags.ObjectInherit, PropagationFlags.None, AccessControlType.Allow));
        Directory.CreateDirectory(dir, acl); HostReverse.Safe(dir);
    }
    public void Confirm(Prepared plan) {
        lock (gate) {
            if (plan == null || plan.owner != this || plan.consumed) throw new InvalidOperationException("确认已失效，请重新预览。");
            plan.consumed=true;
            // Other desktop processes using this same fixed destination must serialize confirmations.
            string name="Local\\XshellBridge-LocalAgent-"+HostReverse.Hash(Encoding.UTF8.GetBytes(path.ToUpperInvariant()));
            using (var mutex=new Mutex(false,name)) {
                bool acquired=false;
                try {
                    try { acquired=mutex.WaitOne(10000); } catch (AbandonedMutexException) { acquired=true; }
                    if (!acquired) throw new InvalidOperationException("另一个本机脚本操作仍在进行，请重新检查状态。");
                    ConfirmLocked(plan);
                } finally { if (acquired) mutex.ReleaseMutex(); }
            }
        }
    }
    void ConfirmLocked(Prepared plan) {
        if (BeforeScriptCommit != null) BeforeScriptCommit();
        if (plan.snapshot != Snapshot(plan.id, plan.environment, plan.encoding)) throw new InvalidOperationException("文件、程序包或配对已变化，请重新预览。");
        using (var files = new LockedFiles()) {
            // Pin ancestors before creation, then pin every input and managed file. No
            // writer, deletion, directory rename or junction replacement can race commit.
            files.PinDirectory(Path.GetDirectoryName(directory));
            foreach (string input in builder.LocalInputs()) files.PinInput(input);
            var oldScript = files.PinTarget(path); var oldManifest = files.PinTarget(manifestPath);
            if (plan.snapshot != Snapshot(plan.id, plan.environment, plan.encoding)) throw new InvalidOperationException("文件、程序包或配对已变化，请重新预览。");
            var state = Inspect();
            if (state.status != "absent" && state.status != "current" && state.status != "update_available") throw new InvalidOperationException("受管文件归属或内容已变化，拒绝覆盖。");
            if (state.source_id != null && state.source_id != plan.id) throw new InvalidOperationException("来源已变化。");
            byte[] output = builder.BuildAgent(plan.id, builder.PairingEndpoint(), "manual_terminal", plan.environment, plan.encoding, true);
            var manifest = new Manifest { owner="xshell-bridge-local-v1", version=version, source_id=plan.id, script_hash=HostReverse.Hash(output), environment=plan.environment, log_encoding=plan.encoding };
            manifest.mac=builder.LocalOwnershipMac(Canonical(manifest));
            CreatePrivateDirectory(directory); files.PinDirectory(directory);
            if (!PrivateDirectory() || (plan.directoryAcl != "absent" && DirectoryAcl() != plan.directoryAcl) || AclHash(path) != plan.scriptAcl || FileHash(manifestPath) != plan.manifestHash || AclHash(manifestPath) != plan.manifestAcl || builder.LocalSnapshot() != plan.sources) throw new InvalidOperationException("文件、程序包或配对已变化，请重新预览。");
            var script = files.Stage(path, output, oldScript);
            var metadata = files.Stage(manifestPath, Encoding.UTF8.GetBytes(json.Serialize(manifest)), oldManifest);
            files.Install(script);
            if (BeforeManifestWrite != null) BeforeManifestWrite();
            files.Install(metadata);
            files.Complete();
        }
    }

    // Rename through held handles, never replace an arbitrary pathname. A failed
    // second-file commit restores the held originals; unknown collisions survive.
    sealed class LockedFiles : IDisposable {
        [StructLayout(LayoutKind.Sequential)] struct NativeInfo {
            internal uint Attributes;
            internal System.Runtime.InteropServices.ComTypes.FILETIME Created, Accessed, Written;
            internal uint Volume, SizeHigh, SizeLow, Links, IndexHigh, IndexLow;
        }
        [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
        static extern SafeFileHandle CreateFile(string name, uint access, uint share, IntPtr security, uint disposition, uint flags, IntPtr template);
        [DllImport("kernel32.dll", SetLastError=true)]
        static extern bool GetFileInformationByHandle(SafeFileHandle handle, out NativeInfo info);
        [DllImport("kernel32.dll", SetLastError=true)]
        static extern bool SetFileInformationByHandle(SafeFileHandle handle, int infoClass, IntPtr info, uint size);
        internal sealed class Held {
            internal SafeFileHandle Handle; internal FileStream Stream;
            internal string Path, Original; internal Held Previous; internal bool Installed, BackedUp;
            internal void Close() { if (Stream != null) Stream.Dispose(); else Handle.Dispose(); }
        }
        readonly List<Held> handles = new List<Held>(), stages = new List<Held>();
        readonly HashSet<string> directories = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        bool complete;
        static IOException Failure() { return new IOException("受管文件正在使用或已变化。", new Win32Exception(Marshal.GetLastWin32Error())); }
        Held Open(string path, bool directory, bool rename, bool optional) {
            var handle=CreateFile(path, directory ? 0x80u : 0x80000000u | (rename ? 0x10000u : 0u), 1, IntPtr.Zero, 3, 0x00200000u | (directory ? 0x02000000u : 0u), IntPtr.Zero);
            if (handle.IsInvalid) { int error=Marshal.GetLastWin32Error(); handle.Dispose(); if(optional && (error==2 || error==3))return null; throw Failure(); }
            NativeInfo info;
            if (!GetFileInformationByHandle(handle,out info) || (info.Attributes & 0x400)!=0 || ((info.Attributes & 0x10)!=0)!=directory) { handle.Dispose(); throw new IOException("受管路径类型已变化。"); }
            var held=new Held { Handle=handle,Path=path,Original=path }; handles.Add(held); return held;
        }
        internal void PinDirectory(string path) {
            path=System.IO.Path.GetFullPath(path);
            if(directories.Contains(path))return;
            string parent=System.IO.Path.GetDirectoryName(path);
            if(!String.IsNullOrEmpty(parent))PinDirectory(parent);
            Open(path,true,false,false); directories.Add(path);
        }
        internal void PinInput(string path) { PinDirectory(System.IO.Path.GetDirectoryName(path)); Open(path,false,false,false); }
        internal Held PinTarget(string path) {
            string parent=System.IO.Path.GetDirectoryName(path);
            if(Directory.Exists(parent))PinDirectory(parent);
            return Open(path,false,true,true);
        }
        internal Held Stage(string destination, byte[] bytes, Held previous) {
            string temporary=System.IO.Path.Combine(System.IO.Path.GetDirectoryName(destination), ".local-"+Guid.NewGuid().ToString("N")+".tmp");
            var acl=new FileSecurity(); acl.SetAccessRuleProtection(true,false); acl.SetOwner(WindowsIdentity.GetCurrent().User);
            foreach(var sid in new[]{WindowsIdentity.GetCurrent().User,new SecurityIdentifier(WellKnownSidType.LocalSystemSid,null)}) acl.AddAccessRule(new FileSystemAccessRule(sid,FileSystemRights.FullControl,AccessControlType.Allow));
            var stream=new FileStream(temporary,FileMode.CreateNew,FileSystemRights.Read | FileSystemRights.Write | FileSystemRights.Delete,FileShare.Read,4096,FileOptions.WriteThrough,acl);
            var item=new Held { Stream=stream,Handle=stream.SafeFileHandle,Path=temporary,Original=destination,Previous=previous }; handles.Add(item); stages.Add(item);
            stream.Write(bytes,0,bytes.Length); stream.Flush(true); return item;
        }
        static void Rename(Held item, string destination) {
            byte[] name=Encoding.Unicode.GetBytes(destination); int offset=IntPtr.Size==8?20:12;
            IntPtr buffer=Marshal.AllocHGlobal(offset+name.Length+2);
            try {
                Marshal.Copy(new byte[offset+name.Length+2],0,buffer,offset+name.Length+2);
                Marshal.WriteInt32(buffer,IntPtr.Size==8?16:8,name.Length); Marshal.Copy(name,0,IntPtr.Add(buffer,offset),name.Length);
                if(!SetFileInformationByHandle(item.Handle,3,buffer,(uint)(offset+name.Length+2)))throw Failure();
                item.Path=destination;
            } finally { Marshal.FreeHGlobal(buffer); }
        }
        static void Delete(Held item) {
            IntPtr buffer=Marshal.AllocHGlobal(4);
            try { Marshal.WriteInt32(buffer,1); if(!SetFileInformationByHandle(item.Handle,4,buffer,4))throw Failure(); }
            finally { Marshal.FreeHGlobal(buffer); }
        }
        internal void Install(Held item) {
            if(item.Previous!=null) {
                Rename(item.Previous,System.IO.Path.Combine(System.IO.Path.GetDirectoryName(item.Original),".local-backup-"+Guid.NewGuid().ToString("N")));
                item.BackedUp=true;
            }
            Rename(item,item.Original); item.Installed=true;
        }
        internal void Complete() { complete=true; }
        public void Dispose() {
            for(int i=stages.Count-1;i>=0;i--) {
                var item=stages[i];
                if(!complete) {
                    try { if(item.Installed)Rename(item,System.IO.Path.Combine(System.IO.Path.GetDirectoryName(item.Original),".local-rollback-"+Guid.NewGuid().ToString("N"))); } catch { }
                    try { if(item.BackedUp)Rename(item.Previous,item.Original); } catch { /* Keep the original backup if an unknown file appeared. */ }
                    try { Delete(item); } catch { }
                } else if(item.BackedUp) { try { Delete(item.Previous); } catch { /* A private backup is safe to retain. */ } }
            }
            for(int i=handles.Count-1;i>=0;i--)handles[i].Close();
        }
    }
}
}
