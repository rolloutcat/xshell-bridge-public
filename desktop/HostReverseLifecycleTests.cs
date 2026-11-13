using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;

namespace XshellDesktop {
/// <summary>Windows fixture tests using fake SSH only; no helper or Xshell execution.</summary>
public static class HostReverseLifecycleTests {
    sealed class Fake : IReverseProcess {
        public bool Dead, Disposed, StopThrows, StayAlive, ProbeThrows, DisposeThrows, WaitThrows;
        public int StopCalls, DisposeCalls;
        public bool HasExited {
            get {
                if (ProbeThrows) throw new InvalidOperationException("fake probe failure");
                return Dead;
            }
        }
        public int ExitCode { get { return 17; } }
        public bool WaitForExit(int milliseconds) {
            Check(milliseconds > 0 && milliseconds <= 2000, "bounded wait");
            if (WaitThrows) throw new InvalidOperationException("fake wait failure");
            return Dead;
        }
        public void Stop() {
            StopCalls++;
            if (StopThrows) throw new InvalidOperationException("fake stop failure");
            if (!StayAlive) Dead = true;
        }
        public void Dispose() {
            DisposeCalls++;
            if (DisposeThrows) throw new InvalidOperationException("fake disposal failure");
            Disposed = true;
        }
    }
    sealed class Fixture : IDisposable {
        public readonly string Root, Key, Known;
        public readonly HostReverse Host;
        public readonly List<Fake> Processes = new List<Fake>();
        public Fake Next;
        public Fixture() {
            Root = Path.Combine(Path.GetTempPath(), "reverse lifecycle-" + Guid.NewGuid().ToString("N"));
            Directory.CreateDirectory(Root);
            string bundle = Path.Combine(Root, "bundle"), runtime = Path.Combine(Root, "runtime");
            Directory.CreateDirectory(bundle);
            Directory.CreateDirectory(runtime);
            File.WriteAllText(Path.Combine(runtime, "client.json"), "{\"endpoint\":\"http://127.0.0.1:18888\",\"token\":\"" + new string('C', 40) + "\"}");
            Key = Path.Combine(Root, "key");
            Known = Path.Combine(Root, "known hosts");
            File.WriteAllText(Key, "fixture");
            File.WriteAllText(Known, "fixture");
            Host = new HostReverse(Path.Combine(Root, "catalog"), bundle, runtime, delegate(System.Diagnostics.ProcessStartInfo start) {
                var process = Next ?? new Fake();
                Next = null;
                Processes.Add(process);
                return process;
            });
        }
        public string Add(string host = "vm.example", int sshPort = 22, string user = "operator", int remotePort = 18889) {
            return Host.Add("VM", host, sshPort, user, Key, Known, remotePort, "test", "manual_terminal");
        }
        public Fake Start(string id) {
            Check(Host.Connect(id).status == HostReverse.AwaitingAgent, "started status");
            return Processes.Last();
        }
        public void Dispose() {
            foreach (var process in Processes) {
                process.StopThrows = false;
                process.StayAlive = false;
                process.ProbeThrows = false;
                process.DisposeThrows = false;
                process.WaitThrows = false;
            }
            try { Host.Dispose(); }
            finally { Directory.Delete(Root, true); }
        }
    }
    static void Check(bool value, string message) {
        if (!value) throw new Exception(message);
    }
    static void Reject(Action action) {
        bool rejected = false;
        try { action(); } catch (InvalidOperationException) { rejected = true; }
        Check(rejected, "expected refusal");
    }
    static void StopFailureRetainsOwnership(bool throws) {
        using (var f = new Fixture()) {
            string id = f.Add();
            var process = f.Start(id);
            process.StopThrows = throws;
            process.StayAlive = !throws;
            Reject(delegate { f.Host.Disconnect(id); });
            Check(!process.Disposed, "live process handle retained");
            Check(f.Host.Get(id).status == HostReverse.AwaitingAgent, "not falsely disconnected");
            f.Host.Connect(id);
            Check(f.Processes.Count == 1, "reconnect remains idempotent");
            Reject(delegate { f.Host.Remove(id); });
            process.StopThrows = false;
            process.StayAlive = false;
            f.Host.Disconnect(id);
            Check(process.Dead && process.Disposed, "retry cleans up owned process");
            Check(f.Host.Get(id).status == HostReverse.Disconnected, "confirmed disconnection");
        }
    }
    static void ProbeFailureDoesNotStopExistingConnection() {
        using (var f = new Fixture()) {
            string id = f.Add();
            var process = f.Start(id);
            process.ProbeThrows = true;
            Reject(delegate { f.Host.Connect(id); });
            Check(process.StopCalls == 0 && !process.Disposed, "failed observation must not terminate existing connection");
            Reject(delegate { f.Host.Disconnect(id); });
            Check(!process.Disposed, "unknown exit state retains handle");
            process.ProbeThrows = false;
            Check(f.Host.Get(id).status == HostReverse.AwaitingAgent, "ownership survives probe failure");
        }
    }
    static void StartupFailureRetainsUnstoppableProcess() {
        using (var f = new Fixture()) {
            string id = f.Add();
            var process = new Fake { WaitThrows = true, StopThrows = true };
            f.Next = process;
            Reject(delegate { f.Host.Connect(id); });
            Check(process.StopCalls == 1 && !process.Disposed, "failed startup cleanup retains live handle");
            Check(f.Host.Get(id).status == HostReverse.AwaitingAgent, "failed startup is still owned");
            f.Host.Connect(id);
            Check(f.Processes.Count == 1, "no duplicate after failed startup cleanup");
            process.StopThrows = false;
            f.Host.Disconnect(id);
            Check(process.Disposed, "startup cleanup can be retried");
        }
    }
    static void DisposeRetriesAndAttemptsOtherProcesses() {
        using (var f = new Fixture()) {
            string first = f.Add(), second = f.Add("other.example");
            var a = f.Start(first);
            var b = f.Start(second);
            a.StopThrows = true;
            Reject(delegate { f.Host.Dispose(); });
            Check(!a.Disposed && !a.Dead, "failed shutdown retains first process");
            Check(b.Dead && b.Disposed, "shutdown still attempts other processes");
            Reject(delegate { f.Host.Connect(first); });
            Check(f.Processes.Count == 2, "closed host cannot start more processes");
            a.StopThrows = false;
            f.Host.Dispose();
            Check(a.Dead && a.Disposed, "second Dispose retries retained process");
            int stops = a.StopCalls;
            f.Host.Dispose();
            Check(a.StopCalls == stops && b.DisposeCalls == 1, "completed shutdown is idempotent");
        }
    }
    static void DisposalFailureRetainsExitedHandle() {
        using (var f = new Fixture()) {
            string id = f.Add();
            var process = f.Start(id);
            process.DisposeThrows = true;
            Reject(delegate { f.Host.Disconnect(id); });
            Check(process.Dead && !process.Disposed, "exit precedes disposal");
            Check(f.Host.Get(id).status == HostReverse.Exited, "failed disposal retains exited ownership");
            process.DisposeThrows = false;
            f.Host.Disconnect(id);
            Check(process.Disposed && process.StopCalls == 1, "retry disposes without another stop");
        }
    }
    static void DuplicateEndpointsAndIndependentVMs() {
        using (var f = new Fixture()) {
            string id = f.Add(), duplicate = f.Add("VM.EXAMPLE");
            var first = f.Start(id);
            Reject(delegate { f.Host.Connect(duplicate); });
            Check(f.Processes.Count == 1 && first.StopCalls == 0, "duplicate rejected before factory without stopping owner");
            Check(f.Host.Get(duplicate).status == HostReverse.Disconnected, "rejected profile remains disconnected");
            f.Host.Connect(id);
            Check(f.Processes.Count == 1, "same-profile connect is idempotent");
            f.Start(f.Add("other.example"));
            f.Start(f.Add("vm.example", 2222));
            f.Start(f.Add("vm.example", 22, "Operator"));
            f.Start(f.Add("vm.example", 22, "operator", 18890));
            Check(f.Processes.Count == 5, "host, SSH port, user and remote port are separate endpoint dimensions");
            first.Dead = true;
            f.Start(duplicate);
            Check(f.Processes.Count == 6, "exited owner does not block endpoint reuse");
            Check(f.Host.Get(id).status == HostReverse.Exited, "original exit remains observable");
            f.Host.Disconnect(id);
            Check(first.Disposed && first.StopCalls == 0, "already exited process is disposed without Stop");
        }
    }
    static void CatalogRemovalDoesNotReleaseOwnership(bool deleteFile) {
        using (var f = new Fixture()) {
            string id = f.Add();
            var process = f.Start(id);
            string catalog = Path.Combine(f.Root, "catalog", "host-reverse.json");
            using (var peer = new HostReverse(Path.Combine(f.Root, "catalog"), Path.Combine(f.Root, "bundle"), Path.Combine(f.Root, "runtime"), delegate(System.Diagnostics.ProcessStartInfo start) {
                throw new Exception("peer must not start SSH");
            })) {
                Check(!peer.HasActiveOwnedProcesses, "peer owns no processes");
                if (deleteFile) File.Delete(catalog); else peer.Remove(id);
                Check(f.Host.HasActiveOwnedProcesses, "catalog removal cannot hide owned SSH");
                var row = f.Host.List().Single();
                Check(row.id == id && row.name == "VM" && row.status == HostReverse.AwaitingAgent, "removed row remains visible from owned snapshot");
                Check(process.StopCalls == 0 && !process.Disposed, "catalog removal cannot stop another manager's SSH");
                Reject(delegate { peer.Disconnect(id); });
                Check(process.StopCalls == 0, "peer cannot release another manager's process");
                f.Host.Disconnect(id);
                Check(process.Dead && process.Disposed && process.StopCalls == 1, "owner can disconnect without catalog row");
                Check(!f.Host.HasActiveOwnedProcesses && f.Host.List().Count == 0, "released missing row disappears");
                Reject(delegate { f.Host.Disconnect(id); });
            }
        }
    }
    static void OwnedSnapshotSurvivesCatalogEdit() {
        using (var f = new Fixture()) {
            string id = f.Add();
            f.Start(id);
            string catalog = Path.Combine(f.Root, "catalog", "host-reverse.json");
            File.WriteAllText(catalog, File.ReadAllText(catalog).Replace("vm.example", "edited.example"));
            var row = f.Host.List().Single();
            Check(row.id == id && row.ssh_host == "vm.example" && row.status == HostReverse.AwaitingAgent, "live status uses connection snapshot");
            f.Host.Disconnect(id);
            row = f.Host.List().Single();
            Check(row.ssh_host == "edited.example" && row.status == HostReverse.Disconnected, "disconnected status uses current catalog");
        }
    }
    static void BadCatalogAndObservationFailures() {
        using (var f = new Fixture()) {
            string id = f.Add();
            var process = f.Start(id);
            File.WriteAllText(Path.Combine(f.Root, "catalog", "host-reverse.json"), "{bad JSON");
            Check(f.Host.HasActiveOwnedProcesses, "guard is independent of invalid catalog");
            Reject(delegate { f.Host.List(); });
            process.ProbeThrows = true;
            Reject(delegate { bool active = f.Host.HasActiveOwnedProcesses; });
            Reject(delegate { f.Host.Disconnect(id); });
            Check(!process.Disposed && process.StopCalls == 0, "observation failure retains ownership");
            process.ProbeThrows = false;
            f.Host.Disconnect(id);
            Check(!f.Host.HasActiveOwnedProcesses && process.Disposed, "owned cleanup ignores invalid catalog");
        }
    }
    public static void RunAll() {
        CatalogRemovalDoesNotReleaseOwnership(false);
        CatalogRemovalDoesNotReleaseOwnership(true);
        OwnedSnapshotSurvivesCatalogEdit();
        BadCatalogAndObservationFailures();
        StopFailureRetainsOwnership(true);
        StopFailureRetainsOwnership(false);
        ProbeFailureDoesNotStopExistingConnection();
        StartupFailureRetainsUnstoppableProcess();
        DisposeRetriesAndAttemptsOtherProcesses();
        DisposalFailureRetainsExitedHandle();
        DuplicateEndpointsAndIndependentVMs();
    }
}
}
