using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Net;
using System.Text;
using System.Text.RegularExpressions;
using System.Web.Script.Serialization;

namespace XshellDesktop {
public sealed class ConnectionProfiles {
    public sealed class Client {
        public Uri Endpoint;
        public string Token;
    }
    static InvalidOperationException Error(string message) { return new InvalidOperationException(message); }
    public static string Absolute(string path) {
        try {
            if (String.IsNullOrWhiteSpace(path) || path.Any(char.IsControl) || !Path.IsPathRooted(path)) throw Error("路径无效。");
            string full = Path.GetFullPath(path);
            if (!String.Equals(Path.GetPathRoot(full), Path.GetPathRoot(path), StringComparison.OrdinalIgnoreCase)) throw Error("路径无效。");
            return full;
        } catch { throw Error("必须提供不含控制字符的绝对路径。"); }
    }
    public static void Safe(string path) {
        string current = Absolute(path);
        while (!String.IsNullOrEmpty(current)) {
            if ((File.Exists(current) || Directory.Exists(current)) && (File.GetAttributes(current) & FileAttributes.ReparsePoint) != 0) throw Error("拒绝重解析点路径。");
            current = Path.GetDirectoryName(current);
        }
    }
    public static Client ReadClient(string path) {
        try {
            Safe(path);
            if (!File.Exists(path)) throw Error("missing");
            var info = new FileInfo(path);
            if (info.Length > 65536) throw Error("invalid");
            var data = new JavaScriptSerializer().Deserialize<Dictionary<string,object>>(File.ReadAllText(path, new UTF8Encoding(false,true)));
            object e, t;
            Uri uri;
            IPAddress address;
            if (data == null || !data.TryGetValue("endpoint",out e) || !data.TryGetValue("token",out t) || !(e is string) || !(t is string)) throw Error("invalid");
            string token = (string)t;
            if (token.Length < 32 || token.Any(char.IsControl) || token.Any(char.IsWhiteSpace) || !Uri.TryCreate((string)e,UriKind.Absolute,out uri) || uri.Scheme != "http" || uri.AbsolutePath != "/" || uri.UserInfo != "" || uri.Query != "" || uri.Fragment != "" || !IPAddress.TryParse(uri.Host.Trim('[',']'),out address) || !IPAddress.IsLoopback(address)) throw Error("invalid");
            // Reject URI normalization of nonnumeric host spellings and dot paths.
            if (!Regex.IsMatch((string)e,"\\Ahttp://(?:127\\.0\\.0\\.1|\\[::1\\])(?::[0-9]+)?/?\\z",RegexOptions.IgnoreCase)) throw Error("invalid");
            return new Client { Endpoint=uri, Token=token };
        } catch { throw Error("客户端配置不可用：文件缺失、路径不安全或内容无效。"); }
    }
}
}
