package sqlite

import (
	"context"
	configservice "github.com/yjrszcq/ovpn-go/internal/config"
	"reflect"
	"testing"
)

func TestAPIConfigurationPersistsAndMigratesDefaultSnapshots(t *testing.T) {
	store, instance := storeWithInstance(t)
	originalDigest := instance.Applied.Digest
	path := store.Path()
	if _, err := store.db.Exec("DROP TABLE applied_api_config; UPDATE schema_metadata SET database_revision=9"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	migrated, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	loaded, err := migrated.LoadOnlyInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Applied.Digest != originalDigest || loaded.Applied.Config.API.Enabled || migrated.Metadata().DataSchema != 4 {
		t.Fatalf("migration changed defaults: %+v", loaded)
	}
	desired := loaded.Applied.Config
	desired.API.Enabled = true
	desired.API.ListenIP = "::1"
	desired.API.Port = 12000
	desired.API.CORS = []string{"localhost:3000"}
	snapshot, err := configservice.NewAppliedSnapshot(loaded.Applied.Revision+1, desired)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrated.ApplyConfig(context.Background(), loaded.ID, snapshot); err != nil {
		t.Fatal(err)
	}
	loaded, err = migrated.LoadOnlyInstance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded.Applied.Config.API, desired.API) || loaded.Applied.Digest != snapshot.Digest {
		t.Fatalf("API lost: %+v", loaded.Applied)
	}
}
