package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrUnsafeTarget = errors.New("unsafe configuration target")

// RestoreFile replaces configured YAML without parsing its current contents.
// It shares serialization with HTTP desired updates and refuses unsafe targets.
func RestoreFile(path string, content []byte) error {
	desiredUpdateMu.Lock()
	defer desiredUpdateMu.Unlock()
	before, err := restoreTarget(path)
	if err != nil {
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
	after, err := restoreTarget(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(before, after) {
		return ErrDesiredConflict
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

// Prefix distinguishes an absent file from an existing empty file.
func restoreTarget(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return []byte{0}, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: configuration target must be a regular file", ErrUnsafeTarget)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return append([]byte{1}, content...), nil
}
