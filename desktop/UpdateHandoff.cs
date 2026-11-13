using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.Globalization;
using System.IO;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text;
using System.Threading;
using System.Web.Script.Serialization;

namespace XshellDesktop {
internal static class UpdateHandoff {
 internal sealed class Identity {
  internal int Pid;
  internal long Start;
  internal string Exe;
 }
 internal interface IEnvironment {
  Identity Current { get; }
  Identity Find(int pid);
  long Milliseconds { get; }
  void Wait(int milliseconds);
  bool LockExists(string path);
  bool Start(ProcessStartInfo info);
 }
 internal sealed class RealEnvironment : IEnvironment {
  readonly Stopwatch watch = Stopwatch.StartNew();
  static Identity Read(Process p) {
   return new Identity { Pid=p.Id, Start=p.StartTime.ToUniversalTime().Ticks, Exe=p.MainModule.FileName };
  }
  public Identity Current { get { using(var p=Process.GetCurrentProcess()) return Read(p); } }
  public Identity Find(int pid) {
   Process p;
   try { p=Process.GetProcessById(pid); }
   catch(ArgumentException) { return null; }
   using(p) {
    if(p.HasExited) return null;
    try { return Read(p); }
    catch(InvalidOperationException) { if(p.HasExited) return null; throw; }
   }
  }
  public long Milliseconds { get { return watch.ElapsedMilliseconds; } }
  public void Wait(int milliseconds) { Thread.Sleep(milliseconds); }
  public bool LockExists(string path) { SafePath(path); return File.Exists(path) || Directory.Exists(path); }
  public bool Start(ProcessStartInfo info) { using(var p=Process.Start(info)) return p!=null; }
 }
 internal sealed class Manifest {
  internal int OldPid;
  internal long OldStart;
  internal string OldExe, NewBundle, OldBundle, Mode, Runtime, ObserverConfig;
  internal bool Observer;
 }
 static InvalidOperationException Invalid() { return new InvalidOperationException("更新交接验证失败；旧版本目录已保留，未自动重试或回退。"); }
 internal static string PrivateRoot {
  get { return Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData), "XshellBridge-updates", "handoff"); }
 }
 static SecurityIdentifier User { get { using(var i=WindowsIdentity.GetCurrent()) return i.User; } }
 internal static string SafePath(string path) {
  if(String.IsNullOrEmpty(path) || path.IndexOf('"')>=0) throw Invalid();
  foreach(char c in path) if(Char.IsControl(c)) throw Invalid();
  // Only absolute DOS paths: no UNC, device paths, ADS, or drive-relative paths.
  if(path.Length<3 || !Char.IsLetter(path[0]) || path[1]!=':' || path[2]!='\\' || path.IndexOf(':',2)>=0) throw Invalid();
  string full=Path.GetFullPath(path).TrimEnd('\\');
  if(full.Length==2) full+="\\";
  for(string p=full; !String.IsNullOrEmpty(p); p=Path.GetDirectoryName(p)) {
   try { if((File.GetAttributes(p)&FileAttributes.ReparsePoint)!=0) throw Invalid(); }
   catch(FileNotFoundException) { }
   catch(DirectoryNotFoundException) { }
  }
  return full;
 }
 static bool Same(string a,string b) { return String.Equals(a,b,StringComparison.OrdinalIgnoreCase); }
 static void FileRequired(string path) { SafePath(path); if(!File.Exists(path)) throw Invalid(); }
 static void Bundle(string path) {
  if(!Directory.Exists(SafePath(path))) throw Invalid();
  foreach(string n in new[]{"xshdesk.exe","xshctl.exe","xshupdater.exe","VERSION","scripts\\start-desktop.ps1"}) FileRequired(Path.Combine(path,n));
 }
 internal static void Validate(Manifest m) {
  if(m==null || m.OldPid<=0 || m.OldStart<=0 || m.OldStart>DateTime.MaxValue.Ticks || (m.Mode!="Local" || m.Observer)) throw Invalid();
  m.OldBundle=SafePath(m.OldBundle); m.NewBundle=SafePath(m.NewBundle); m.OldExe=SafePath(m.OldExe);
  if(!Same(m.OldExe,Path.Combine(m.OldBundle,"xshdesk.exe")) || Same(m.NewBundle,m.OldBundle) || Within(m.NewBundle,m.OldBundle) || Within(m.OldBundle,m.NewBundle)) throw Invalid();
  Bundle(m.OldBundle); Bundle(m.NewBundle);
  if(m.ObserverConfig!=null) throw Invalid();
  m.Runtime=SafePath(m.Runtime);
  if(!Same(Path.GetFileName(m.Runtime),"runtime") || !Directory.Exists(m.Runtime)) throw Invalid();
  foreach(string f in new[]{"bridge.json","client.json","agent.json","admin.json"}) FileRequired(Path.Combine(m.Runtime,f));
 }
 static bool Within(string child,string parent) { return child.StartsWith(parent.TrimEnd('\\')+"\\",StringComparison.OrdinalIgnoreCase); }
 static DirectorySecurity DirectoryAcl() {
  var s=new DirectorySecurity(); s.SetAccessRuleProtection(true,false); s.SetOwner(User);
  s.AddAccessRule(new FileSystemAccessRule(User,FileSystemRights.FullControl,InheritanceFlags.ContainerInherit|InheritanceFlags.ObjectInherit,PropagationFlags.None,AccessControlType.Allow)); return s;
 }
 static FileSecurity FileAcl() {
  var s=new FileSecurity(); s.SetAccessRuleProtection(true,false); s.SetOwner(User);
  s.AddAccessRule(new FileSystemAccessRule(User,FileSystemRights.FullControl,AccessControlType.Allow)); return s;
 }
 internal static void CheckPrivate(string path,bool directory) {
  SafePath(path);
  FileSystemSecurity s=directory ? (FileSystemSecurity)Directory.GetAccessControl(path) : File.GetAccessControl(path);
  if(!s.AreAccessRulesProtected || !User.Equals(s.GetOwner(typeof(SecurityIdentifier)))) throw Invalid();
  bool full=false;
  foreach(FileSystemAccessRule r in s.GetAccessRules(true,true,typeof(SecurityIdentifier))) {
   if(r.AccessControlType==AccessControlType.Allow) {
    if(!User.Equals(r.IdentityReference)) throw Invalid();
    if((r.FileSystemRights&FileSystemRights.FullControl)==FileSystemRights.FullControl) full=true;
   } else throw Invalid();
  }
  if(!full) throw Invalid();
 }
 static void EnsureRoot(string root) {
  root=SafePath(root);
  if(!Directory.Exists(root)) {
   string parent=Path.GetDirectoryName(root); SafePath(parent);
   Directory.CreateDirectory(parent); SafePath(parent);
   Directory.CreateDirectory(root,DirectoryAcl());
  }
  CheckPrivate(root,true);
 }
 static string OwnedPath(string path,string root) {
  root=SafePath(root); path=SafePath(path);
  Guid id;
  if(!Same(Path.GetDirectoryName(path),root) || !String.Equals(Path.GetExtension(path),".json",StringComparison.Ordinal) || !Guid.TryParseExact(Path.GetFileNameWithoutExtension(path),"N",out id)) throw Invalid();
  CheckPrivate(root,true); CheckPrivate(path,false); return path;
 }
 static Dictionary<string,object> Data(Manifest m) {
  return new Dictionary<string,object> {
   {"oldPID",m.OldPid},{"startUTCticks",m.OldStart},{"oldExe",m.OldExe},{"newBundle",m.NewBundle},{"oldBundle",m.OldBundle},{"mode",m.Mode},{"runtime",m.Runtime},{"observerConfig",m.ObserverConfig},{"observer",m.Observer}
  };
 }
 internal static string WriteManifest(Manifest m,string root) {
  Validate(m); EnsureRoot(root);
  string path=Path.Combine(root,Guid.NewGuid().ToString("N")+".json");
  byte[] bytes=new UTF8Encoding(false,true).GetBytes(new JavaScriptSerializer().Serialize(Data(m)));
  if(bytes.Length>65536) throw Invalid();
  using(var f=new FileStream(path,FileMode.CreateNew,FileSystemRights.Write,FileShare.None,4096,FileOptions.WriteThrough,FileAcl())) { f.Write(bytes,0,bytes.Length); f.Flush(true); }
  OwnedPath(path,root); return path;
 }
 internal static Manifest ReadManifest(string path,string root) {
  path=OwnedPath(path,root);
  string text;
  using(var f=new FileStream(path,FileMode.Open,FileAccess.Read,FileShare.Read)) {
   if(f.Length==0 || f.Length>65536) throw Invalid();
   byte[] b=new byte[(int)f.Length]; int n=0,k;
   while(n<b.Length && (k=f.Read(b,n,b.Length-n))>0) n+=k;
   if(n!=b.Length) throw Invalid(); text=new UTF8Encoding(false,true).GetString(b);
  }
  var serializer=new JavaScriptSerializer { MaxJsonLength=65536, RecursionLimit=4 };
  var d=serializer.DeserializeObject(text) as Dictionary<string,object>;
  if(d==null || d.Count!=9) throw Invalid();
  foreach(string key in new[]{"oldPID","startUTCticks","oldExe","newBundle","oldBundle","mode","runtime","observerConfig","observer"}) if(!d.ContainsKey(key)) throw Invalid();
  // Reject duplicate keys, aliases, extra syntax, and noncanonical numeric representations.
  var m=new Manifest {
   OldPid=Convert.ToInt32(d["oldPID"],CultureInfo.InvariantCulture), OldStart=Convert.ToInt64(d["startUTCticks"],CultureInfo.InvariantCulture),
   OldExe=d["oldExe"] as string, NewBundle=d["newBundle"] as string, OldBundle=d["oldBundle"] as string, Mode=d["mode"] as string,
   Runtime=d["runtime"] as string, ObserverConfig=d["observerConfig"] as string, Observer=d["observer"] is bool && (bool)d["observer"]
  };
  if(serializer.Serialize(Data(m))!=text) throw Invalid();
  Validate(m); return m;
 }
 internal sealed class Prepared : IDisposable {
  readonly string path,root,helper;
  readonly IEnvironment environment;
  bool attempted,started,disposed;
  internal Prepared(string p,string r,string h,IEnvironment e) { path=p;root=r;helper=h;environment=e; }
  internal bool Start() {
   if(disposed || attempted) return false;
   attempted=true;
   try {
    ReadManifest(path,root); FileRequired(helper);
    started=environment.Start(Hidden(helper,"--manifest "+Quote(path))); return started;
   } catch { return false; }
  }
  public void Dispose() {
   if(disposed) return; disposed=true;
   if(!started) try { OwnedPath(path,root); File.Delete(path); } catch { }
  }
 }
 internal static Prepared Prepare(UpdateCandidate.Plan plan) { return PrepareForTest(plan,PrivateRoot,new RealEnvironment(),AppDomain.CurrentDomain.BaseDirectory); }
 internal static Prepared PrepareForTest(UpdateCandidate.Plan plan,string root,IEnvironment environment,string oldBundle) {
  if(plan==null) throw Invalid();
  var id=environment.Current;
  var m=new Manifest { OldPid=id.Pid,OldStart=id.Start,OldExe=id.Exe,OldBundle=oldBundle,NewBundle=plan.Bundle,Mode=plan.Mode,Runtime=plan.Runtime,ObserverConfig=plan.ObserverConfig,Observer=plan.Observer };
  string path=WriteManifest(m,root);
  return new Prepared(path,root,Path.Combine(SafePath(oldBundle),"xshupdater.exe"),environment);
 }
 internal static string Quote(string value) {
  if(value==null) throw Invalid();
  var b=new StringBuilder("\""); int slashes=0;
  foreach(char c in value) {
   if(Char.IsControl(c)) throw Invalid();
   if(c=='\\') { slashes++; continue; }
   b.Append('\\',c=='"' ? slashes*2+1 : slashes); b.Append(c); slashes=0;
  }
  b.Append('\\',slashes*2); return b.Append('"').ToString();
 }
 static ProcessStartInfo Hidden(string file,string args) {
  return new ProcessStartInfo { FileName=file,Arguments=args,UseShellExecute=false,CreateNoWindow=true,WindowStyle=ProcessWindowStyle.Hidden,WorkingDirectory=Path.GetDirectoryName(file) };
 }
 internal static bool RunCore(Manifest m,IEnvironment environment) {
  try {
   Validate(m);
   long start=environment.Milliseconds;
   while(true) {
    var id=environment.Find(m.OldPid);
    if(id==null) break;
    if(id.Pid!=m.OldPid || id.Start!=m.OldStart || !Same(SafePath(id.Exe),m.OldExe)) throw Invalid();
    if(environment.Milliseconds-start>=30000) throw Invalid();
    environment.Wait(100);
   }
   {
    start=environment.Milliseconds;
    string lockPath=Path.Combine(m.Runtime,"bridge.lock");
    while(environment.LockExists(lockPath)) {
     if(environment.Milliseconds-start>=30000) throw Invalid();
     environment.Wait(100);
    }
   }
   Validate(m);
   string powershell=Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.Windows),"System32\\WindowsPowerShell\\v1.0\\powershell.exe");
   FileRequired(powershell);
   string args="-NoProfile -ExecutionPolicy RemoteSigned -File "+Quote(Path.Combine(m.NewBundle,"scripts\\start-desktop.ps1"));
   args+=" -DataRoot "+Quote(Path.GetDirectoryName(m.Runtime));
   return environment.Start(Hidden(powershell,args));
  } catch { return false; }
 }
 static void LogFailure() {
  try {
   EnsureRoot(PrivateRoot);
   string p=Path.Combine(PrivateRoot,Guid.NewGuid().ToString("N")+".log");
   byte[] b=Encoding.UTF8.GetBytes("更新交接失败；旧版本目录已保留。未自动重试、回退或结束进程。\r\n");
   using(var f=new FileStream(p,FileMode.CreateNew,FileSystemRights.Write,FileShare.None,4096,FileOptions.WriteThrough,FileAcl())) f.Write(b,0,b.Length);
  } catch { }
 }
#if UPDATE_HANDOFF_EXE
 [STAThread]
 internal static int Main(string[] args) {
  bool ok=false;string retained=null;
  try {
   if(args==null || args.Length!=2 || args[0]!="--manifest") throw Invalid();
   string path=OwnedPath(args[1],PrivateRoot);
   // Consume once before waiting or starting anything. No automatic replay.
   Manifest m=ReadManifest(path,PrivateRoot);
   string self=new RealEnvironment().Current.Exe;
   if(!Same(SafePath(self),Path.Combine(m.OldBundle,"xshupdater.exe"))) throw Invalid();
   retained=m.OldBundle;
   File.Delete(path);
   ok=RunCore(m,new RealEnvironment());
  } catch { }
  if(!ok) {
   LogFailure();
   try {
    System.Windows.Forms.MessageBox.Show("更新交接失败；旧版本目录已保留。"+(retained==null?"":"\r\n旧版本目录："+retained)+"\r\nBridge 可能已关闭。请人工核对 Bridge 状态及运行目录的锁，再从保留目录手动启动。不会自动重试或回退，也未结束其他进程。","Xshell Bridge 更新",System.Windows.Forms.MessageBoxButtons.OK,System.Windows.Forms.MessageBoxIcon.Error);
   } catch { }
  }
  return ok ? 0 : 1;
 }
#endif
}
}
