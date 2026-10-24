package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/yjrszcq/ovpn-go/internal/apperror"
	configservice "github.com/yjrszcq/ovpn-go/internal/config"
)

func runConfigEdit(args []string, stdout, stderr io.Writer) int {
	editor, action := "", ""
	var forwarded []string
	seen := make(map[string]bool)
	for index := 0; index < len(args); index++ {
		option := args[index]
		switch option {
		case "-e":
			option = "--editor"
		case "-p":
			option = "--plan"
		case "-a":
			option = "--apply"
		case "-y":
			option = "--yes"
		case "-f":
			option = "--force"
		case "-j":
			option = "--json"
		}
		if seen[option] {
			return writeError(stderr, usageError(option+" may only be specified once"))
		}
		seen[option] = true
		switch option {
		case "--editor":
			index++
			if index >= len(args) || args[index] == "" {
				return writeError(stderr, usageError("--editor requires an executable name or path"))
			}
			editor = args[index]
		case "--plan", "--apply":
			if action != "" {
				return writeError(stderr, usageError("--plan and --apply are mutually exclusive"))
			}
			action = option
		case "--yes", "--force", "--json":
			forwarded = append(forwarded, option)
		default:
			return writeError(stderr, usageError("usage: ovpn config edit [-e EDITOR] [-p | -a] [-y] [-f] [-j]"))
		}
	}
	if (seen["--yes"] || seen["--force"]) && action != "--apply" || seen["--json"] && action == "" {
		return writeError(stderr, usageError("--yes/--force require --apply; --json requires --plan or --apply"))
	}
	path := environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath)
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return writeError(stderr, apperror.New(apperror.ExitData, "configuration_missing", "configuration file is missing or not a regular file: "+path))
	}
	if err := runAddressEditor(editor, path); err != nil {
		var exited *exec.ExitError
		if errors.As(err, &exited) && exited.ExitCode() > 0 {
			fmt.Fprintln(stderr, "ovpn: editor failed; configuration was not planned or applied")
			return exited.ExitCode()
		}
		return writeError(stderr, apperror.Wrap(apperror.ExitUnavailable, "editor_failed", "configuration editor failed", err))
	}
	switch action {
	case "--plan":
		return runConfigPlan(forwarded, stdout, stderr)
	case "--apply":
		return runConfigApply(forwarded, stdout, stderr)
	default:
		return int(apperror.ExitSuccess)
	}
}
