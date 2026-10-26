package configuration

import (
	"context"
	"path/filepath"

	"github.com/yjrszcq/ovpn-go/internal/artifact"
	configservice "github.com/yjrszcq/ovpn-go/internal/config"
	storesqlite "github.com/yjrszcq/ovpn-go/internal/store/sqlite"
)

type RestoreStore interface {
	LoadOnlyInstance(context.Context) (storesqlite.InstanceState, error)
	PendingOperations(context.Context, string) ([]storesqlite.Operation, error)
}

// Restore writes only desired YAML while holding the same data lock as apply.
// It requires initialized state; default generation remains a local CLI action.
func Restore(ctx context.Context, store RestoreStore, dataDir, path string, expected configservice.Revision) (configservice.AppliedView, error) {
	lock, err := acquireApplyLock(ctx, filepath.Join(dataDir, ".ovpn-data.lock"))
	if err != nil {
		return configservice.AppliedView{}, err
	}
	defer lock.Release()
	instance, err := store.LoadOnlyInstance(ctx)
	if err != nil {
		return configservice.AppliedView{}, err
	}
	if expected == 0 || instance.Applied.Revision != expected {
		return configservice.AppliedView{}, ErrPlanConflict
	}
	pending, err := store.PendingOperations(ctx, instance.ID)
	if err != nil {
		return configservice.AppliedView{}, err
	}
	if len(pending) != 0 {
		return configservice.AppliedView{}, ErrRecoveryRequired
	}
	local, err := artifact.NewLocal(dataDir)
	if err != nil {
		return configservice.AppliedView{}, err
	}
	artifacts, err := local.PendingOperations()
	if err != nil {
		return configservice.AppliedView{}, err
	}
	if len(artifacts) != 0 {
		return configservice.AppliedView{}, ErrRecoveryRequired
	}
	view, err := configservice.Show(instance.Applied)
	if err != nil {
		return configservice.AppliedView{}, err
	}
	content, err := configservice.ExportYAML(instance.Applied)
	if err != nil {
		return configservice.AppliedView{}, err
	}
	if err := configservice.RestoreFile(path, content); err != nil {
		return configservice.AppliedView{}, err
	}
	return view, nil
}
