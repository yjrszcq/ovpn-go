package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/yjrszcq/ovpn-go/internal/apikey"
	"github.com/yjrszcq/ovpn-go/internal/artifact"
	clientservice "github.com/yjrszcq/ovpn-go/internal/client"
	"github.com/yjrszcq/ovpn-go/internal/compatibility"
	configservice "github.com/yjrszcq/ovpn-go/internal/config"
	configurationservice "github.com/yjrszcq/ovpn-go/internal/configuration"
	"github.com/yjrszcq/ovpn-go/internal/domain"
	"github.com/yjrszcq/ovpn-go/internal/httpapi"
	"github.com/yjrszcq/ovpn-go/internal/initialize"
	"github.com/yjrszcq/ovpn-go/internal/pki"
	"github.com/yjrszcq/ovpn-go/internal/render"
	runtimecontrol "github.com/yjrszcq/ovpn-go/internal/runtime"
	statecontrol "github.com/yjrszcq/ovpn-go/internal/state"
	storesqlite "github.com/yjrszcq/ovpn-go/internal/store/sqlite"
)

func main() {
	if err := run(); err != nil {
		log.Printf("ovpn-api: %v", err)
		os.Exit(1)
	}
}

func run() error {
	dataDir := environmentOr("OVPN_DATA_DIR", initialize.DefaultDataDir)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	defer signal.Stop(hup)
	database, err := storesqlite.Open(ctx, filepath.Join(dataDir, "meta", "state.db"))
	if err != nil {
		return fmt.Errorf("open authoritative state: %w", err)
	}
	defer database.Close()
	for {
		instance, err := database.LoadOnlyInstance(ctx)
		if err != nil {
			return fmt.Errorf("load instance: %w", err)
		}
		settings := configservice.EffectiveAPI(instance.Applied.Config.API)
		if !settings.Enabled {
			// Keep an already-running API child idle after disabling it. A later apply
			// can enable it again without dropping the current REST apply response.
			select {
			case <-ctx.Done():
				return nil
			case <-hup:
				continue
			}
		}
		authenticator, err := apikey.NewService(database, instance.ID)
		if err != nil {
			return err
		}
		resources, err := newResources(database, instance.ID, dataDir)
		if err != nil {
			return err
		}
		handler, err := httpapi.NewHandler(authenticator, settings.CORS, resources)
		if err != nil {
			return err
		}
		address := configservice.APIAddress(settings)
		server, err := httpapi.NewServer(address, handler)
		if err != nil {
			return err
		}
		listener, err := net.Listen("tcp", address)
		if err != nil {
			return fmt.Errorf("listen on API address: %w", err)
		}
		log.Printf("ovpn-api: listening on %s", listener.Addr())
		serveCtx, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- httpapi.Serve(serveCtx, listener, server) }()
		select {
		case <-ctx.Done():
			cancel()
			return <-done
		case <-hup:
			cancel()
			if err := <-done; err != nil {
				return err
			}
		case err := <-done:
			cancel()
			return err
		}
	}
}

func newResources(database *storesqlite.Store, instanceID, dataDir string) (httpapi.Resources, error) {
	local, err := artifact.NewLocal(dataDir)
	if err != nil {
		return httpapi.Resources{}, err
	}
	clients, err := clientservice.NewService(database, local)
	if err != nil {
		return httpapi.Resources{}, err
	}
	contractPath := environmentOr("OVPN_COMPATIBILITY_FILE", compatibility.DefaultContractPath)
	contract, err := compatibility.Load(contractPath)
	if err != nil {
		return httpapi.Resources{}, fmt.Errorf("load compatibility contract: %w", err)
	}
	renderer, err := render.New(environmentOr("OVPN_TEMPLATE_ROOT", render.DefaultTemplateRoot), contract)
	if err != nil {
		return httpapi.Resources{}, err
	}
	runtimeDir := environmentOr("OVPN_RUNTIME_DIR", initialize.DefaultRuntimeDir)
	runner, err := pki.NewRunner(pki.Config{
		EasyRSABinary: apiEasyRSABinary(),
		OpenVPNBinary: environmentOr("OVPN_OPENVPN_BIN", "openvpn"),
	}, nil)
	if err != nil {
		return httpapi.Resources{}, err
	}
	manager, err := clientservice.NewManager(database, local, runner, renderer, render.Paths{DataDir: dataDir, RuntimeDir: runtimeDir})
	if err != nil {
		return httpapi.Resources{}, err
	}
	stateOptions := statecontrol.Options{
		DataDir: dataDir, ConfigFile: environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath),
		ServerName: initialize.DefaultServerName, Renderer: renderer,
		Paths: render.Paths{DataDir: dataDir, RuntimeDir: runtimeDir},
	}
	configurationQueries, err := configurationservice.NewService(database)
	if err != nil {
		return httpapi.Resources{}, err
	}
	configPath := environmentOr("OVPN_CONFIG_FILE", configservice.DefaultPath)
	loadConfigState := func(ctx context.Context, expectedDigest string, expectedRevision uint64) (domain.Config, error) {
		desired, err := configservice.LoadFile(configPath)
		if err != nil {
			return domain.Config{}, err
		}
		digest, err := configservice.Digest(desired)
		if err != nil || digest != expectedDigest {
			return domain.Config{}, configservice.ErrDesiredConflict
		}
		instance, err := database.LoadOnlyInstance(ctx)
		if err != nil {
			return domain.Config{}, err
		}
		if uint64(instance.Applied.Revision) != expectedRevision {
			return domain.Config{}, configurationservice.ErrPlanConflict
		}
		return desired, nil
	}
	return httpapi.Resources{
		Version: func() httpapi.VersionResponse { return httpapi.NewVersionResponse(contract) },
		State: func(ctx context.Context) (statecontrol.Report, error) {
			return statecontrol.Scan(ctx, stateOptions), nil
		},
		Clients:   clients,
		Mutations: manager,
		Runtime: func(ctx context.Context) (runtimecontrol.Status, error) {
			identities, err := database.ClientIdentities(ctx, instanceID)
			if err != nil {
				return runtimecontrol.Status{}, err
			}
			return runtimecontrol.QueryStatus(ctx, runtimecontrol.SocketPath(runtimeDir), identities)
		},
		Events: func(ctx context.Context, lines int) ([]runtimecontrol.Event, error) {
			values := make([]runtimecontrol.Event, 0)
			err := runtimecontrol.StreamLines(ctx, runtimecontrol.StreamOptions{Path: filepath.Join(dataDir, "logs", "events.jsonl"), Lines: lines}, func(line string) error {
				event, err := runtimecontrol.ParseEvent(line)
				if err == nil {
					values = append(values, event)
				}
				return err
			})
			return values, err
		},
		Disconnect: func(ctx context.Context, id, name string) (runtimecontrol.DisconnectResult, error) {
			return runtimecontrol.Disconnect(ctx, runtimecontrol.SocketPath(runtimeDir), id, name)
		},
		Applied: func(ctx context.Context) (configservice.AppliedView, error) {
			instance, err := database.LoadOnlyInstance(ctx)
			if err != nil {
				return configservice.AppliedView{}, err
			}
			return configservice.Show(instance.Applied)
		},
		Desired: func(context.Context) (configservice.DesiredView, error) { return configservice.LoadDesired(configPath) },
		PutDesired: func(_ context.Context, expected string, view configservice.View) (configservice.DesiredView, error) {
			return configservice.UpdateDesiredFile(configPath, expected, view)
		},
		ConfigPlan: func(ctx context.Context) (configurationservice.Plan, error) {
			desired, err := configservice.LoadFile(configPath)
			if err != nil {
				return configurationservice.Plan{}, err
			}
			return configurationQueries.Plan(ctx, desired)
		},
		ConfigApply: func(ctx context.Context, input httpapi.ConfigApplyRequest) (configurationservice.ApplyResult, error) {
			desired, err := loadConfigState(ctx, input.DesiredDigest, input.CurrentRevision)
			if err != nil {
				return configurationservice.ApplyResult{}, err
			}
			plan, err := configurationQueries.Plan(ctx, desired)
			if err != nil {
				return configurationservice.ApplyResult{}, err
			}
			if !input.Force {
				options := stateOptions
				options.ConfigFile = ""
				report := statecontrol.Scan(ctx, options)
				if report.State != statecontrol.Healthy {
					return configurationservice.ApplyResult{}, configurationservice.ErrPlanConflict
				}
			}
			if plan.Configuration.InSync {
				return configurationservice.ApplyResult{
					Version: 1,
					Activation: configurationservice.ActivationReport{
						ProfileRedistribution: append(make([]configurationservice.ClientRef, 0, len(plan.ProfileRedistribution)), plan.ProfileRedistribution...),
					},
					Plan: plan,
				}, nil
			}
			session, err := runtimecontrol.BeginApply(ctx, runtimeDir)
			if err != nil {
				log.Printf("configuration apply: pause runtime: %v", err)
				return configurationservice.ApplyResult{}, err
			}
			desired, err = loadConfigState(ctx, input.DesiredDigest, input.CurrentRevision)
			var result configurationservice.ApplyResult
			if err == nil {
				result, err = configurationservice.ApplyPersistent(ctx, desired, renderer, render.Paths{DataDir: dataDir, RuntimeDir: runtimeDir})
			}
			resumeCtx, cancelResume := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancelResume()
			if resumeErr := session.Resume(resumeCtx); resumeErr != nil {
				log.Printf("configuration apply: resume runtime: %v", resumeErr)
				return configurationservice.ApplyResult{}, resumeErr
			}
			if err == nil {
				result.Activation.RuntimeRestarted = true
				result.Activation.RestartRequired = false
			}
			return result, err
		},
	}, nil
}

func environmentOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func apiEasyRSABinary() string {
	if value := os.Getenv("OVPN_EASYRSA_BIN"); value != "" {
		return value
	}
	if info, err := os.Stat("/usr/share/easy-rsa/easyrsa"); err == nil && info.Mode().IsRegular() {
		return "/usr/share/easy-rsa/easyrsa"
	}
	return "easyrsa"
}
