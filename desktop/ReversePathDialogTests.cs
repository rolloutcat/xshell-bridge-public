using System;
using System.Threading;
using System.Threading.Tasks;
using System.Windows.Forms;

namespace XshellDesktop {
// Real Control.Invoke + STA message loop; the dialog callback is synthetic and never opens a dialog.
internal static class ReversePathDialogTests {
    static void Check(bool value, string message) { if (!value) throw new Exception(message); }
    static int Main() {
        Control owner = null; ApplicationContext loop = null; Exception uiError = null;
        var ready = new ManualResetEventSlim();
        var ui = new Thread(delegate() {
            try {
                owner = new Control(); owner.CreateControl(); loop = new ApplicationContext(); ready.Set();
                Application.Run(loop); owner.Dispose();
            } catch (Exception error) { uiError = error; ready.Set(); }
        });
        ui.IsBackground = true; ui.SetApartmentState(ApartmentState.STA); ui.Start();
        try {
            Check(ready.Wait(5000) && uiError == null, "STA loop unavailable");
            string expected = @"D:\synthetic & only\xshell_agent.js"; int calls = 0;
            string actual = ReversePathDialog.Choose(owner, () => false, delegate {
                calls++; Check(Thread.CurrentThread.ManagedThreadId == ui.ManagedThreadId && Thread.CurrentThread.GetApartmentState() == ApartmentState.STA, "wrong chooser thread");
                return expected;
            });
            Check(actual == expected && calls == 1, "selection changed or replayed");
            Check(ReversePathDialog.Choose(owner, () => false, () => null) == null, "cancel changed");
            string code = null;
            try { ReversePathDialog.Choose(owner, () => false, delegate { throw new UnauthorizedAccessException("PRIVATE CALLBACK DETAIL"); }); }
            catch (ReverseExportError error) { code = error.Code; Check(!error.ToString().Contains("PRIVATE CALLBACK"), "callback detail leaked"); }
            Check(code == "EXPORT_ACCESS_DENIED", "Invoke wrapper lost typed error");
            code = null;
            try { ReversePathDialog.Choose(owner, () => true, () => { calls++; return expected; }); }
            catch (ReverseExportError error) { code = error.Code; }
            Check(code == "EXPORT_UI_UNAVAILABLE" && calls == 1, "closed owner invoked selector");
            using (var noHandle = new Control()) {
                code = null;
                try { ReversePathDialog.Choose(noHandle, () => false, () => expected); }
                catch (ReverseExportError error) { code = error.Code; }
                Check(code == "EXPORT_UI_UNAVAILABLE" && !noHandle.IsHandleCreated, "created UI handle on caller thread");
            }
            using (var wrongApartment = new Control()) {
                wrongApartment.CreateControl(); code = null;
                try { ReversePathDialog.Choose(wrongApartment, () => false, () => expected); }
                catch (ReverseExportError error) { code = error.Code; }
                Check(code == "EXPORT_UI_THREAD", "MTA owner was accepted");
            }
            Console.WriteLine("PASS: real STA Control.Invoke chooser wrapper, cancellation, redaction and shutdown"); return 0;
        } catch (Exception error) { Console.Error.WriteLine(error.Message); return 1; }
        finally {
            if (owner != null && owner.IsHandleCreated) owner.BeginInvoke(new Action(() => loop.ExitThread()));
            ui.Join(5000); ready.Dispose();
        }
    }
}
}
