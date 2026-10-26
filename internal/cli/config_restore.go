package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yjrszcq/ovpn-go/internal/apperror"
	configservice "github.com/yjrszcq/ovpn-go/internal/config"
	"github.com/yjrszcq/ovpn-go/internal/initialize"
)

func restoreConfiguration(yes bool, stdout, stderr io.Writer) int {
	empty, err := dataDirectoryEmpty(environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir))
	if err != nil {
		return writeConfigurationError(stderr, err, false)
	}
	source := "default configuration"
	var content []byte
	if empty {
		// Use the same strict parser/defaults as normal YAML configuration.
		value, parseErr := configservice.Parse([]byte("version: 1\nserver:\n  endpoint: vpn.example.com\nipv4:\n  network: 10.42.0.0/24\n  dynamicPoolSize: 64\n"))
		if parseErr != nil {
			return writeConfigurationError(stderr, parseErr, false)
		}
		snapshot, snapshotErr := configservice.NewAppliedSnapshot(1, value)
		if snapshotErr != nil {
			return writeConfigurationError(stderr, snapshotErr, false)
		}
		content, err = configservice.ExportYAML(snapshot)
	} else {
		store, instance, openErr := openConfigurationState(context.Background())
		if openErr != nil {
			return writeConfigurationError(stderr, openErr, false)
		}
		defer store.Close()
		content, err = configservice.ExportYAML(instance.Applied)
		source = fmt.Sprintf("applied revision %d", instance.Applied.Revision)
	}
	if err != nil {
		return writeConfigurationError(stderr, err, false)
	}
	path := environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath)
	if !yes {
		confirmed, err := confirmAction(stderr, fmt.Sprintf("Type yes to overwrite %s with %s, discarding file edits and comments: ", path, source))
		if err != nil || !confirmed {
			return writeError(stderr, apperror.Wrap(apperror.ExitPolicy, "confirmation_required", "config export --restore requires confirmation or --yes", err))
		}
	}
	if err := restoreConfigFile(path, content); err != nil {
		return writeError(stderr, apperror.Wrap(apperror.ExitFailure, "output_failure", "restore configuration file", err))
	}
	if empty {
		fmt.Fprintf(stdout, "Generated default configuration at %s. Edit server.endpoint before initialization.\n", path)
	} else {
		fmt.Fprintf(stdout, "Restored %s from %s.\n", path, source)
	}
	return int(apperror.ExitSuccess)
}

func restoreConfigFile(path string, content []byte) error {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("configuration target must be a regular file: %s", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".config-restore-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}
