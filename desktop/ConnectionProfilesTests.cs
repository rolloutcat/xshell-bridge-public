using System;
using System.IO;
namespace XshellDesktop {
class ConnectionProfilesTests {
 static void Reject(Action a) { try { a(); } catch (InvalidOperationException) { return; } throw new Exception("accepted unsafe config"); }
 static void Main() {
  string root=Path.Combine(Path.GetTempPath(),"client-config-"+Guid.NewGuid().ToString("N"));Directory.CreateDirectory(root);
  try {
   string path=Path.Combine(root,"client.json");
   File.WriteAllText(path,"{\"endpoint\":\"http://127.0.0.1:18765\",\"token\":\""+new string('a',40)+"\"}");
   if(ConnectionProfiles.ReadClient(path).Endpoint.Port!=18765) throw new Exception("valid config");
   File.WriteAllText(path,"{\"endpoint\":\"http://[::1]:18765\",\"token\":\""+new string('a',40)+"\"}");
   if(ConnectionProfiles.ReadClient(path).Endpoint.Port!=18765) throw new Exception("valid IPv6 loopback config");
   Reject(()=>ConnectionProfiles.ReadClient("relative.json"));
   foreach(string endpoint in new[]{"http://example.com","http://127.1:18765","http://[::2]:18765","http://127.0.0.1:18765/a/..","http://127.0.0.1:18765/?q=x","http://user@127.0.0.1:18765"}) {
    File.WriteAllText(path,"{\"endpoint\":\""+endpoint+"\",\"token\":\""+new string('a',40)+"\"}"); Reject(()=>ConnectionProfiles.ReadClient(path));
   }
   Console.WriteLine("PASS: private client config path and loopback validation");
  } finally {Directory.Delete(root,true);}
 }
}
}
