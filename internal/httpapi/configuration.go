package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/yjrszcq/ovpn-go/internal/artifact"
	configservice "github.com/yjrszcq/ovpn-go/internal/config"
	configurationservice "github.com/yjrszcq/ovpn-go/internal/configuration"
	runtimecontrol "github.com/yjrszcq/ovpn-go/internal/runtime"
	storesqlite "github.com/yjrszcq/ovpn-go/internal/store/sqlite"
)

// desiredConfiguration contains only remotely writable VPN settings.
// The full config.View also contains local-only API listener settings.
type desiredConfiguration struct {
	Version int `json:"version"`
	Server  struct {
		Endpoint       string `json:"endpoint"`
		Protocol       string `json:"protocol"`
		Family         string `json:"family"`
		Port           uint16 `json:"port"`
		ClientToClient bool   `json:"client_to_client"`
	} `json:"server"`
	IPv4 struct {
		Network         string   `json:"network"`
		DynamicPoolSize uint64   `json:"dynamic_pool_size"`
		NATEnabled      bool     `json:"nat_enabled"`
		NATInterface    string   `json:"nat_interface"`
		RedirectGateway bool     `json:"redirect_gateway"`
		DNS             []string `json:"dns"`
		Routes          []string `json:"routes"`
	} `json:"ipv4"`
	Logging struct {
		MaxBytes uint64 `json:"max_bytes"`
		Backups  uint32 `json:"backups"`
	} `json:"logging"`
}

func (value desiredConfiguration) view() configservice.View {
	return configservice.View{Version: value.Version, Server: value.Server, IPv4: value.IPv4, Logging: value.Logging}
}

type ConfigApplyRequest struct {
	DesiredDigest   string `json:"desired_digest"`
	CurrentRevision uint64 `json:"current_revision"`
	Force           bool   `json:"force"`
}

type ConfigRestoreRequest struct {
	CurrentRevision uint64 `json:"current_revision"`
	Confirm         bool   `json:"confirm"`
}

func (handler *handler) routeConfiguration(response http.ResponseWriter, request *http.Request, requestID string) bool {
	path := request.URL.Path
	if handler.resources.Applied == nil || !strings.HasPrefix(path, "/api/v1/config/") {
		return false
	}
	if request.URL.RawQuery != "" {
		writeAPIError(response, http.StatusBadRequest, "invalid_query", "query parameters are not accepted", requestID)
		return true
	}
	switch path {
	case "/api/v1/config/export":
		if request.Method != http.MethodGet {
			return rejectMethod(response, requestID, http.MethodGet)
		}
		content, err := handler.resources.ConfigExport(request.Context())
		if err != nil {
			handler.writeConfigurationResult(response, nil, err, requestID)
			return true
		}
		response.Header().Set("Content-Type", "application/yaml")
		response.Header().Set("Content-Disposition", `attachment; filename="config.yaml"`)
		_, _ = response.Write(content)
		return true
	case "/api/v1/config/restore":
		if request.Method != http.MethodPost {
			return rejectMethod(response, requestID, http.MethodPost)
		}
		var input ConfigRestoreRequest
		if err := decodeJSON(response, request, &input); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "request body is invalid", requestID)
			return true
		}
		if input.CurrentRevision == 0 || !input.Confirm {
			writeAPIError(response, http.StatusUnprocessableEntity, "invalid_configuration", "current_revision and confirm=true are required", requestID)
			return true
		}
		value, err := handler.resources.ConfigRestore(request.Context(), input)
		if err == nil {
			response.Header().Set("ETag", `"`+value.Digest+`"`)
		}
		handler.writeConfigurationResult(response, value, err, requestID)
		return true
	case "/api/v1/config/applied":
		if request.Method != http.MethodGet {
			return rejectMethod(response, requestID, http.MethodGet)
		}
		value, err := handler.resources.Applied(request.Context())
		handler.writeConfigurationResult(response, value, err, requestID)
		return true
	case "/api/v1/config/desired":
		if request.Method == http.MethodGet {
			value, err := handler.resources.Desired(request.Context())
			handler.writeDesiredResult(response, value, err, requestID)
			return true
		}
		if request.Method != http.MethodPut {
			return rejectMethod(response, requestID, http.MethodGet+", "+http.MethodPut)
		}
		expected, err := parseIfMatch(request.Header.Get("If-Match"))
		if err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_if_match", "If-Match must contain one quoted digest", requestID)
			return true
		}
		var input desiredConfiguration
		if err := decodeJSON(response, request, &input); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "request body is invalid", requestID)
			return true
		}
		value, err := handler.resources.PutDesired(request.Context(), expected, input.view())
		handler.writeDesiredResult(response, value, err, requestID)
		return true
	case "/api/v1/config/plan":
		if request.Method != http.MethodGet {
			return rejectMethod(response, requestID, http.MethodGet)
		}
		value, err := handler.resources.ConfigPlan(request.Context())
		handler.writeConfigurationResult(response, value, err, requestID)
		return true
	case "/api/v1/config/apply":
		if request.Method != http.MethodPost {
			return rejectMethod(response, requestID, http.MethodPost)
		}
		var input ConfigApplyRequest
		if err := decodeJSON(response, request, &input); err != nil {
			writeAPIError(response, http.StatusBadRequest, "invalid_json", "request body is invalid", requestID)
			return true
		}
		if !validDigest(input.DesiredDigest) || input.CurrentRevision == 0 {
			writeAPIError(response, http.StatusUnprocessableEntity, "invalid_configuration", "configuration apply preconditions are invalid", requestID)
			return true
		}
		value, err := handler.resources.ConfigApply(request.Context(), input)
		handler.writeConfigurationResult(response, value, err, requestID)
		return true
	}
	return false
}

func (handler *handler) writeDesiredResult(response http.ResponseWriter, value configservice.DesiredView, err error, requestID string) {
	if err == nil {
		response.Header().Set("ETag", `"`+value.Digest+`"`)
	}
	handler.writeConfigurationResult(response, value, err, requestID)
}

func parseIfMatch(value string) (string, error) {
	if len(value) != 66 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", errors.New("invalid If-Match")
	}
	digest := value[1 : len(value)-1]
	if !validDigest(digest) {
		return "", errors.New("invalid digest")
	}
	return digest, nil
}

func validDigest(digest string) bool {
	if len(digest) != 64 {
		return false
	}
	if _, err := strconv.ParseUint(digest[:16], 16, 64); err != nil {
		return false
	}
	for _, character := range digest {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}

func (handler *handler) writeConfigurationResult(response http.ResponseWriter, value any, err error, requestID string) {
	switch {
	case err == nil:
		writeJSON(response, http.StatusOK, value)
	case errors.Is(err, configservice.ErrDesiredConflict), errors.Is(err, configurationservice.ErrPlanConflict), errors.Is(err, artifact.ErrLocked), errors.Is(err, storesqlite.ErrBusy):
		writeAPIError(response, http.StatusConflict, "configuration_conflict", "configuration state changed or is busy", requestID)
	case errors.Is(err, runtimecontrol.ErrControlUnavailable), errors.Is(err, runtimecontrol.ErrControlRejected):
		writeAPIError(response, http.StatusServiceUnavailable, "runtime_unavailable", "runtime supervisor is unavailable", requestID)
	case errors.Is(err, configurationservice.ErrRecoveryRequired):
		writeAPIError(response, http.StatusConflict, "recovery_required", "interrupted operation recovery is required", requestID)
	case errors.Is(err, configservice.ErrUnsafeTarget):
		writeAPIError(response, http.StatusUnprocessableEntity, "unsafe_config_target", "configuration target must be a regular file", requestID)
	case errors.Is(err, configservice.ErrLocalAPIConfig):
		writeAPIError(response, http.StatusUnprocessableEntity, "local_configuration_required", "API settings must be applied locally through the CLI", requestID)
	case errors.Is(err, configservice.ErrInvalidDesired):
		writeAPIError(response, http.StatusUnprocessableEntity, "invalid_configuration", "configuration is invalid", requestID)
	default:
		writeAPIError(response, http.StatusInternalServerError, "internal_error", "request could not be completed", requestID)
	}
}
