package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRestoreFileAcceptsMissingAndCorruptTargets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, initial := range []string{"", "invalid: ["} {
		if initial != "" {
			if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := RestoreFile(path, []byte("version: 1\n")); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(path)
		if err != nil || string(content) != "version: 1\n" {
			t.Fatalf("content=%q err=%v", content, err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("info=%v err=%v", info, err)
		}
	}
}

func TestRestoreFileRefusesSymlinksAndDirectories(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "original")
	if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{root, link} {
		if err := RestoreFile(path, []byte("overwrite")); !errors.Is(err, ErrUnsafeTarget) {
			t.Fatalf("path=%s err=%v", path, err)
		}
	}
	content, _ := os.ReadFile(target)
	if string(content) != "keep" {
		t.Fatal("unsafe restoration changed original")
	}
	if err := RestoreFile(filepath.Join(root, "missing", "config.yaml"), []byte("overwrite")); err == nil {
		t.Fatal("accepted missing parent")
	}
}
