using System;
using System.Drawing;
using System.IO;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Collections.Generic;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using System.Web.Script.Serialization;
using System.Windows.Forms;
using Microsoft.Web.WebView2.Core;
using Microsoft.Web.WebView2.WinForms;

namespace XshellDesktop
{
    internal sealed class Shell : Form
    {
        private readonly Backend backend;
        private readonly WebView2 webView;
        private readonly Label notice;
        private readonly SemaphoreSlim messageGate = new SemaphoreSlim(1, 1);
        private readonly JavaScriptSerializer serializer = new JavaScriptSerializer
        {
            MaxJsonLength = 1048576,
            RecursionLimit = 64
        };

        private Icon windowIcon;
        private readonly NotifyIcon pendingIcon = new NotifyIcon();
        private readonly System.Windows.Forms.Timer pendingTimer = new System.Windows.Forms.Timer();
        private int noticeCount;
        private int queuedNotice;
        private DateTime lastBalloon = DateTime.MinValue;
        [StructLayout(LayoutKind.Sequential)]
        private struct FlashInfo { public uint cbSize; public IntPtr hwnd; public uint flags; public uint count; public uint timeout; }
        [DllImport("user32.dll")]
        private static extern bool FlashWindowEx(ref FlashInfo info);
        private void FlashOwn(bool enabled)
        {
            if (!IsHandleCreated) return;
            FlashInfo info = new FlashInfo { cbSize = (uint)Marshal.SizeOf(typeof(FlashInfo)), hwnd = Handle, flags = enabled ? 3u : 0u, count = enabled ? 5u : 0u, timeout = 0 };
            FlashWindowEx(ref info);
        }
        private void FlushPendingNotice()
        {
            if (queuedNotice == 0 || noticeCount == 0 || (ContainsFocus && WindowState != FormWindowState.Minimized)) return;
            if ((DateTime.UtcNow - lastBalloon).TotalSeconds < 10) return;
            queuedNotice = 0; lastBalloon = DateTime.UtcNow;
            FlashOwn(true);
            pendingIcon.ShowBalloonTip(5000, "Xshell Bridge 待批准", noticeCount + " 项等待批准或激活。点击查看审批中心。", ToolTipIcon.Info);
        }
        private Task initialization;
        private TaskCompletionSource<bool> drained;
        private int pending;
        private bool blankLoadAllowed;
        private bool htmlSubmitted;
        private bool stopping;
        private bool closeAllowed;
        private bool closeInProgress;
        private bool backendDisposalStarted;
        private bool backendDisposed;
        private string embeddedDataUri;

        [STAThread]
        public static void Main(string[] args)
        {
            Application.EnableVisualStyles();
            Application.SetCompatibleTextRenderingDefault(false);

            Backend backend = null;
            Shell shell = null;
            try
            {
                backend = new Backend(args);
                if(!backend.AcquireDesktopLifetime()) {
                    MessageBox.Show("此运行目录的工作台已经打开，请使用现有窗口。","Xshell Bridge",MessageBoxButtons.OK,MessageBoxIcon.Information);
                    return;
                }
                backend.BindBridgeLifetimeAsync().GetAwaiter().GetResult();
                shell = new Shell(backend);
                Application.Run(shell);
            }
            catch (Exception)
            {
                MessageBox.Show(
                    "Xshell Bridge could not start. Check the application configuration and access permissions.",
                    "Xshell Bridge", MessageBoxButtons.OK, MessageBoxIcon.Error);
            }
            finally
            {
                if (shell != null)
                {
                    try { shell.DisposeBackend(); }
                    catch (Exception)
                    {
                        MessageBox.Show(
                            "清理失败：无法确认本桌面拥有的反向 SSH 已全部停止；不会声称已断开，也不会停止其他进程。",
                            "Xshell Bridge", MessageBoxButtons.OK, MessageBoxIcon.Error);
                    }
                    finally { shell.Dispose(); }
                }
                else if (backend != null)
                {
                    try { backend.Dispose(); }
                    catch (Exception) { }
                }
            }
        }

        private Shell(Backend backend)
        {
            this.backend = backend;
            backend.SetReversePathSelector(ChooseReversePath);
            Text = "Xshell Bridge";
            ClientSize = new Size(1280, 850);
            MinimumSize = new Size(900, 600);
            StartPosition = FormStartPosition.CenterScreen;

            try { windowIcon = System.Drawing.Icon.ExtractAssociatedIcon(Application.ExecutablePath); }
            catch (Exception) { }
            // Own only extracted or cloned icons, never the shared system icon.
            if (windowIcon == null) windowIcon = (Icon)SystemIcons.Application.Clone();
            Icon = windowIcon;

            webView = new WebView2 { Dock = DockStyle.Fill };
            notice = new Label
            {
                Dock = DockStyle.Top,
                AutoSize = false,
                Height = 64,
                Padding = new Padding(12),
                BackColor = Color.LightYellow,
                ForeColor = Color.Black,
                Visible = false
            };
            Controls.Add(webView);
            Controls.Add(notice);

            pendingIcon.Icon = windowIcon;
            pendingIcon.Text = "Xshell Bridge 待批准";
            pendingIcon.BalloonTipClicked += delegate {
                if (stopping || noticeCount == 0) return;
                if (WindowState == FormWindowState.Minimized) WindowState = FormWindowState.Normal;
                Show(); Activate(); FlashOwn(false);
                if (IsTrustedDocument()) webView.CoreWebView2.PostWebMessageAsJson("{\"op\":\"openApprovals\"}");
            };
            Activated += delegate { FlashOwn(false); queuedNotice = 0; };
            pendingTimer.Interval = 1000;
            pendingTimer.Tick += delegate { FlushPendingNotice(); };
            pendingTimer.Start();
            Shown += OnShown;
            FormClosing += OnFormClosing;
        }

        // Called only by the explicit reverseChoosePath request, never by refresh/listing.
        // Backend continuations can run off-thread: all dialogs are marshalled to this STA UI thread.
        private string ChooseReversePath(string kind)
        {
            return ReversePathDialog.Choose(this, () => stopping, () => ShowReversePathDialog(kind));
        }

        private string ShowReversePathDialog(string kind)
        {
            if (kind == "export")
            {
                using (var dialog = new SaveFileDialog())
                {
                    dialog.Title = "导出远端 Xshell JS（包含代理凭据，请私密传输）";
                    dialog.Filter = "JavaScript (*.js)|*.js";
                    // Let DefaultExt/AddExtension add the suffix once for the default name.
                    dialog.FileName = "xshell_agent";
                    dialog.DefaultExt = "js";
                    dialog.AddExtension = true;
                    dialog.OverwritePrompt = true;
                    dialog.CheckPathExists = true;
                    // Selection never writes. Native export still refuses an existing destination.
                    return dialog.ShowDialog(this) == DialogResult.OK ? dialog.FileName : null;
                }
            }
            if (kind != "key" && kind != "known_hosts")
                throw new InvalidOperationException("Unsupported path selector.");
            using (var dialog = new OpenFileDialog())
            {
                dialog.Title = kind == "key" ? "选择 SSH 私钥文件" : "选择 known_hosts 文件";
                dialog.Filter = "All files (*.*)|*.*";
                dialog.CheckFileExists = true;
                dialog.CheckPathExists = true;
                dialog.Multiselect = false;
                return dialog.ShowDialog(this) == DialogResult.OK ? dialog.FileName : null;
            }
        }

        private void OnShown(object sender, EventArgs e)
        {
            initialization = InitializeAsync();
        }

        private async Task InitializeAsync()
        {
            try
            {
                string html;
                using (Stream stream = Assembly.GetExecutingAssembly()
                    .GetManifestResourceStream("XshellDesktop.app.html"))
                {
                    if (stream == null)
                        throw new InvalidOperationException("Missing application resource.");
                    using (StreamReader reader = new StreamReader(stream))
                        html = reader.ReadToEnd();
                }

                string portableRuntime = Path.Combine(AppDomain.CurrentDomain.BaseDirectory, "WebView2Runtime");
                string browserFolder = File.Exists(Path.Combine(portableRuntime,"msedgewebview2.exe")) ? portableRuntime : null;
                CoreWebView2Environment environment =
                    await CoreWebView2Environment.CreateAsync(browserFolder, backend.ProfileDirectory);
                if (stopping)
                    return;

                await webView.EnsureCoreWebView2Async(environment);
                if (stopping)
                    return;

                CoreWebView2 core = webView.CoreWebView2;
                CoreWebView2Settings settings = core.Settings;
                settings.AreDevToolsEnabled = false;
                settings.AreHostObjectsAllowed = false;
                settings.AreDefaultContextMenusEnabled = false;
                settings.AreBrowserAcceleratorKeysEnabled = false;
                settings.IsStatusBarEnabled = false;
                settings.IsGeneralAutofillEnabled = false;
                settings.IsPasswordAutosaveEnabled = false;
                settings.IsWebMessageEnabled = true;

                core.NavigationStarting += OnNavigationStarting;
                core.FrameNavigationStarting += OnFrameNavigationStarting;
                core.NavigationCompleted += OnNavigationCompleted;
                core.NewWindowRequested += OnNewWindowRequested;
                core.PermissionRequested += OnPermissionRequested;
                core.DownloadStarting += OnDownloadStarting;
                core.WebMessageReceived += OnWebMessageReceived;

                blankLoadAllowed = true;
                htmlSubmitted = true;
                embeddedDataUri = "data:text/html;charset=utf-8;base64," + Convert.ToBase64String(Encoding.UTF8.GetBytes(html));
                core.NavigateToString(html);
            }
            catch (WebView2RuntimeNotFoundException)
            {
                FailInitialization(
                    "未检测到 Microsoft Edge WebView2 Runtime。lite 包不包含运行时，请安装微软 WebView2 Evergreen Runtime；离线机器可使用微软提供的独立安装包。");
            }
            catch (Exception)
            {
                FailInitialization(
                    "Xshell Bridge could not initialize its embedded browser. Check that " +
                    "Microsoft WebView2 Evergreen Runtime is installed and that the profile " +
                    "directory is accessible.");
            }
        }

        private void FailInitialization(string text)
        {
            if (stopping)
                return;
            MessageBox.Show(this, text, "Xshell Bridge",
                MessageBoxButtons.OK, MessageBoxIcon.Error);
            // Schedule closing after InitializeAsync has completed.
            BeginInvoke(new Action(Close));
        }

        private void OnNavigationStarting(object sender,
            CoreWebView2NavigationStartingEventArgs e)
        {
            if (!stopping && htmlSubmitted && blankLoadAllowed &&
                IsEmbeddedAddress(e.Uri))
            {
                blankLoadAllowed = false;
                return;
            }

            e.Cancel = true;
            InvalidateReviews();
        }

        private void OnFrameNavigationStarting(object sender,
            CoreWebView2NavigationStartingEventArgs e)
        {
            e.Cancel = true;
            InvalidateReviews();
        }

        private void OnNewWindowRequested(object sender,
            CoreWebView2NewWindowRequestedEventArgs e)
        {
            e.Handled = true;
            InvalidateReviews();
        }

        private void OnPermissionRequested(object sender,
            CoreWebView2PermissionRequestedEventArgs e)
        {
            e.State = CoreWebView2PermissionState.Deny;
            e.Handled = true;
        }

        private void OnDownloadStarting(object sender,
            CoreWebView2DownloadStartingEventArgs e)
        {
            e.Cancel = true;
            e.Handled = true;
        }

        private void OnNavigationCompleted(object sender,
            CoreWebView2NavigationCompletedEventArgs e)
        {
            if (!stopping && !e.IsSuccess)
                ShowNotice("内置页面加载失败：" + e.WebErrorStatus + "。请关闭后重新启动桌面端。");
        }

        private bool IsEmbeddedAddress(string source)
        {
            // Some Runtime versions expose NavigateToString as this exact data URL.
            // Do not accept arbitrary data URLs, other about pages, or external URLs.
            return String.Equals(source, "about:blank", StringComparison.Ordinal) ||
                (embeddedDataUri != null && String.Equals(source, embeddedDataUri, StringComparison.Ordinal));
        }

        private bool IsTrustedDocument()
        {
            Uri source = webView.Source;
            return !stopping && source != null &&
                IsEmbeddedAddress(source.AbsoluteUri);
        }

        private async void OnWebMessageReceived(object sender,
            CoreWebView2WebMessageReceivedEventArgs e)
        {
            bool counted = false;
            bool acquired = false;
            string message = null;
            try
            {
                if (!IsTrustedDocument() ||
                    !IsEmbeddedAddress(e.Source))
                    return;

                try { message = e.TryGetWebMessageAsString(); }
                catch (ArgumentException) { return; }

                if (message == null)
                    return;
                if (message.Length > 65536)
                {
                    Reject(message, "消息超过大小限制。", true);
                    return;
                }

                // Eight total outstanding requests, including the active request.
                if (pending >= 8)
                {
                    Reject(message, "本地请求队列已满，请稍后查询原对象。", true);
                    return;
                }

                pending++;
                counted = true;
                await messageGate.WaitAsync();
                acquired = true;

                // Queued approvals are discarded during shutdown, never replayed.
                if (stopping)
                    return;

                var envelope = serializer.DeserializeObject(message) as Dictionary<string, object>;
                object deadline;
                if (envelope == null) { Reject(message, "请求格式无效。", true); return; }
                if (envelope.TryGetValue("deadline_ms", out deadline))
                {
                    double value;
                    if (!Double.TryParse(Convert.ToString(deadline, System.Globalization.CultureInfo.InvariantCulture), System.Globalization.NumberStyles.Float, System.Globalization.CultureInfo.InvariantCulture, out value) || Double.IsNaN(value) || Double.IsInfinity(value) || value <= (DateTime.UtcNow - new DateTime(1970, 1, 1)).TotalMilliseconds)
                    { Reject(message, "排队请求已过期，未提交。请查询原对象。", true); return; }
                    envelope.Remove("deadline_ms");
                }
                object operation;
                if (envelope.TryGetValue("op", out operation) && Object.Equals(operation, "pendingNotice"))
                {
                    object id, count, notify;
                    if (envelope.Count != 4 || !envelope.TryGetValue("id", out id) || !(id is int || id is long) || !envelope.TryGetValue("count", out count) || !(count is int) || (int)count < 0 || (int)count > 999 || !envelope.TryGetValue("notify", out notify) || !(notify is bool))
                    { Reject(message, "提醒请求格式无效。", true); return; }
                    noticeCount = (int)count;
                    Text = (noticeCount > 0 ? "(" + noticeCount + ") 待批准 · " : "") + "Xshell Bridge";
                    pendingIcon.Text = noticeCount > 0 ? noticeCount + " 项等待批准 / 激活" : "Xshell Bridge";
                    pendingIcon.Visible = noticeCount > 0;
                    if (queuedNotice > 0) queuedNotice = noticeCount;
                    if (noticeCount == 0) { queuedNotice = 0; FlashOwn(false); }
                    else if ((bool)notify && (!ContainsFocus || WindowState == FormWindowState.Minimized)) { queuedNotice = noticeCount; FlushPendingNotice(); }
                    webView.CoreWebView2.PostWebMessageAsJson(serializer.Serialize(new { id = id, ok = true, data = new { } }));
                    return;
                }
                object result = await backend.Handle(serializer.Serialize(envelope));
                if (IsTrustedDocument())
                {
                    string json = serializer.Serialize(result);
                    webView.CoreWebView2.PostWebMessageAsJson(json);
                }
                if (backend.HandoffScheduled && !stopping)
                    BeginInvoke(new Action(Close));
            }
            catch (Exception)
            {
                if (!stopping)
                    { Reject(message, "本地请求未能完成；结果可能未知，请查询原对象，不要重复提交。", true); ShowNotice("请求异常，请查询原任务状态；不会自动重放命令。"); }
            }
            finally
            {
                if (acquired)
                    messageGate.Release();
                if (counted)
                {
                    pending--;
                    if (stopping && pending == 0 && drained != null)
                        drained.TrySetResult(true);
                }
            }
        }

        private void Reject(string message, string error, bool readId)
        {
            object id = null;
            if (readId)
            {
                try
                {
                    var envelope = serializer.DeserializeObject(message)
                        as System.Collections.Generic.Dictionary<string, object>;
                    object candidate;
                    if (envelope != null && envelope.TryGetValue("id", out candidate) &&
                        (candidate is int || candidate is long ||
                         candidate is decimal || candidate is double))
                        id = candidate;
                }
                catch (Exception) { }
            }

            if (IsTrustedDocument())
                webView.CoreWebView2.PostWebMessageAsJson(
                    serializer.Serialize(new { id = id, error = error }));
        }

        private void InvalidateReviews()
        {
            if (backendDisposalStarted)
                return;
            try { backend.InvalidateReviews(); }
            catch (Exception)
            {
                ShowNotice("Review state could not be refreshed. Close and restart Xshell Bridge.");
            }
        }

        private void ShowNotice(string text)
        {
            if (IsDisposed || stopping)
                return;
            notice.Text = text;
            notice.Visible = true;
            notice.BringToFront();
        }

        private async void OnFormClosing(object sender, FormClosingEventArgs e)
        {
            if (closeAllowed)
                return;

            e.Cancel = true;
            if (closeInProgress)
                return;

            closeInProgress = true;
            // Remain frozen even when disposal fails: Backend may already be disposed.
            // Only the native window close button can retry cleanup.
            stopping = true;
            webView.Enabled = false;
            notice.Text = "正在关闭工作台与 Bridge；未派发任务取消，已派发但未确认结果的任务保留为未知。远端命令可能仍在运行，不会自动重放。";
            notice.Visible = true;
            notice.BringToFront();

            try
            {
                InvalidateReviews();
                if (initialization != null)
                    await initialization;

                if (pending != 0)
                {
                    drained = new TaskCompletionSource<bool>();
                    await drained.Task;
                }

                // Invalidate once more after any active operation has completed.
                InvalidateReviews();
                // Stop is bounded per owned process, but must never block the UI thread.
                await backend.CloseBridgeAsync();
                await Task.Run(new Action(DisposeBackend));
                closeAllowed = true;
            }
            catch (Exception)
            {
                // Keep the native window and its close button available, not backend commands.
                notice.Text = "关闭未完成：Bridge 停机或本桌面隧道清理未确认，窗口保持打开。再次关闭仅重试尚未提交的清理，不重发结果未知的停机请求。强制结束主程序会结束其所属 Bridge；远端命令可能仍在运行。";
                notice.Visible = true;
                notice.BringToFront();
            }
            finally
            {
                closeInProgress = false;
            }
            if (closeAllowed)
                Close();
        }

        private void DisposeBackend()
        {
            if (backendDisposed)
                return;
            backendDisposalStarted = true;
            backend.Dispose();
            backendDisposed = true;
        }

        protected override void Dispose(bool disposing)
        {
            if (disposing)
            {
                pendingTimer.Stop();
                pendingTimer.Dispose();
                FlashOwn(false);
                pendingIcon.Visible = false;
                pendingIcon.Dispose();
                if (windowIcon != null)
                {
                    Icon = null;
                    windowIcon.Dispose();
                    windowIcon = null;
                }
                webView.Dispose();
                notice.Dispose();
                if (pending == 0)
                    messageGate.Dispose();
            }
            base.Dispose(disposing);
        }
    }
}
