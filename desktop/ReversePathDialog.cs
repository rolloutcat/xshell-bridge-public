using System;
using System.Threading;
using System.Windows.Forms;

namespace XshellDesktop {
internal static class ReversePathDialog {
    // Only the Shell supplies the native dialog callback. IPC cannot inject a delegate.
    internal static string Choose(Control owner, Func<bool> stopping, Func<string> showDialog) {
        try {
            // InvokeRequired alone returns false before a handle exists; never create a handle on a worker.
            if (owner == null || owner.IsDisposed || !owner.IsHandleCreated) throw new ReverseExportError("EXPORT_UI_UNAVAILABLE");
            if (owner.InvokeRequired)
                return (string)owner.Invoke(new Func<string>(() => Choose(owner, stopping, showDialog)));
            if (stopping()) throw new ReverseExportError("EXPORT_UI_UNAVAILABLE");
            if (Thread.CurrentThread.GetApartmentState() != ApartmentState.STA) throw new ReverseExportError("EXPORT_UI_THREAD");
            return showDialog();
        } catch (Exception error) { throw ReverseExportError.At(error, "selection"); }
    }
}
}
