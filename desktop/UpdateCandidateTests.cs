using System;
using System.IO;
using XshellDesktop;

internal static class UpdateCandidateTests {
 static void Check(bool b) { if(!b)throw new Exception("Assertion failed"); }
 static void Reject(Action a) { try{a();}catch(InvalidOperationException){return;}throw new Exception("Expected rejection"); }
 static void Main() {
  string root=Path.Combine(Path.GetTempPath(),"candidate-"+Guid.NewGuid().ToString("N"));
  Directory.CreateDirectory(root);
  try {
   Directory.CreateDirectory(Path.Combine(root,"scripts"));
   foreach(string name in new[]{"xshdesk.exe","xshctl.exe","xshupdater.exe","VERSION","scripts/start-desktop.ps1"})
    File.WriteAllText(Path.Combine(root,name),name=="VERSION"?"0.3.0":"synthetic");
   DateTime now=new DateTime(2026,1,1,0,0,0,DateTimeKind.Utc);
   var c=new UpdateCandidate(()=>now);
   int launched=0;
   Reject(()=>c.PrepareApply(false,"Local",Path.Combine(root,"runtime"),null));
   c.AcceptImported(root);
   Check(launched==0);
   string runtime=Path.Combine(root,"runtime");
   var p=c.PrepareApply(false,"Local",runtime,null);
   Reject(()=>c.ConfirmApply(p.Ticket,false,"Vm",runtime,null,plan=>launched++));
   Reject(()=>c.ConfirmApply(p.Ticket,false,"Local",runtime,null,plan=>launched++));
   p=c.PrepareApply(false,"Local",runtime,null);
   now=now.AddMinutes(2);
   Reject(()=>c.ConfirmApply(p.Ticket,false,"Local",runtime,null,plan=>launched++));
   p=c.PrepareApply(false,"Local",runtime,null);
   File.WriteAllText(Path.Combine(root,"xshctl.exe"),"tampered");
   Reject(()=>c.ConfirmApply(p.Ticket,false,"Local",runtime,null,plan=>launched++));
   Check(launched==0);
   c.AcceptImported(root);
   p=c.PrepareApply(false,"Local",runtime,null);
   c.ConfirmApply(p.Ticket,false,"Local",runtime,null,plan=>{Check(!plan.Observer);Check(plan.Bundle==root);launched++;});
   Reject(()=>c.ConfirmApply(p.Ticket,false,"Local",runtime,null,plan=>launched++));
   Check(launched==1);
   Reject(()=>c.PrepareApply(true,"Vm",null,Path.Combine(root,"client-host.json")));
   Reject(()=>c.PrepareApply(true,"Local",null,Path.Combine(root,"client-host.json")));
   Check(launched==1);
   p=c.PrepareApply(false,"Local",runtime,null);c.Invalidate();
   Reject(()=>c.ConfirmApply(p.Ticket,false,"Local",runtime,null,plan=>launched++));
   File.Delete(Path.Combine(root,"xshupdater.exe"));c.AcceptImported(root);
   Reject(()=>c.PrepareApply(false,"Local",runtime,null));
   Check(launched==1);
   Console.WriteLine("PASS: candidate hashes, context proof, expiry, consumption, observer isolation; no processes launched");
  } finally {Directory.Delete(root,true);}
 }
}
