package main

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/signal"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/hubertat/servicemaker"
	"github.com/hubertat/swkit"
	"github.com/hubertat/swkit/agent"
	"github.com/hubertat/swkit/logging"
	"github.com/hubertat/swkit/server"
	"github.com/hubertat/swkit/ui/tui"
)

const defaultSyncInterval = "330ms"
const defaultSensorsSyncInterval = "10s"
const defaultForceSyncEveryCycle = 1000

var (
	Version string
	Build   string

	config              = flag.String("config", "config.json", "path of the configuration file")
	flagInstall         = flag.Bool("install", false, "Install service in os")
	syncInterval        = flag.String("sync", defaultSyncInterval, "sync interval (time.Duration)")
	forceSyncEveryCycle = flag.Int("force-sync-every", defaultForceSyncEveryCycle, "force sync every n cycles")
	sensorsSyncInterval = flag.String("sensors-sync", defaultSensorsSyncInterval, "sensors sync interval (time.Duration)")
	debug               = flag.Bool("debug", false, "debug mode")
	tuiEnabled          = flag.Bool("tui", false, "run TUI interface alongside server")

	swkService = servicemaker.ServiceMaker{
		User:               "swkit",
		UserGroups:         []string{"gpio"},
		ServicePath:        "/etc/systemd/system/swkit.service",
		ServiceDescription: "SwKit service: HomeKit enabled switch/input/roller shutter controller. github.com/hubertat/swkit",
		ExecDir:            "/srv/swkit",
		ExecName:           "swkit",
	}
)

// appServices holds the lifecycle handles for services tied to a SwKit instance.
type appServices struct {
	serviceCancel func()
	tickerDone    <-chan struct{}
	hkCancel      func()
	hkErrCh       <-chan error
}

// loadSwKit reads the config file, unmarshals it, and calls Setup.
func loadSwKit(configPath string, ctx context.Context, logger *log.Logger) (*swkit.SwKit, error) {
	sk, err := parseSwKit(configPath)
	if err != nil {
		return nil, err
	}
	if err := sk.Setup(ctx, logger); err != nil {
		return nil, err
	}
	return sk, nil
}

// parseSwKit reads and unmarshals the config file without running Setup.
func parseSwKit(configPath string) (*swkit.SwKit, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	sk := &swkit.SwKit{}
	if err := json.Unmarshal(data, sk); err != nil {
		return nil, err
	}
	return sk, nil
}

// startServices starts the sync ticker and HomeKit (if configured) for sk.
func startServices(parentCtx context.Context, sk *swkit.SwKit, syncDuration time.Duration, forceEvery int, version string, logger *log.Logger) *appServices {
	ctx, cancel := context.WithCancel(parentCtx)

	done := make(chan struct{})
	go func() {
		sk.StartTicker(ctx, syncDuration, forceEvery)
		close(done)
	}()

	var hkCancel func()
	var hkErrCh <-chan error
	if len(sk.HkPin) == 8 {
		var err error
		hkCancel, hkErrCh, err = sk.StartHomeKit(ctx, version)
		if err != nil {
			logger.Error("failed to start HomeKit", "err", err)
		} else {
			logger.Info("HomeKit started", "pin", sk.HkPin)
		}
	}

	return &appServices{
		serviceCancel: cancel,
		tickerDone:    done,
		hkCancel:      hkCancel,
		hkErrCh:       hkErrCh,
	}
}

// performReload loads the new config, stops old services, swaps the provider, and starts new services.
// Config is parsed first for early validation; drivers (and MQTT) are only set up after old ones are torn down.
// On config parse failure, the old sk and svcs are returned unchanged so the app keeps running.
func performReload(configPath string, currentSk *swkit.SwKit, provider *swkit.SwKitProvider,
	svcs *appServices, parentCtx context.Context, syncDuration time.Duration, forceEvery int, version string, logger *log.Logger) (*swkit.SwKit, *appServices) {

	// Parse config first for early validation — no drivers/MQTT yet.
	newSk, err := parseSwKit(configPath)
	if err != nil {
		logger.Error("reload failed (config parse error), keeping current config", "err", err)
		return currentSk, svcs
	}

	// Stop old services.
	svcs.serviceCancel()
	if svcs.hkCancel != nil {
		svcs.hkCancel()
	}
	<-svcs.tickerDone

	// Close old drivers (including MQTT disconnect) before new ones connect.
	if closeErr := currentSk.Close(); closeErr != nil {
		logger.Error("error closing old SwKit drivers", "err", closeErr)
	}

	// Now set up new drivers and MQTT connections.
	if err := newSk.Setup(parentCtx, logger); err != nil {
		logger.Error("reload failed (setup error), no active config — restart required", "err", err)
		return currentSk, svcs
	}

	// Swap the provider to point at the new SwKit.
	provider.Reload(newSk)

	// Start services for the new SwKit.
	newSvcs := startServices(parentCtx, newSk, syncDuration, forceEvery, version, logger)
	logger.Info("config reloaded successfully")
	return newSk, newSvcs
}

func main() {
	flag.Parse()
	_ = sensorsSyncInterval // declared for future use
	if *debug {
		log.SetLevel(log.DebugLevel)
	}
	logger := logging.NewLogger(logging.PrefixMain)
	logger.Info("swkit started", "version", Version)

	if *debug {
		logger.Warn("debug mode enabled")
	}

	if *flagInstall {
		err := swkService.InstallService()
		if err != nil {
			panic(err)
		} else {
			logger.Info("service installed, will exit")
			return
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	syncDuration, err := time.ParseDuration(*syncInterval)
	if err != nil {
		panic(err)
	}

	sk, err := loadSwKit(*config, ctx, logger)
	if err != nil {
		logger.Fatal("failed to load config", "err", err)
	}
	logger.Debug("swkit setup done OK")

	sk.PrintIoStatus(os.Stdout)

	// Config provider holds the reload channel (also used by SSH/TUI).
	configProvider := swkit.NewConfigProvider(sk, *config)

	// State provider is swap-safe; updated on each reload.
	provider := swkit.NewStateProvider(sk)

	// SIGHUP feeds into the config provider's reload channel.
	sighupCh := make(chan os.Signal, 1)
	signal.Notify(sighupCh, syscall.SIGHUP)
	go func() {
		for range sighupCh {
			logger.Info("SIGHUP received, triggering config reload")
			configProvider.TriggerReload()
		}
	}()

	// Initialize agent if API key is available.
	var ag *agent.Agent
	agentCfg := agent.DefaultConfig()
	if sk.Agent != nil {
		agentCfg = agentCfg.WithModel(sk.Agent.Model).WithSystemPrompt(sk.Agent.SystemPrompt)
	}
	if agentCfg.Valid() {
		var agentErr error
		ag, agentErr = agent.NewAgent(agentCfg, provider)
		if agentErr != nil {
			logger.Error("failed to create agent", "err", agentErr)
		} else {
			logger.Info("AI agent enabled", "model", agentCfg.Model)
		}
	} else {
		logger.Debug("AI agent disabled (ANTHROPIC_API_KEY not set)")
	}

	// Start SSH TUI server if configured.
	if sk.SshServer != nil && sk.SshServer.Enabled {
		sshSrv, err := server.NewSshTuiServerWithAgent(provider, ag, sk.SshServer.Port, sk.SshServer.HostKeyPath, logger)
		if err != nil {
			logger.Error("failed to create SSH server", "err", err)
		} else {
			go func() {
				if err := sshSrv.Start(ctx); err != nil {
					logger.Error("SSH server error", "err", err)
				}
			}()
		}
	}

	// Start Web UI server if configured.
	if sk.WebServer != nil && sk.WebServer.Enabled {
		getRawConfig := func() json.RawMessage {
			data, err := os.ReadFile(*config)
			if err != nil {
				return nil
			}
			return json.RawMessage(data)
		}
		services := server.ServicesConfig{
			WebPort: sk.WebServer.Port,
		}
		if sk.SshServer != nil && sk.SshServer.Enabled {
			services.SSHEnabled = true
			services.SSHPort = sk.SshServer.Port
		}
		if ag != nil {
			services.AgentEnabled = true
			services.AgentModel = agentCfg.Model
		}
		webSrv, err := server.NewWebServerWithConfig(provider, sk.WebServer.Port, logger, server.WebServerOptions{
			GetRawConfig: getRawConfig,
			Version:      Version,
			Services:     services,
		})
		if err != nil {
			logger.Error("failed to create web server", "err", err)
		} else {
			go func() {
				if err := webSrv.Start(ctx); err != nil {
					logger.Error("web server error", "err", err)
				}
			}()
		}
	}

	// Start ticker and HomeKit.
	logger.Info("starting sync ticker", "interval", syncDuration)
	svcs := startServices(ctx, sk, syncDuration, *forceSyncEveryCycle, Version, logger)

	// Run TUI in a background goroutine if enabled.
	if *tuiEnabled {
		go func() {
			p := tea.NewProgram(tui.NewModelWithOptions(provider, configProvider, ag, nil), tea.WithAltScreen())
			if _, err := p.Run(); err != nil {
				logger.Error("TUI error", "err", err)
			}
			logger.Info("TUI exited, shutting down...")
			cancel()
		}()
	}

	// Graceful shutdown signals.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// Unified event loop: handles reload triggers, signals, HomeKit errors, and context cancellation.
	for {
		select {
		case <-configProvider.ReloadCh():
			sk, svcs = performReload(*config, sk, provider, svcs, ctx, syncDuration, *forceSyncEveryCycle, Version, logger)

		case sig := <-sigCh:
			logger.Info("received signal, shutting down...", "signal", sig)
			signal.Stop(sigCh)
			signal.Stop(sighupCh)
			svcs.serviceCancel()
			if svcs.hkCancel != nil {
				svcs.hkCancel()
			}
			<-svcs.tickerDone
			if err := sk.Close(); err != nil {
				logger.Error("error during shutdown", "err", err)
			}
			cancel()
			logger.Info("swkit shutdown complete")
			return

		case err := <-svcs.hkErrCh:
			if err != nil {
				logger.Error("HomeKit terminated with error", "err", err)
			} else {
				logger.Info("HomeKit terminated ok")
			}
			svcs.serviceCancel()
			<-svcs.tickerDone
			if err := sk.Close(); err != nil {
				logger.Error("error during shutdown", "err", err)
			}
			cancel()
			logger.Info("swkit shutdown complete")
			return

		case <-ctx.Done():
			// Context cancelled (e.g., TUI exited).
			svcs.serviceCancel()
			if svcs.hkCancel != nil {
				svcs.hkCancel()
			}
			<-svcs.tickerDone
			if err := sk.Close(); err != nil {
				logger.Error("error during shutdown", "err", err)
			}
			logger.Info("swkit shutdown complete")
			return
		}
	}
}
