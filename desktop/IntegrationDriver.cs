// Test-only executable. Never included in the desktop distribution.
using System;
using System.Text;
using System.Web.Script.Serialization;
namespace XshellDesktop {
internal static class IntegrationDriver {
    static void Main(string[] args) {
        Console.InputEncoding=new UTF8Encoding(false);Console.OutputEncoding=new UTF8Encoding(false);
        var json=new JavaScriptSerializer{MaxJsonLength=4*1024*1024};
        using(var backend=new Backend(args)) {
            string line;while((line=Console.ReadLine())!=null) {
                Console.WriteLine(json.Serialize(backend.Handle(line).GetAwaiter().GetResult()));
            }
        }
    }
}
}
