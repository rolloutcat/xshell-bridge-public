// Fixture only: models a cooperating Xshell UTF-8 log writer with real handles.
using System;
using System.IO;
using System.Text;
using System.Collections.Generic;
using System.Web.Script.Serialization;
class LogWriter {
    static int Main() {
        var json=new JavaScriptSerializer { MaxJsonLength=1048576 };
        FileStream file=null;
        try {
            string line;
            while((line=Console.ReadLine())!=null) {
                var r=json.Deserialize<Dictionary<string,object>>(line);
                string op=(string)r["op"];
                if(op=="open") file=new FileStream((string)r["path"],FileMode.CreateNew,FileAccess.Write,FileShare.ReadWrite);
                else if(op=="write") {
                    string choice=r.ContainsKey("encoding") ? (string)r["encoding"] : "utf-8";
                    int cp=choice=="utf-8" ? 65001 : choice=="utf-16le" ? 1200 : choice=="utf-16be" ? 1201 : choice=="gbk" ? 936 : choice=="gb18030" ? 54936 : 0;
                    if(cp==0) return 2;
                    Encoding encoding=Encoding.GetEncoding(cp,EncoderFallback.ExceptionFallback,DecoderFallback.ExceptionFallback);
                    if(r.ContainsKey("bom") && (bool)r["bom"]) {byte[] pre=encoding.GetPreamble();file.Write(pre,0,pre.Length);}
                    byte[] b=encoding.GetBytes((string)r["text"]);file.Write(b,0,b.Length);file.Flush();
                }
                else if(op=="close") {file.Dispose();file=null;Console.WriteLine("OK");return 0;}
                else return 2;
                Console.WriteLine("OK");
            }
            return 0;
        } catch { return 1; }
        finally {if(file!=null)file.Dispose();}
    }
}
