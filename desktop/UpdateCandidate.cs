using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Security.Cryptography;
using System.Text.RegularExpressions;

namespace XshellDesktop {
// This class owns no process APIs and accepts no page-provided executable path.
// AcceptImported must only be called by Updates after successful extraction.
internal sealed class UpdateCandidate {
 internal sealed class Plan {
  internal string UpdateID,Bundle,Version,Runtime,Mode,ObserverConfig;
  internal bool Observer;
 }
 internal sealed class Preparation {
  internal string Ticket;
  internal DateTime Expires;
  internal Plan Plan;
 }
 readonly object sync=new object();
 readonly Func<DateTime> clock;
 Dictionary<string,string> hashes;
 string bundle,version,updateID;
 Preparation pending;
 internal UpdateCandidate():this(()=>DateTime.UtcNow){}
 internal UpdateCandidate(Func<DateTime> utcNow){clock=utcNow;}
 static InvalidOperationException Invalid(){return new InvalidOperationException("更新确认无效、已过期、文件已变化或上下文不匹配；没有执行更新。请重新导入并核对。");}
 static string Token(){byte[] b=new byte[32];using(var r=RandomNumberGenerator.Create())r.GetBytes(b);return BitConverter.ToString(b).Replace("-","").ToLowerInvariant();}
 static void Safe(string path){
  for(string p=Path.GetFullPath(path);!String.IsNullOrEmpty(p);p=Path.GetDirectoryName(p))
   if((File.Exists(p)||Directory.Exists(p))&&(File.GetAttributes(p)&FileAttributes.ReparsePoint)!=0)throw Invalid();
 }
 static string Full(string p){
  if(String.IsNullOrEmpty(p)||!Path.IsPathRooted(p)||p.Any(char.IsControl)||p.Contains("\""))throw Invalid();
  string full=Path.GetFullPath(p);Safe(full);return full;
 }
 static string Hash(string file){Safe(file);using(var f=new FileStream(file,FileMode.Open,FileAccess.Read,FileShare.Read))using(var s=SHA256.Create())return Convert.ToBase64String(s.ComputeHash(f));}
 // Walk explicitly, checking each directory before descending: never follow a
 // reparse point via SearchOption.AllDirectories.
 static Dictionary<string,string> Inventory(string root){
  Safe(root);
  var result=new Dictionary<string,string>(StringComparer.OrdinalIgnoreCase);
  var stack=new Stack<string>();stack.Push(root);
  while(stack.Count!=0){
   string dir=stack.Pop();Safe(dir);
   foreach(string entry in Directory.GetFileSystemEntries(dir)){
    Safe(entry);
    if(Directory.Exists(entry))stack.Push(entry);
    else result.Add(entry.Substring(root.Length+1),Hash(entry));
   }
  }
  return result;
 }
 internal void AcceptImported(string validatedBundle){
  lock(sync){
   pending=null;hashes=null;bundle=null;version=null;updateID=null;
   string path=Full(validatedBundle).TrimEnd(Path.DirectorySeparatorChar);
   var inventory=Inventory(path);
   string text=File.ReadAllText(Path.Combine(path,"VERSION")).Trim();
   if(text.Length>128||text.Any(char.IsControl))throw Invalid();
   bundle=path;hashes=inventory;version=text;updateID=Token();
  }
 }
 internal object ImportedResult(){lock(sync){if(hashes==null)throw Invalid();return new{update_id=updateID,version=version,bundle_path=bundle,launcher_path=Path.Combine(bundle,"xshdesk.exe"),message="已验证并暂存；未执行任何文件。"};}}
 internal void Invalidate(){lock(sync){pending=null;}}
 void ValidateFiles(){
  if(hashes==null||bundle==null)throw Invalid();
  foreach(string name in new[]{"xshdesk.exe","xshctl.exe","VERSION","scripts/start-desktop.ps1","xshupdater.exe"})
   if(!hashes.ContainsKey(name.Replace('/',Path.DirectorySeparatorChar)))throw Invalid();
  if(!Regex.IsMatch(version??"",@"\Av?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][0-9A-Za-z.+-]+)?\z"))throw Invalid();
  var current=Inventory(bundle);
  if(current.Count!=hashes.Count||hashes.Any(x=>!current.ContainsKey(x.Key)||current[x.Key]!=x.Value))throw Invalid();
 }
 Plan Context(bool observer,string mode,string runtime,string observerConfig){
  if(mode!="Local" || observer || observerConfig!=null)throw Invalid();
  runtime=Full(runtime);if(!String.Equals(Path.GetFileName(runtime.TrimEnd(Path.DirectorySeparatorChar)),"runtime",StringComparison.OrdinalIgnoreCase))throw Invalid();
  return new Plan{UpdateID=updateID,Bundle=bundle,Version=version,Observer=observer,Mode=mode,Runtime=runtime,ObserverConfig=observerConfig};
 }
 static bool Same(Plan a,Plan b){return a.UpdateID==b.UpdateID&&a.Bundle==b.Bundle&&a.Version==b.Version&&a.Observer==b.Observer&&a.Mode==b.Mode&&a.Runtime==b.Runtime&&a.ObserverConfig==b.ObserverConfig;}
 internal Preparation PrepareApply(bool observer,string mode,string runtime,string observerConfig){
  lock(sync){pending=null;ValidateFiles();var p=new Preparation{Ticket=Token(),Expires=clock().AddMinutes(2),Plan=Context(observer,mode,runtime,observerConfig)};pending=p;return new Preparation{Ticket=p.Ticket,Expires=p.Expires,Plan=Context(observer,mode,runtime,observerConfig)};}
 }
 // Consumed before validation or calling the injected native coordinator. An
 // uncertain mutation/launch result must never be retried with the same ticket.
 internal void ConfirmApply(string ticket,bool observer,string mode,string runtime,string observerConfig,Action<Plan> handoff){
  Plan plan;
  lock(sync){
   var p=pending;pending=null;
   if(p==null||p.Ticket!=ticket||clock()>=p.Expires||handoff==null)throw Invalid();
   ValidateFiles();plan=Context(observer,mode,runtime,observerConfig);
   if(!Same(p.Plan,plan))throw Invalid();
  }
  handoff(plan);
 }
}
}
