package config_test

import (
	"github.com/yjrszcq/ovpn-go/internal/config"
	"strings"
	"testing"
)

func TestAPIConfigurationDefaultsAndRoundTrip(t *testing.T) {
	value, err := config.Parse([]byte(minimalConfig))
	if err != nil {
		t.Fatal(err)
	}
	if value.API.Enabled || value.API.ListenIP != "127.0.0.1" || value.API.Port != 11940 || len(value.API.CORS) != 0 {
		t.Fatalf("defaults=%+v", value.API)
	}
	configured := minimalConfig + "\napi:\n  enabled: true\n  listenIP: '::1'\n  port: 12400\n  cors: [console.example:3000, localhost]\n"
	value, err = config.Parse([]byte(configured))
	if err != nil {
		t.Fatal(err)
	}
	if config.APIAddress(value.API) != "[::1]:12400" {
		t.Fatal(value.API)
	}
	snapshot, err := config.NewAppliedSnapshot(1, value)
	if err != nil {
		t.Fatal(err)
	}
	exported, err := config.ExportYAML(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := config.Parse(exported)
	if err != nil {
		t.Fatal(err)
	}
	equal, err := config.EqualCanonical(value, restored)
	if err != nil || !equal {
		t.Fatalf("roundtrip equal=%t err=%v", equal, err)
	}
	view := config.NewView(value)
	view.API.Port = 0
	if _, err := config.FromView(view); err == nil {
		t.Fatal("JSON API port zero was accepted")
	}
	defaults := mustConfig(t, minimalConfig)
	plan, err := config.BuildPlan(&defaults, value)
	if err != nil || len(plan.Changes) != 4 || !plan.Impact.RestartRequired || plan.Impact.ProfileRedistribution {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	for _, item := range []string{"enabled: maybe", "listenIP: localhost", "listenIP: ''", "listenIP: 'fe80::1%eth0'", "port: 0", "port: 65536", "cors: ['https://example.com']", "cors: ['*', localhost]", "cors: [localhost, LOCALHOST]", "cors: ['']"} {
		if _, err := config.Parse([]byte(minimalConfig + "\napi:\n  " + item + "\n")); err == nil {
			t.Fatalf("accepted %s", item)
		}
	}
}

func TestAPIBootstrapConfiguration(t *testing.T) {
	values := map[string]string{"OVPN_BOOTSTRAP_ENDPOINT": "vpn.example.com", "OVPN_BOOTSTRAP_IPV4_NETWORK": "10.42.0.0/24", "OVPN_BOOTSTRAP_API_ENABLED": "true", "OVPN_BOOTSTRAP_API_LISTEN_IP": "0.0.0.0", "OVPN_BOOTSTRAP_API_PORT": "12000", "OVPN_BOOTSTRAP_API_CORS": "console.example:3000,localhost"}
	lookup := func(name string) (string, bool) { value, ok := values[name]; return value, ok }
	value, err := config.BootstrapConfig(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if !value.API.Enabled || value.API.ListenIP != "0.0.0.0" || value.API.Port != 12000 || strings.Join(value.API.CORS, ",") != "console.example:3000,localhost" {
		t.Fatalf("API=%+v", value.API)
	}
}
