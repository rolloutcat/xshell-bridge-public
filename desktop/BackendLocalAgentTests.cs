using System;
using System.Collections.Generic;
using System.Collections;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Threading.Tasks;
using System.Web.Script.Serialization;

namespace XshellDesktop {
internal static class BackendLocalAgentTests {
    static readonly JavaScriptSerializer json = new JavaScriptSerializer();
    static void Check(bool ok, string message) { if (!ok) throw new Exception(message); }
    static Dictionary<string,object> Map(object value) { return (Dictionary<string,object>)json.DeserializeObject(json.Serialize(value)); }
    static void Set(Backend b, string field, object value) { typeof(Backend).GetField(field, BindingFlags.NonPublic | BindingFlags.Instance).SetValue(b, value); }
    sealed class Fixture : IDisposable {
        internal string root, runtime, bundle; internal Backend backend; internal bool capability = true;
        internal Fixture(bool readOnly = false, string mode = "Local", bool original = true) {
            root=Path.Combine(Path.GetTempPath(), "local-ipc-"+Guid.NewGuid().ToString("N")); runtime=Path.Combine(root,"runtime"); bundle=Path.Combine(root,"bundle");
            Directory.CreateDirectory(runtime); Directory.CreateDirectory(bundle); Config("agent.json", 17890, 'a'); Config("client.json", 17890, 'c');
            foreach (string name in new[] { "xshell_agent.js", "remote_bootstrap.js", "log_parser.js" }) File.WriteAllText(Path.Combine(bundle,name), "// fixture " + name);
            File.WriteAllBytes(Path.Combine(bundle,"XshLogCapture.exe"), Enumerable.Repeat((byte)42,2048).ToArray());
            var reverse=new HostReverse(Path.Combine(root,"catalog"),bundle,runtime);
            backend=new Backend(Call,readOnly,mode,original?runtime:null,"http://127.0.0.1:17890/",reverse,null);
        }
        Task<object> Call(string method,string path,object body) { Check(method=="GET" && path=="/v1/health", "unexpected network action"); return Task.FromResult<object>(Map(new { source_agents=capability })); }
        internal void Config(string file,int port,char token) { File.WriteAllText(Path.Combine(runtime,file),json.Serialize(new { endpoint="http://127.0.0.1:"+port, token=new string(token,48) })); }
        internal Dictionary<string,object> Request(object request,bool ok) {
            var response=Map(backend.Handle(json.Serialize(request)).GetAwaiter().GetResult());
            Check((bool)response["ok"]==ok, "unexpected IPC response: "+json.Serialize(response));
            string wire=json.Serialize(response); Check(!wire.Contains(new string('a',48))&&!wire.Contains(new string('c',48))&&!wire.Contains("helper_base64")&&!wire.Contains("XSH_REMOTE_BUNDLE"), "secret or script leaked"); return response;
        }
        internal string Prepare() { return (string)Map(Request(new { id=1,op="localAgentPrepare",environment="fixture",log_encoding="auto" },true)["data"])["ticket"]; }
        internal void Confirm(string ticket,bool ok) { Request(new { id=2,op="localAgentConfirm",ticket=ticket },ok); }
        public void Dispose() { backend.Dispose(); }
    }
    static void Main() { try { Run(); Console.WriteLine("PASS: local agent IPC isolation"); } catch(Exception e) { Console.Error.WriteLine(e.ToString()); Environment.ExitCode=1; } }
    static void Run() {
        using(var f=new Fixture()) {
            Check((string)Map(f.Request(new { id=1,op="localAgentStatus" },true)["data"])["status"]=="absent", "status");
            string ticket=f.Prepare(); f.Confirm(ticket,true); f.Confirm(ticket,false);
            Check((string)Map(f.Request(new { id=1,op="localAgentStatus" },true)["data"])["status"]=="current", "current");
            ticket=f.Prepare(); f.Config("agent.json",17890,'b'); f.Confirm(ticket,false); f.Confirm(ticket,false);
        }
        using(var f=new Fixture()) {
            string ticket=f.Prepare();
            var reviews=(IDictionary)typeof(Backend).GetField("reviews",BindingFlags.NonPublic|BindingFlags.Instance).GetValue(f.backend);
            object review=reviews[ticket]; review.GetType().GetField("Expires").SetValue(review,DateTime.UtcNow.AddSeconds(-1));
            f.Confirm(ticket,false); f.Confirm(ticket,false);
            ticket=f.Prepare(); File.AppendAllText(Path.Combine(f.bundle,"xshell_agent.js"),"changed"); f.Confirm(ticket,false);
            f.Request(new { id=1,op="localAgentPrepare",environment="fixture",log_encoding="auto",path="C:\\malicious.js" },false);
        }
        using(var f=new Fixture()) {
            File.WriteAllText(Path.Combine(f.runtime,"agent.json"),json.Serialize(new { endpoint="http://[::1]:17890",token=new string('a',48) }));
            File.WriteAllText(Path.Combine(f.runtime,"client.json"),json.Serialize(new { endpoint="http://[::1]:17890",token=new string('c',48) }));
            Set(f.backend,"connectionEndpoint","http://[::1]:17890/");
            var reverse=(HostReverse)typeof(Backend).GetField("reverse",BindingFlags.NonPublic|BindingFlags.Instance).GetValue(f.backend);
            Check(reverse.PairingEndpoint()=="http://[::1]:17890", "IPv6 pairing");
            var client=ConnectionProfiles.ReadClient(Path.Combine(f.runtime,"client.json")); var agent=ConnectionProfiles.ReadClient(Path.Combine(f.runtime,"agent.json"));
            Check(client.Endpoint.AbsoluteUri==agent.Endpoint.AbsoluteUri && client.Endpoint.AbsoluteUri==new Uri("http://[::1]:17890/").AbsoluteUri,"IPv6 client pairing");
            new LocalAgent(f.runtime,reverse,"0.8.5").Prepare("fixture","auto");
            string ticket=f.Prepare(); f.Confirm(ticket,true);
            var status=Map(f.Request(new { id=1,op="localAgentStatus" },true)["data"]);
            Check((string)status["endpoint"]=="http://[::1]:17890" && File.ReadAllText((string)status["path"]).Contains("http://[::1]:17890"),"IPv6 local endpoint");
        }
        using(var f=new Fixture()) {
            File.WriteAllText(Path.Combine(f.runtime,"agent.json"),json.Serialize(new { endpoint="HTTP://127.0.0.1/",token=new string('a',48) }));
            File.WriteAllText(Path.Combine(f.runtime,"client.json"),json.Serialize(new { endpoint="http://127.0.0.1",token=new string('c',48) }));
            Set(f.backend,"connectionEndpoint","http://127.0.0.1/");
            string ticket=f.Prepare(); f.Confirm(ticket,true);
            Check((string)Map(f.Request(new { id=1,op="localAgentStatus" },true)["data"])["endpoint"]=="http://127.0.0.1:80", "default loopback port");
        }
        foreach(string variant in new[] { "readonly", "mode", "missing", "selected", "endpoint", "pairing", "capability" }) {
            using(var f=new Fixture(variant=="readonly",variant=="mode"?"Remote":"Local",variant!="missing")) {
                if(variant=="selected") Set(f.backend,"selectedProfile","another");
                if(variant=="endpoint") Set(f.backend,"connectionEndpoint","http://127.0.0.1:17891/");
                if(variant=="pairing") f.Config("agent.json",17891,'a');
                if(variant=="capability") f.capability=false;
                f.Request(new { id=1,op="localAgentStatus" },false);
                f.Request(new { id=1,op="localAgentPrepare",environment="fixture",log_encoding="auto" },false);
            }
        }
        using(var f=new Fixture()) { string ticket=f.Prepare(); Set(f.backend,"connectionEndpoint","http://127.0.0.1:17891/"); f.Confirm(ticket,false); }
    }
}
}
