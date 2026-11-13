using System;
using System.IO;
using System.IO.Compression;
using System.Security.Cryptography;
using System.Threading.Tasks;
using XshellDesktop;
class UpdatesTests {
 static void Assert(bool b){if(!b)throw new Exception("Assertion failed");}
 static void Reject(Action a){try{a();}catch(InvalidOperationException){return;}throw new Exception("Expected rejection");}
 static string Hash(string p){using(var f=File.OpenRead(p))using(var s=SHA256.Create())return BitConverter.ToString(s.ComputeHash(f)).Replace("-","");}
 static void Zip(string p,string extra,bool duplicate){using(var f=File.Create(p))using(var z=new ZipArchive(f,ZipArchiveMode.Create)){foreach(var n in new[]{"bundle/xshdesk.exe","bundle/xshctl.exe","bundle/VERSION"})using(var w=new StreamWriter(z.CreateEntry(n).Open()))w.Write("synthetic");if(extra!=null)using(var w=new StreamWriter(z.CreateEntry(extra).Open()))w.Write("test");if(duplicate)z.CreateEntry("bundle/VERSION");}}
 static void Main(){var root=Path.Combine(Path.GetTempPath(),"updates-tests-"+Guid.NewGuid().ToString("N"));Directory.CreateDirectory(root);try{
 Assert(Updates.Compare("v0.3.0","0.3.0")==0);Assert(Updates.Compare("0.10.0","0.3.0")>0);Assert(Updates.Compare("0.3.0-rc.10","0.3.0-rc.2")>0);Assert(Updates.Compare("0.3.0-rc.1","0.3.0")<0);Assert(Updates.Compare("0.3.0-01","0.3.0")==null);Assert(!Updates.ReleaseUrl("https://github.com/evil/repo/releases/tag/v1"));
 int calls=0;var u=new Updates(Path.Combine(root,"managed"),"0.3.0",(cwd,args)=>{calls++;return Task.FromResult(args.Contains("rev-parse")?new string('a',40):"");});u.Status();Assert(calls==0);u.Source().GetAwaiter().GetResult();Assert(calls==3);
 var zip=Path.Combine(root,"test.zip");Zip(zip,null,false);u.ImportZip(zip,Hash(zip));Reject(()=>u.ImportZip(zip,new string('0',64)));
 foreach(var bad in new[]{"bundle/../evil","bundle/runtime/a","bundle/sub/admin.json","bundle/.env","/absolute","bundle/a:b","bundle/a\\b","bundle/VERSION"}){Zip(zip,bad,false);Reject(()=>u.ImportZip(zip,Hash(zip)));}Zip(zip,null,true);Reject(()=>u.ImportZip(zip,Hash(zip)));
 // Patch central-directory uncompressed size; rejection occurs before extraction.
 Zip(zip,null,false);var bytes=File.ReadAllBytes(zip);for(int i=0;i<bytes.Length-28;i++)if(bytes[i]==0x50&&bytes[i+1]==0x4b&&bytes[i+2]==1&&bytes[i+3]==2){bytes[i+24]=255;bytes[i+25]=255;bytes[i+26]=255;bytes[i+27]=127;break;}File.WriteAllBytes(zip,bytes);Reject(()=>u.ImportZip(zip,Hash(zip)));
 Console.WriteLine("Updates tests passed (no network/Git execution).");
 }finally{Directory.Delete(root,true);}}
}
