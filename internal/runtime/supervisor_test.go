package runtime

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/yjrszcq/ovpn-go/internal/artifact"
	configservice "github.com/yjrszcq/ovpn-go/internal/config"
	"github.com/yjrszcq/ovpn-go/internal/domain"
	storesqlite "github.com/yjrszcq/ovpn-go/internal/store/sqlite"
)

func TestRuntimeHelperProcess(t *testing.T) {
	if os.Getenv("OVPN_RUNTIME_HELPER") != "1" {
		return
	}
	separator := 0
	for index, value := range os.Args {
		if value == "--" {
			separator = index
			break
		}
	}
	if separator == 0 || separator+1 >= len(os.Args) {
		os.Exit(2)
	}
	role, args := os.Args[separator+1], os.Args[separator+2:]
	markerDir := os.Getenv("OVPN_RUNTIME_MARKERS")
	switch role {
	case "broker":
		listen := option(args, "--listen")
		listener, err := net.Listen("unix", listen)
		if err != nil {
			os.Exit(3)
		}
		_ = os.Chmod(listen, 0o600)
		_ = os.WriteFile(filepath.Join(markerDir, "broker-started"), []byte("ok"), 0o600)
		if os.Getenv("OVPN_RUNTIME_BROKER_EXIT") == "1" {
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if _, err := os.Stat(filepath.Join(markerDir, "openvpn-started")); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			_ = listener.Close()
			_ = os.Remove(listen)
			os.Exit(5)
		}
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
		<-signals
		_ = listener.Close()
		_ = os.Remove(listen)
	case "openvpn":
		_ = os.WriteFile(filepath.Join(markerDir, "openvpn-started"), []byte("ok"), 0o600)
		if os.Getenv("OVPN_RUNTIME_OPENVPN_EXIT") == "1" {
			os.Exit(4)
		}
		signals := make(chan os.Signal, 2)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		for value := range signals {
			if value == syscall.SIGHUP {
				_ = os.WriteFile(filepath.Join(markerDir, "openvpn-hup"), []byte("ok"), 0o600)
				continue
			}
			return
		}
	case "api":
		if os.Getenv("OVPN_RUNTIME_API_EXIT") == "1" {
			os.Exit(6)
		}
		signals := make(chan os.Signal, 1)
		signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
		_ = os.WriteFile(filepath.Join(markerDir, "api-started"), []byte(strconv.Itoa(os.Getpid())), 0o600)
		for value := range signals {
			if value == syscall.SIGHUP {
				_ = os.WriteFile(filepath.Join(markerDir, "api-hup"), []byte("ok"), 0600)
				continue
			}
			return
		}
	default:
		os.Exit(2)
	}
}

func TestSupervisorStartsForwardsHUPAndStopsProcesses(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	installHelperBuilder(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	runtimeDir := filepath.Join(t.TempDir(), "run")
	if err := os.MkdirAll(filepath.Join(dataDir, "server"), 0o750); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	hup := make(chan os.Signal, 1)
	done := make(chan error, 1)
	go func() {
		done <- testSupervisor(dataDir, runtimeDir).Run(ctx, hup, testInstance())
	}()
	waitForFile(t, filepath.Join(markers, "openvpn-started"))
	hup <- syscall.SIGHUP
	waitForFile(t, filepath.Join(markers, "openvpn-hup"))
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("supervisor shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not stop")
	}
	if _, err := os.Lstat(filepath.Join(runtimeDir, "management.sock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broker socket remains after shutdown: %v", err)
	}
}

func TestSupervisorStopsBrokerWhenOpenVPNExits(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	t.Setenv("OVPN_RUNTIME_OPENVPN_EXIT", "1")
	installHelperBuilder(t)
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "run"))
	err := supervisor.Run(context.Background(), nil, testInstance())
	if err == nil || !strings.Contains(err.Error(), "OpenVPN exited") {
		t.Fatalf("unexpected critical exit: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(markers, "broker-started")); statErr != nil {
		t.Fatalf("broker never started: %v", statErr)
	}
}

func TestSupervisorStopsOpenVPNWhenBrokerExits(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	t.Setenv("OVPN_RUNTIME_BROKER_EXIT", "1")
	installHelperBuilder(t)
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "run"))
	err := supervisor.Run(context.Background(), nil, testInstance())
	if err == nil || !strings.Contains(err.Error(), "management broker exited") {
		t.Fatalf("unexpected critical exit: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(markers, "openvpn-started")); statErr != nil {
		t.Fatalf("OpenVPN never started before broker exit: %v", statErr)
	}
}

func TestSupervisorStartsAndStopsOptionalAPI(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	installHelperBuilder(t)
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "run"), testAPIInstance())
	supervisor.APIBinary = "fake-api"
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx, nil, testAPIInstance()) }()
	waitForFile(t, filepath.Join(markers, "api-started"))
	waitForFile(t, filepath.Join(markers, "openvpn-started"))
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestSupervisorFailsWhenOptionalAPIExits(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	t.Setenv("OVPN_RUNTIME_API_EXIT", "1")
	installHelperBuilder(t)
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "run"), testAPIInstance())
	supervisor.APIBinary = "fake-api"
	err := supervisor.Run(context.Background(), nil, testAPIInstance())
	if err == nil || !strings.Contains(err.Error(), "management API exited") {
		t.Fatalf("unexpected API exit error: %v", err)
	}
}

func TestSupervisorRefusesUnsafeBackendPath(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "run")
	if err := os.MkdirAll(runtimeDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "openvpn-management.sock"), []byte("owned"), 0o600); err != nil {
		t.Fatal(err)
	}
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), runtimeDir)
	err := supervisor.Run(context.Background(), nil, testInstance())
	if err == nil || !strings.Contains(err.Error(), "non-socket") {
		t.Fatalf("unsafe backend path was not refused: %v", err)
	}
}

func TestSupervisorRefusesSecondServerProcess(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	installHelperBuilder(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	runtimeDir := filepath.Join(t.TempDir(), "run")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- testSupervisor(dataDir, runtimeDir).Run(ctx, nil, testInstance()) }()
	waitForFile(t, filepath.Join(markers, "openvpn-started"))
	second := testSupervisor(dataDir, runtimeDir)
	second.LockTimeout = 50 * time.Millisecond
	if err := second.Run(context.Background(), nil, testInstance()); !errors.Is(err, artifact.ErrLocked) {
		t.Fatalf("second supervisor error=%v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("first supervisor did not stop")
	}
}

func TestSupervisorHoldsSharedDataRuntimeLock(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	installHelperBuilder(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	runtimeDir := filepath.Join(t.TempDir(), "run")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- testSupervisor(dataDir, runtimeDir).Run(ctx, nil, testInstance()) }()
	waitForFile(t, filepath.Join(markers, "openvpn-started"))
	lockCtx, lockCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer lockCancel()
	if _, err := artifact.AcquireLock(lockCtx, artifact.RuntimeLockPath(dataDir), artifact.LockExclusive); !errors.Is(err, artifact.ErrLocked) {
		t.Fatalf("exclusive runtime lock error=%v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestSupervisorPausesForApplyAndRestartsRuntime(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	installHelperBuilder(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	runtimeDir := filepath.Join(t.TempDir(), "run")
	network := &recordingNetwork{}
	supervisor := testSupervisor(dataDir, runtimeDir)
	supervisor.APIBinary = "fake-api"
	supervisor.Network = network
	var reloads atomic.Int32
	supervisor.ReloadInstance = func(context.Context, string) (storesqlite.InstanceState, error) {
		reloads.Add(1)
		return testAPIInstance(), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx, nil, testAPIInstance()) }()
	waitForFile(t, filepath.Join(markers, "openvpn-started"))
	waitForFile(t, filepath.Join(markers, "api-started"))
	apiPIDData, err := os.ReadFile(filepath.Join(markers, "api-started"))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	apiPID, err := strconv.Atoi(string(apiPIDData))
	if err != nil {
		cancel()
		t.Fatal(err)
	}

	session, err := BeginApply(context.Background(), runtimeDir)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if _, err := BeginApply(context.Background(), runtimeDir); !errors.Is(err, ErrControlRejected) {
		cancel()
		t.Fatalf("concurrent apply error=%v", err)
	}
	exclusive, err := artifact.AcquireLock(context.Background(), artifact.RuntimeLockPath(dataDir), artifact.LockExclusive)
	if err != nil {
		cancel()
		t.Fatalf("exclusive lock while paused: %v", err)
	}
	if network.cleaned.Load() != 1 {
		t.Fatalf("network cleanup count while paused=%d", network.cleaned.Load())
	}
	if err := syscall.Kill(apiPID, 0); err != nil {
		cancel()
		t.Fatalf("management API stopped during apply: %v", err)
	}
	if err := exclusive.Release(); err != nil {
		cancel()
		t.Fatal(err)
	}
	if err := session.Resume(context.Background()); err != nil {
		cancel()
		t.Fatal(err)
	}
	if reloads.Load() != 2 || network.reconciled.Load() != 2 {
		cancel()
		t.Fatalf("reloads=%d reconciled=%d", reloads.Load(), network.reconciled.Load())
	}
	lockCtx, lockCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer lockCancel()
	if _, err := artifact.AcquireLock(lockCtx, artifact.RuntimeLockPath(dataDir), artifact.LockExclusive); !errors.Is(err, artifact.ErrLocked) {
		cancel()
		t.Fatalf("exclusive lock after restart error=%v", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not stop")
	}
}

func TestBeginApplyReportsUnavailableSupervisor(t *testing.T) {
	if _, err := BeginApply(context.Background(), t.TempDir()); !errors.Is(err, ErrControlUnavailable) {
		t.Fatalf("unavailable control error=%v", err)
	}
}

func TestSupervisorReconcilesBeforeStartAndCleansAfterStop(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	installHelperBuilder(t)
	network := &recordingNetwork{}
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "run"))
	supervisor.Network = network
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx, nil, testInstance()) }()
	waitForFile(t, filepath.Join(markers, "openvpn-started"))
	if network.reconciled.Load() != 1 || network.cleaned.Load() != 0 {
		t.Fatalf("network before stop reconciled=%d cleaned=%d", network.reconciled.Load(), network.cleaned.Load())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if network.cleaned.Load() != 1 {
		t.Fatalf("network cleanup count=%d", network.cleaned.Load())
	}
}

func TestSupervisorDoesNotStartProcessesWhenNetworkReconcileFails(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	installHelperBuilder(t)
	network := &recordingNetwork{err: errors.New("network denied")}
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "run"))
	supervisor.Network = network
	supervisor.APIBinary = "fake-api"
	err := supervisor.Run(context.Background(), nil, testInstance())
	if err == nil || !strings.Contains(err.Error(), "network denied") {
		t.Fatalf("network error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(markers, "broker-started")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broker started after network failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(markers, "api-started")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("management API started after network failure: %v", err)
	}
	if network.reconciled.Load() != 1 || network.cleaned.Load() != 0 {
		t.Fatalf("network calls reconciled=%d cleaned=%d", network.reconciled.Load(), network.cleaned.Load())
	}
}

func installHelperBuilder(t *testing.T) {
	t.Helper()
	previous := commandBuilder
	commandBuilder = func(name string, args ...string) *exec.Cmd {
		role := "openvpn"
		if strings.Contains(name, "broker") {
			role = "broker"
		} else if strings.Contains(name, "api") {
			role = "api"
		}
		helperArgs := []string{"-test.run=TestRuntimeHelperProcess", "--", role}
		return exec.Command(os.Args[0], append(helperArgs, args...)...)
	}
	t.Cleanup(func() { commandBuilder = previous })
}

func testSupervisor(dataDir, runtimeDir string, initial ...storesqlite.InstanceState) Supervisor {
	instance := testInstance()
	if len(initial) != 0 {
		instance = initial[0]
	}
	return Supervisor{ReloadInstance: func(context.Context, string) (storesqlite.InstanceState, error) { return instance, nil }, DataDir: dataDir, RuntimeDir: runtimeDir, OpenVPNBinary: "fake-openvpn", BrokerBinary: "fake-broker", StartupTimeout: time.Second, StopTimeout: time.Second, Network: noOpNetwork{}}
}

type noOpNetwork struct{}

func (noOpNetwork) Reconcile(context.Context, string, domain.Config) error { return nil }
func (noOpNetwork) Cleanup(context.Context, string) error                  { return nil }

type recordingNetwork struct {
	reconciled atomic.Int32
	cleaned    atomic.Int32
	err        error
}

func (network *recordingNetwork) Reconcile(context.Context, string, domain.Config) error {
	network.reconciled.Add(1)
	return network.err
}

func (network *recordingNetwork) Cleanup(context.Context, string) error {
	network.cleaned.Add(1)
	return nil
}

func testInstance() storesqlite.InstanceState {
	return storesqlite.InstanceState{Applied: configservice.AppliedSnapshot{Config: domain.Config{Logging: domain.LoggingConfig{MaxBytes: 1024, Backups: 1}}}}
}

func option(args []string, name string) string {
	for index := 0; index+1 < len(args); index += 2 {
		if args[index] == name {
			return args[index+1]
		}
	}
	return ""
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func testAPIInstance() storesqlite.InstanceState {
	instance := testInstance()
	instance.Applied.Config.API.Enabled = true
	return instance
}

func TestSupervisorActivatesAPIAndReloadsItAfterApply(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	installHelperBuilder(t)
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "run"))
	supervisor.APIBinary = "fake-api"
	var enabled atomic.Bool
	supervisor.ReloadInstance = func(context.Context, string) (storesqlite.InstanceState, error) {
		instance := testInstance()
		instance.Applied.Config.API.Enabled = enabled.Load()
		return instance, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx, nil, testInstance()) }()
	waitForFile(t, filepath.Join(markers, "openvpn-started"))
	if _, err := os.Stat(filepath.Join(markers, "api-started")); !os.IsNotExist(err) {
		t.Fatalf("disabled API unexpectedly started: %v", err)
	}
	enabled.Store(true)
	session, err := BeginApply(ctx, supervisor.RuntimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, filepath.Join(markers, "api-started"))
	enabled.Store(false)
	session, err = BeginApply(ctx, supervisor.RuntimeDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	waitForFile(t, filepath.Join(markers, "api-hup"))
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorLoadsAppliedStateUnderStartupLocks(t *testing.T) {
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "run"))
	rejected := errors.New("reject changed startup state")
	supervisor.ReloadInstance = func(ctx context.Context, dataDir string) (storesqlite.InstanceState, error) {
		for _, path := range []string{filepath.Join(supervisor.RuntimeDir, ".server.lock"), artifact.RuntimeLockPath(dataDir)} {
			lockCtx, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
			lock, err := artifact.AcquireLock(lockCtx, path, artifact.LockExclusive)
			cancel()
			if lock != nil {
				_ = lock.Release()
			}
			if !errors.Is(err, artifact.ErrLocked) {
				t.Errorf("startup state loaded without lock %s: %v", path, err)
			}
		}
		return storesqlite.InstanceState{}, rejected
	}
	if err := supervisor.Run(context.Background(), nil, testInstance()); !errors.Is(err, rejected) {
		t.Fatalf("startup ignored authoritative reload: %v", err)
	}
}

func TestSupervisorStartsWithUpdatedAPIConfiguration(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	installHelperBuilder(t)
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "run"), testAPIInstance())
	supervisor.APIBinary = "fake-api"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx, nil, testInstance()) }()
	waitForFile(t, filepath.Join(markers, "api-started"))
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// Network cleanup can take more than two seconds on busy or emulated hosts.
type slowCleanupNetwork struct{ noOpNetwork }

func (slowCleanupNetwork) Cleanup(ctx context.Context, _ string) error {
	select {
	case <-time.After(2100 * time.Millisecond):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSupervisorAllowsNetworkCleanupWithinStopTimeout(t *testing.T) {
	markers := t.TempDir()
	t.Setenv("OVPN_RUNTIME_HELPER", "1")
	t.Setenv("OVPN_RUNTIME_MARKERS", markers)
	installHelperBuilder(t)
	supervisor := testSupervisor(filepath.Join(t.TempDir(), "data"), filepath.Join(t.TempDir(), "run"))
	supervisor.StopTimeout = 3 * time.Second
	supervisor.Network = slowCleanupNetwork{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- supervisor.Run(ctx, nil, testInstance()) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("supervisor did not stop")
		}
	})
	waitForFile(t, filepath.Join(markers, "openvpn-started"))
	session, err := BeginApply(ctx, supervisor.RuntimeDir)
	if err != nil {
		t.Fatalf("cleanup interrupted before stop timeout: %v", err)
	}
	if err := session.Resume(ctx); err != nil {
		t.Fatalf("resume after slow cleanup: %v", err)
	}
}
