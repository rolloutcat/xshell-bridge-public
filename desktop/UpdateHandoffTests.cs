using System;
using System.Diagnostics;
using System.IO;
using System.Security.AccessControl;
using System.Security.Principal;

namespace XshellDesktop {
// Compile together with UpdateCandidate and UpdateHandoff; select this explicit /main.
// No test uses RealEnvironment, starts a process, or invokes a Bridge API.
internal static class UpdateHandoffTests {
 sealed class Fake : UpdateHandoff.IEnvironment {
  internal UpdateHandoff.Identity Identity;
  internal long Time;
  internal int ExitAfter=200, UnlockAfter=300, Starts, Locks;
  internal bool Launch=true;
  internal ProcessStartInfo Info;
  public UpdateHandoff.Identity Current { get { return Identity; } }
  public UpdateHandoff.Identity Find(int pid) { return Time>=ExitAfter ? null : Identity; }
  public long Milliseconds { get { return Time; } }
  public void Wait(int milliseconds) { Time+=milliseconds; }
  public bool LockExists(string path) { Locks++; return Time<UnlockAfter; }
  public bool Start(ProcessStartInfo info) { Starts++; Info=info; return Launch; }
 }
 static void Assert(bool value,string name) { if(!value) throw new Exception(name); }
 static void Reject(Action action,string name) {
  bool rejected=false; try { action(); } catch { rejected=true; } Assert(rejected,name);
 }
 static void Bundle(string path) {
  Directory.CreateDirectory(Path.Combine(path,"scripts"));
  foreach(string f in new[]{"xshdesk.exe","xshctl.exe","xshupdater.exe","VERSION","scripts\\start-desktop.ps1"}) File.WriteAllText(Path.Combine(path,f),"test fixture, never executable");
 }
 static UpdateHandoff.Manifest Manifest(string root,string mode,bool observer) {
  return new UpdateHandoff.Manifest { OldPid=123,OldStart=123456,OldExe=Path.Combine(root,"old bundle\\xshdesk.exe"),OldBundle=Path.Combine(root,"old bundle"),NewBundle=Path.Combine(root,"new bundle"),Mode=mode,Observer=observer,Runtime=observer ? null : Path.Combine(root,"data space\\runtime"),ObserverConfig=observer ? Path.Combine(root,"original config.json") : null };
 }
 static Fake Env(UpdateHandoff.Manifest m) { return new Fake { Identity=new UpdateHandoff.Identity { Pid=m.OldPid,Start=m.OldStart,Exe=m.OldExe } }; }
 internal static int Main(string[] args) {
  string root=Path.Combine(Path.GetTempPath(),"handoff tests "+Guid.NewGuid().ToString("N"));
  try {
   Directory.CreateDirectory(root); Bundle(Path.Combine(root,"old bundle")); Bundle(Path.Combine(root,"new bundle"));
   string runtime=Path.Combine(root,"data space\\runtime"); Directory.CreateDirectory(runtime);
   foreach(string f in new[]{"bridge.json","client.json","agent.json","admin.json","client-host.json"}) File.WriteAllText(Path.Combine(runtime,f),"{}");
   File.WriteAllText(Path.Combine(root,"original config.json"),"{}");
   foreach(string mode in new[]{"Local"}) foreach(bool observer in new[]{false}) {
    var m=Manifest(root,mode,observer); var e=Env(m);
    Assert(UpdateHandoff.RunCore(m,e),"positive "+mode+observer);
    Assert(e.Starts==1 && e.Info.UseShellExecute==false && e.Info.CreateNoWindow,"hidden direct launch");
    Assert(e.Info.FileName.EndsWith("System32\\WindowsPowerShell\\v1.0\\powershell.exe",StringComparison.OrdinalIgnoreCase),"fixed powershell");
    Assert(!e.Info.Arguments.Contains("-Mode") && !e.Info.Arguments.Contains("-ClientConfig"),"host-only launcher arguments");
    Assert(e.Info.Arguments.Contains(UpdateHandoff.Quote(observer ? m.ObserverConfig : Path.GetDirectoryName(m.Runtime))),"original path and spaces");
    Assert(!observer || e.Locks==0,"observer never checks bridge");
   }
   Assert(!UpdateHandoff.RunCore(Manifest(root,"Vm",false), Env(Manifest(root,"Vm",false))),"legacy VM rejected");
   Assert(!UpdateHandoff.RunCore(Manifest(root,"Local",true), Env(Manifest(root,"Local",true))),"legacy observer rejected");
   var model=Manifest(root,"Local",false);
   var bad=Env(model); bad.Identity.Start++; Assert(!UpdateHandoff.RunCore(model,bad) && bad.Starts==0,"PID reuse start mismatch");
   bad=Env(model); bad.Identity.Exe=Path.Combine(root,"new bundle\\xshdesk.exe"); Assert(!UpdateHandoff.RunCore(model,bad) && bad.Starts==0,"PID path mismatch");
   bad=Env(model); bad.ExitAfter=Int32.MaxValue; Assert(!UpdateHandoff.RunCore(model,bad) && bad.Time==30000 && bad.Starts==0,"exit timeout");
   bad=Env(model); bad.UnlockAfter=Int32.MaxValue; Assert(!UpdateHandoff.RunCore(model,bad) && bad.Starts==0 && bad.Time==30200,"lock timeout");
   bad=Env(model); bad.Launch=false; Assert(!UpdateHandoff.RunCore(model,bad) && bad.Starts==1,"launch failure no retry");
   foreach(string p in new[]{"relative","C:relative","\\\\server\\share","C:\\bad\"name","C:\\bad\nname","C:\\file:stream"}) Reject(()=>UpdateHandoff.SafePath(p),"unsafe path");
   Assert(UpdateHandoff.Quote("a b\\")=="\"a b\\\\\"","trailing slash quoting");
   Assert(UpdateHandoff.Quote("a\"b")=="\"a\\\"b\"","embedded quote quoting");
   string privateRoot=Path.Combine(root,"private");
   bool aclAvailable=true; string manifest=null;
   try { manifest=UpdateHandoff.WriteManifest(model,privateRoot); }
   catch(UnauthorizedAccessException) { aclAvailable=false; Console.WriteLine("SKIP ACL: permission unavailable"); }
   if(aclAvailable) {
    Assert(UpdateHandoff.ReadManifest(manifest,privateRoot).OldPid==123,"roundtrip");
    string text=File.ReadAllText(manifest);
    Assert(!text.Contains("UpdateID") && !text.Contains("Ticket"),"no token schema");
    File.WriteAllText(manifest,text.Replace("\"mode\":\"Local\"","\"mode\":\"Bad\""));
    Reject(()=>UpdateHandoff.ReadManifest(manifest,privateRoot),"bad mode");
    File.WriteAllText(manifest,new string(' ',65537)); Reject(()=>UpdateHandoff.ReadManifest(manifest,privateRoot),"size bound");
    File.WriteAllText(manifest,text);
    var acl=File.GetAccessControl(manifest);
    acl.AddAccessRule(new FileSystemAccessRule(new SecurityIdentifier(WellKnownSidType.WorldSid,null),FileSystemRights.Read,AccessControlType.Allow));
    File.SetAccessControl(manifest,acl);
    Reject(()=>UpdateHandoff.ReadManifest(manifest,privateRoot),"public allow rejected");
    File.Delete(manifest);
    var plan=new UpdateCandidate.Plan { Bundle=model.NewBundle,Mode=model.Mode,Runtime=model.Runtime };
    var fake=Env(model);
    using(var prepared=UpdateHandoff.PrepareForTest(plan,privateRoot,fake,model.OldBundle)) { Assert(fake.Starts==0,"prepare never launches"); }
    Assert(Directory.GetFiles(privateRoot,"*.json").Length==0,"dispose owns manifest");
    using(var prepared=UpdateHandoff.PrepareForTest(plan,privateRoot,fake,model.OldBundle)) {
     Assert(prepared.Start(),"prepared start"); Assert(!prepared.Start(),"one attempt only");
     Assert(fake.Info.FileName==Path.Combine(model.OldBundle,"xshupdater.exe"),"current trusted helper");
     Assert(fake.Info.Arguments.StartsWith("--manifest \"",StringComparison.Ordinal),"manifest only argument");
    }
    Assert(Directory.GetFiles(privateRoot,"*.json").Length==1,"started manifest retained for helper");
   }
   Console.WriteLine("UpdateHandoff tests passed; no processes launched."); return 0;
  } catch(Exception e) { Console.Error.WriteLine(e); return 1; }
  finally { if(Directory.Exists(root)) Directory.Delete(root,true); }
 }
}
}
