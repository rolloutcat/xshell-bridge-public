using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.IO.Compression;
using System.Linq;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Security.Cryptography;
using System.Text;
using System.Text.RegularExpressions;
using System.Threading;
using System.Threading.Tasks;
using System.Web.Script.Serialization;
namespace XshellDesktop {
public sealed class Updates {
 public const string Repository="https://github.com/rolloutcat/xshell-bridge-public";
 readonly string root,version;
 readonly UpdateCandidate candidate=new UpdateCandidate();
 public void InvalidateApply(){candidate.Invalidate();}
 public object PrepareApply(bool observer,string mode,string runtime,string observerConfig){
  var p=candidate.PrepareApply(observer,mode,runtime,observerConfig);
  return new{ticket=p.Ticket,expires=p.Expires.ToString("o"),update_id=p.Plan.UpdateID,version=p.Plan.Version,bundle_path=p.Plan.Bundle,launcher_path=Path.Combine(p.Plan.Bundle,"xshdesk.exe"),observer=p.Plan.Observer,mode=p.Plan.Mode,runtime=p.Plan.Runtime,observer_config=p.Plan.ObserverConfig};
 }
 internal void ConfirmApply(string ticket,bool observer,string mode,string runtime,string observerConfig,Action<UpdateCandidate.Plan> handoff){candidate.ConfirmApply(ticket,observer,mode,runtime,observerConfig,handoff);}
 readonly Func<string,string,Task<string>> run;
 public Updates():this(Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.LocalApplicationData),"XshellBridge-updates"),ReadVersion(),null){}
 public Updates(string managedRoot,string current,Func<string,string,Task<string>> runner){root=Path.GetFullPath(managedRoot);version=current;run=runner??RunGit;}
 public static string ReadVersion(){try{using(var s=typeof(Updates).Assembly.GetManifestResourceStream("XshellDesktop.VERSION"))using(var r=new StreamReader(s)){return r.ReadToEnd().Trim();}}catch{return "unknown";}}
 public object Status(){return new{current=version,repository=Repository,notice="仅显式点击才联网或写文件；源码与 ZIP 均暂存，不替换运行中的程序。"};}
 static string Plain(string s,int max){if(s==null)return "";return new string(s.Take(max).Where(c=>!char.IsControl(c)||c=='\n'||c=='\t').ToArray());}
 static string Text(Dictionary<string,object> m,string k){object v;return m.TryGetValue(k,out v)&&v is string?(string)v:"";}
 public static bool ReleaseUrl(string text){Uri u;return Uri.TryCreate(text,UriKind.Absolute,out u)&&u.Scheme=="https"&&u.Host=="github.com"&&u.IsDefaultPort&&u.UserInfo==""&&u.Query==""&&u.Fragment==""&&Regex.IsMatch(u.AbsolutePath,@"\A/rolloutcat/xshell-bridge-public/releases/tag/[A-Za-z0-9._+-]+\z");}
 public async Task<object> Check(){
  try{using(var h=new HttpClientHandler{AllowAutoRedirect=false})using(var c=new HttpClient(h)){c.Timeout=TimeSpan.FromSeconds(8);c.MaxResponseContentBufferSize=1024*1024;
   using(var q=new HttpRequestMessage(HttpMethod.Get,"https://api.github.com/repos/rolloutcat/xshell-bridge-public/releases/latest")){
    q.Headers.UserAgent.ParseAdd("XshellBridge-updates/1");q.Headers.Accept.ParseAdd("application/vnd.github+json");
    var token=Environment.GetEnvironmentVariable("GH_TOKEN");if(string.IsNullOrEmpty(token))token=Environment.GetEnvironmentVariable("GITHUB_TOKEN");
    if(!string.IsNullOrEmpty(token))q.Headers.Authorization=new AuthenticationHeaderValue("Bearer",token);
    using(var r=await c.SendAsync(q).ConfigureAwait(false)){
     if((int)r.StatusCode==404)return new{status="unavailable",message="无法获取发布：私有仓库无访问权限，或尚未创建 release。不是已是最新版本。"};
     if(!r.IsSuccessStatusCode)return new{status="unavailable",message="GitHub 请求失败（HTTP "+(int)r.StatusCode+"）。"};
     var m=new JavaScriptSerializer{MaxJsonLength=1024*1024}.DeserializeObject(await r.Content.ReadAsStringAsync().ConfigureAwait(false)) as Dictionary<string,object>;
     if(m==null)throw new InvalidDataException();var url=Text(m,"html_url");if(!ReleaseUrl(url))throw new InvalidDataException();
     var tag=Plain(Text(m,"tag_name"),128);var assets=new List<string>();object a;
     if(m.TryGetValue("assets",out a)&&a is object[])foreach(var item in (object[])a){var asset=item as Dictionary<string,object>;if(asset==null)continue;var name=Text(asset,"name");if(name=="xshell-bridge-portable-windows-x64-full.zip"||name=="xshell-bridge-portable-windows-x64-lite.zip")assets.Add(name);}
     int? comparison=Compare(tag,version);return new{status=comparison==null?"unknown":comparison>0?"available":comparison==0?"current":"older",tag=tag,body=Plain(Text(m,"body"),8192),release_url=url,assets=assets.ToArray()};
    }
   }
  }}catch{return new{status="unavailable",message="GitHub 查询超时、网络不可用或响应无效；未确认是否有更新。"};}
 }
 sealed class Sem {public string[] n,p;}
 static Sem Parse(string s){var m=Regex.Match(s??"",@"\Av?(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?\z");if(!m.Success)return null;var p=m.Groups[4].Success?m.Groups[4].Value.Split('.'):new string[0];if(p.Any(x=>Regex.IsMatch(x,@"\A[0-9]+\z")&&x.Length>1&&x[0]=='0'))return null;return new Sem{n=new[]{m.Groups[1].Value,m.Groups[2].Value,m.Groups[3].Value},p=p};}
 static int Numeric(string a,string b){return a.Length!=b.Length?a.Length.CompareTo(b.Length):string.CompareOrdinal(a,b);}
 public static int? Compare(string left,string right){var a=Parse(left);var b=Parse(right);if(a==null||b==null)return null;for(int i=0;i<3;i++){int c=Numeric(a.n[i],b.n[i]);if(c!=0)return c;}if(a.p.Length==0||b.p.Length==0)return a.p.Length==b.p.Length?0:a.p.Length==0?1:-1;for(int i=0;i<Math.Min(a.p.Length,b.p.Length);i++){var x=a.p[i];var y=b.p[i];bool nx=Regex.IsMatch(x,@"\A[0-9]+\z"),ny=Regex.IsMatch(y,@"\A[0-9]+\z");int c=nx&&ny?Numeric(x,y):nx!=ny?(nx?-1:1):string.CompareOrdinal(x,y);if(c!=0)return c;}return a.p.Length.CompareTo(b.p.Length);}
 static void NoReparse(string path){for(var p=Path.GetFullPath(path);!string.IsNullOrEmpty(p);p=Path.GetDirectoryName(p)){if((File.Exists(p)||Directory.Exists(p))&&(File.GetAttributes(p)&FileAttributes.ReparsePoint)!=0)throw new InvalidOperationException("路径包含重解析点。");}}
 string Fresh(string prefix){NoReparse(root);Directory.CreateDirectory(root);var p=Path.Combine(root,prefix+Guid.NewGuid().ToString("N"));Directory.CreateDirectory(p);NoReparse(p);return p;}
 void RemoveOwned(string p){if(p==null)return;var full=Path.GetFullPath(p);if(Path.GetDirectoryName(full)!=root||!Regex.IsMatch(Path.GetFileName(full),@"\A(?:source|portable)-[0-9a-f]{32}\z"))return;try{NoReparse(full);if(Directory.Exists(full)){if(Directory.GetFileSystemEntries(full,"*",SearchOption.AllDirectories).Any(x=>(File.GetAttributes(x)&FileAttributes.ReparsePoint)!=0))return;Directory.Delete(full,true);}}catch{}}
 public async Task<object> Source(){string p=null;try{p=Fresh("source-");await run(root,"clone --no-checkout "+Repository+" "+Quote(p)).ConfigureAwait(false);await run(root,"-C "+Quote(p)+" checkout --detach origin/main").ConfigureAwait(false);var commit=(await run(root,"-C "+Quote(p)+" rev-parse HEAD").ConfigureAwait(false)).Trim();if(!Regex.IsMatch(commit,@"\A[0-9a-fA-F]{40}\z"))throw new InvalidDataException();return new{source_path=p,commit=commit,message="未构建或执行源码。人工审查后手动运行 scripts\\build-portable.ps1；不修改旧仓库，不重启、不重放任务。"};}catch{RemoveOwned(p);throw new InvalidOperationException("获取源码失败或超时。请检查 Git 安装、现有凭据助手和仓库权限；未显示 Git 原始输出。");}}
 static string Quote(string s){if(s.Contains("\"")||s.EndsWith("\\"))throw new InvalidOperationException("路径无效。");return "\""+s+"\"";}
 static string FindGit(){foreach(var entry in (Environment.GetEnvironmentVariable("PATH")??"").Split(';')){var d=entry.Trim().Trim('"');if(!Path.IsPathRooted(d))continue;try{var p=Path.Combine(d,"git.exe");if(File.Exists(p)){NoReparse(p);return p;}}catch{}}throw new InvalidOperationException("找不到 git.exe。");}
 static async Task<string> RunGit(string cwd,string arguments){var exe=FindGit();using(var p=new Process()){p.StartInfo=new ProcessStartInfo(exe,arguments){WorkingDirectory=cwd,UseShellExecute=false,CreateNoWindow=true,WindowStyle=ProcessWindowStyle.Hidden,RedirectStandardOutput=true,RedirectStandardError=true};p.StartInfo.EnvironmentVariables["GIT_TERMINAL_PROMPT"]="0";p.Start();var stdout=Drain(p.StandardOutput);var stderr=Drain(p.StandardError);var exit=Task.Run(()=>p.WaitForExit());if(await Task.WhenAny(exit,Task.Delay(120000)).ConfigureAwait(false)!=exit){try{p.Kill();}catch{}throw new InvalidOperationException("Git 超时。");}await exit.ConfigureAwait(false);var text=await stdout.ConfigureAwait(false);await stderr.ConfigureAwait(false);if(p.ExitCode!=0)throw new InvalidOperationException("Git 失败。");return text;}}
 static async Task<string> Drain(StreamReader r){var b=new char[4096];var s=new StringBuilder();int n;while((n=await r.ReadAsync(b,0,b.Length).ConfigureAwait(false))>0){if(s.Length<4096)s.Append(b,0,Math.Min(n,4096-s.Length));}return s.ToString();}
 public Task<object> Import(string path,string hash){return Task.Run<object>(()=>ImportZip(path,hash));}
 public object ImportZip(string path,string hash){string stage=null;try{
  if(!Regex.IsMatch(hash??"",@"\A[0-9a-fA-F]{64}\z")||string.IsNullOrEmpty(path)||!Regex.IsMatch(path,@"\A[A-Za-z]:[\\/]"))throw new InvalidDataException();path=Path.GetFullPath(path);NoReparse(path);
  using(var f=new FileStream(path,FileMode.Open,FileAccess.Read,FileShare.Read)){
   string actual;using(var sha=SHA256.Create())actual=BitConverter.ToString(sha.ComputeHash(f)).Replace("-","");if(!string.Equals(actual,hash,StringComparison.OrdinalIgnoreCase))throw new InvalidDataException();f.Position=0;
   using(var zip=new ZipArchive(f,ZipArchiveMode.Read,true)){
    if(zip.Entries.Count==0||zip.Entries.Count>10000)throw new InvalidDataException();var names=new HashSet<string>(StringComparer.OrdinalIgnoreCase);string top=null;long total=0;var required=new HashSet<string>(StringComparer.OrdinalIgnoreCase);
    foreach(var e in zip.Entries){var n=e.FullName;if(n.Length==0||n.Contains("\\")||n.Contains(":")||n.StartsWith("/")||((e.ExternalAttributes>>16)&0xF000)==0xA000||(e.ExternalAttributes&0x400)!=0)throw new InvalidDataException();var parts=n.TrimEnd('/').Split('/');if(parts.Length==0||parts.Any(x=>x==""||x=="."||x==".."||x.EndsWith(".")||x.EndsWith(" ")||x.IndexOfAny(Path.GetInvalidFileNameChars())>=0||Regex.IsMatch(x,@"\A(?:CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(?:\.|$)",RegexOptions.IgnoreCase)))throw new InvalidDataException();if(!names.Add(string.Join("/",parts)))throw new InvalidDataException();if(top==null)top=parts[0];if(parts[0]!=top)throw new InvalidDataException();if(parts.Any(x=>new[]{"runtime",".env","admin.json","client.json","agent.json","bridge.json","state.json"}.Contains(x,StringComparer.OrdinalIgnoreCase)))throw new InvalidDataException();if(e.Length<0||e.Length>2147483648L-total)throw new InvalidDataException();total+=e.Length;if(parts.Length==2&&!n.EndsWith("/"))required.Add(parts[1]);}
    if(!new[]{"xshdesk.exe","xshctl.exe","VERSION"}.All(required.Contains))throw new InvalidDataException();stage=Fresh("portable-");
    foreach(var e in zip.Entries){var dest=Path.GetFullPath(Path.Combine(stage,e.FullName.Replace('/',Path.DirectorySeparatorChar)));if(!dest.StartsWith(stage+Path.DirectorySeparatorChar,StringComparison.OrdinalIgnoreCase))throw new InvalidDataException();NoReparse(dest);if(e.FullName.EndsWith("/")){Directory.CreateDirectory(dest);continue;}Directory.CreateDirectory(Path.GetDirectoryName(dest));using(var input=e.Open())using(var output=new FileStream(dest,FileMode.CreateNew,FileAccess.Write,FileShare.None)){var b=new byte[65536];long count=0;int n;while((n=input.Read(b,0,b.Length))>0){count+=n;if(count>e.Length)throw new InvalidDataException();output.Write(b,0,n);}if(count!=e.Length)throw new InvalidDataException();}}
    candidate.AcceptImported(Path.Combine(stage,top));
    return candidate.ImportedResult();
   }
  }
 }catch{RemoveOwned(stage);throw new InvalidOperationException("ZIP 导入失败：请核对绝对路径、SHA256、包结构与安全限制。未修改运行中的程序或私有配置。");}}
}
}
