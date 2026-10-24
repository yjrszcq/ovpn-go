package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigEditOnlyUsesConfiguredFileAndEditor(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configuration with spaces.yaml")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(dir, "editor")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nprintf 'edited' >\"$1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVPN_CONFIG_FILE", path)
	t.Setenv("EDITOR", editor)
	t.Setenv("OVPN_EDITOR", "")
	var output bytes.Buffer
	if code := Run([]string{"config", "edit"}, &output, &output); code != 0 {
		t.Fatalf("code=%d output=%s", code, &output)
	}
	content, _ := os.ReadFile(path)
	if string(content) != "edited" {
		t.Fatalf("content=%q", content)
	}
	// Explicit editor overrides the configured one; errors stop before plan/apply.
	bad := filepath.Join(dir, "bad-editor")
	os.WriteFile(bad, []byte("#!/bin/sh\nexit 17\n"), 0700)
	if code := Run([]string{"config", "edit", "--editor", bad, "--apply", "-y"}, &output, &output); code != 17 {
		t.Fatalf("code=%d output=%s", code, &output)
	}
	// Plan runs validation after editing and never applies an invalid draft.
	output.Reset()
	if code := Run([]string{"config", "edit", "-e", editor, "-p"}, &output, &output); code == 0 || !strings.Contains(output.String(), "configuration is invalid") {
		t.Fatalf("code=%d output=%s", code, &output)
	}
}

func TestConfigEditRejectsInvalidOptionsBeforeInvokingEditor(t *testing.T) {
	for _, args := range [][]string{{"-p", "-a"}, {"--plan", "--apply"}, {"-e"}, {"-y"}, {"-f"}, {"-j"}, {"-p", "-p"}, {"--plan", "--force"}} {
		var output bytes.Buffer
		if code := Run(append([]string{"config", "edit"}, args...), &output, &output); code != 64 {
			t.Fatalf("args=%v code=%d output=%s", args, code, &output)
		}
	}
}
