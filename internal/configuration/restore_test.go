package configuration

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yjrszcq/ovpn-go/internal/artifact"
	configservice "github.com/yjrszcq/ovpn-go/internal/config"
)

func TestRestoreChecksRevisionAndPreservesAppliedState(t *testing.T) {
	_, store, root, _, instance := applyFixture(t)
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, invalid := range []bool{false, true} {
		if invalid {
			if err := os.WriteFile(path, []byte("invalid: ["), 0600); err != nil {
				t.Fatal(err)
			}
		}
		result, err := Restore(context.Background(), store, root, path, instance.Applied.Revision)
		if err != nil || result.Digest != instance.Applied.Digest {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		desired, err := configservice.LoadDesired(path)
		if err != nil || desired.Digest != instance.Applied.Digest {
			t.Fatalf("desired=%+v err=%v", desired, err)
		}
	}
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(context.Background(), store, root, path, instance.Applied.Revision+1); !errors.Is(err, ErrPlanConflict) {
		t.Fatalf("stale revision err=%v", err)
	}
	content, _ := os.ReadFile(path)
	if string(content) != "keep" {
		t.Fatal("conflict overwrote file")
	}
	after, err := store.LoadOnlyInstance(context.Background())
	if err != nil || after.Applied.Digest != instance.Applied.Digest || after.Applied.Revision != instance.Applied.Revision {
		t.Fatalf("DB changed: %+v %v", after, err)
	}
}

func TestRestoreHonorsApplyDataLock(t *testing.T) {
	_, store, root, _, instance := applyFixture(t)
	lock, err := artifact.AcquireLock(context.Background(), filepath.Join(root, ".ovpn-data.lock"), artifact.LockExclusive)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := Restore(context.Background(), store, root, path, instance.Applied.Revision); !errors.Is(err, artifact.ErrLocked) {
		t.Fatalf("lock err=%v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("busy restore wrote YAML")
	}
}
