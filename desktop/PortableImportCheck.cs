using System;
using System.Collections.Generic;
using System.IO;
using System.Security.Cryptography;

namespace XshellDesktop {
// Build/check fixture only. Never included in a portable bundle.
internal static class PortableImportCheck {
 static void NoReparse(string path) {
  for (string p = Path.GetFullPath(path); !String.IsNullOrEmpty(p); p = Path.GetDirectoryName(p)) {
   if ((File.Exists(p) || Directory.Exists(p)) && (File.GetAttributes(p) & FileAttributes.ReparsePoint) != 0)
    throw new InvalidOperationException("Reparse point rejected.");
  }
 }
 static void RemoveOwned(string owned, string parent, string name) {
  if (Path.GetDirectoryName(owned) != parent || Path.GetFileName(owned) != name || name.Length != 36 || !name.StartsWith("xpc-", StringComparison.Ordinal))
   throw new InvalidOperationException("Not an owned check directory.");
  NoReparse(owned);
  if (!Directory.Exists(owned)) return;
  // Check every directory before descending; do not use AllDirectories.
  var pending = new Stack<string>();
  pending.Push(owned);
  while (pending.Count != 0) {
   string dir = pending.Pop();
   NoReparse(dir);
   foreach (string entry in Directory.GetFileSystemEntries(dir)) {
    string full = Path.GetFullPath(entry);
    if (!full.StartsWith(owned + Path.DirectorySeparatorChar, StringComparison.OrdinalIgnoreCase))
     throw new InvalidOperationException("Cleanup path escaped owned root.");
    NoReparse(full);
    if (Directory.Exists(full)) pending.Push(full);
   }
  }
  Directory.Delete(owned, true);
 }
 static int Main(string[] args) {
  try {
   if (args.Length != 1) throw new ArgumentException("Expected one ZIP path.");
   string zip = Path.GetFullPath(args[0]);
   NoReparse(zip);
   string hash;
   using (var input = new FileStream(zip, FileMode.Open, FileAccess.Read, FileShare.Read))
   using (var sha = SHA256.Create())
    hash = BitConverter.ToString(sha.ComputeHash(input)).Replace("-", "").ToLowerInvariant();
   // Keep build fixtures under dist, but avoid nesting under a long TEMP path:
   // .NET Framework must still extract the fixed runtime's longest filenames.
   string parent = Path.GetFullPath(Path.Combine(Path.GetDirectoryName(zip), "_test-artifacts")).TrimEnd(Path.DirectorySeparatorChar);
   NoReparse(parent);
   Directory.CreateDirectory(parent);
   NoReparse(parent);
   string name = "xpc-" + Guid.NewGuid().ToString("N");
   string owned = Path.Combine(parent, name);
   if (Directory.Exists(owned) || File.Exists(owned)) throw new IOException("Check root already exists.");
   Directory.CreateDirectory(owned);
   try {
    NoReparse(owned);
    // ImportZip performs real hash, structure, extraction and candidate checks.
    // No prepare/apply calls, no process launches, no normal user update root.
    var updates = new Updates(owned, "0.0.0", (cwd, arguments) => { throw new InvalidOperationException("Runner forbidden in import check."); });
    updates.ImportZip(zip, hash);
   } finally {
    RemoveOwned(owned, parent, name);
   }
   Console.WriteLine("PASS");
   return 0;
  } catch (Exception) {
   Console.Error.WriteLine("Portable ZIP import check failed.");
   return 1;
  }
 }
}
}
