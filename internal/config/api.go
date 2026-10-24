package config

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/yjrszcq/ovpn-go/internal/domain"
)

// DefaultAPI keeps REST management disabled and bound to IPv4 loopback.
func DefaultAPI() domain.APIConfig {
	return domain.APIConfig{ListenIP: "127.0.0.1", Port: 11940, CORS: []string{}}
}

// EffectiveAPI fills omitted domain defaults, including pre-API snapshots.
func EffectiveAPI(value domain.APIConfig) domain.APIConfig {
	if value.ListenIP == "" {
		value.ListenIP = "127.0.0.1"
	}
	if value.Port == 0 {
		value.Port = 11940
	}
	if value.CORS == nil {
		value.CORS = []string{}
	}
	return value
}

func normalizeAPI(raw yamlAPI) (domain.APIConfig, error) {
	value := DefaultAPI()
	value.Enabled = raw.Enabled
	if raw.ListenIP != nil {
		address, err := netip.ParseAddr(*raw.ListenIP)
		if err != nil || address.Zone() != "" {
			return value, fmt.Errorf("api.listenIP must be an IPv4 or IPv6 address without a zone")
		}
		value.ListenIP = address.String()
	}
	if raw.Port != nil {
		value.Port = *raw.Port
		if value.Port == 0 {
			return value, fmt.Errorf("api.port must be between 1 and 65535")
		}
	}
	seen := map[string]bool{}
	for _, origin := range raw.CORS {
		normalized, err := NormalizeCORSOrigin(origin)
		if err != nil {
			return value, fmt.Errorf("api.cors: %w", err)
		}
		if seen[normalized] {
			return value, fmt.Errorf("api.cors origins must be unique")
		}
		seen[normalized] = true
		if normalized == "*" && len(raw.CORS) != 1 {
			return value, fmt.Errorf("api.cors wildcard must be used alone")
		}
		value.CORS = append(value.CORS, normalized)
	}
	return value, nil
}

// APIAddress formats an already validated API listener, including IPv6 brackets.
func APIAddress(value domain.APIConfig) string {
	value = EffectiveAPI(value)
	return net.JoinHostPort(value.ListenIP, strconv.Itoa(int(value.Port)))
}

// NormalizeCORSOrigin accepts host[:port] or a standalone wildcard.
func NormalizeCORSOrigin(origin string) (string, error) {
	if origin == "*" {
		return origin, nil
	}
	parsed, err := url.Parse("//" + origin)
	if err != nil || origin == "" || strings.Contains(origin, "://") || strings.ContainsAny(origin, " \t\r\n*#") || parsed.Host == "" || parsed.Hostname() == "" || parsed.User != nil || parsed.Path != "" || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" || strings.HasSuffix(parsed.Host, ":") {
		return "", fmt.Errorf("CORS origins must be hostnames or IP addresses with an optional port, without a scheme")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", fmt.Errorf("CORS origin ports must be between 1 and 65535")
		}
	}
	return strings.ToLower(parsed.Host), nil
}
