package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
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

func main() {
	flag.Parse()
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

	sk := &swkit.SwKit{}
	configFile, err := os.Open(*config)
	if err == nil {
		cBuff, err := io.ReadAll(configFile)
		if err != nil {
			logger.Fatal("failed reading config file", "error", err)
		}

		err = json.Unmarshal(cBuff, sk)
		if err != nil {
			logger.Fatal("failed unmarshalling json config", "error", err)
		}
	} else {
		logger.Fatal("can't find/open config file, will terminate.", "file", *config, "error", err)
	}
	logger.Info("will setup SwKit...")
	err = sk.Setup(ctx, logger)
	defer sk.Close()
	if err != nil {
		logger.Fatal("failed to setup swkit", "err", err)
	}
	logger.Debug("swkit done OK")

	sk.PrintIoStatus(os.Stdout)

	// Start ticker in background
	logger.Info("starting sync ticker", "interval", syncDuration)
	go sk.StartTicker(ctx, syncDuration, *forceSyncEveryCycle)

	// Start HomeKit if configured
	var hkCancel func()
	var hkErrCh <-chan error
	if len(sk.HkPin) == 8 {
		logger.Info("HomeKit configured, starting", "pin", sk.HkPin)
		hkCancel, hkErrCh, err = sk.StartHomeKit(ctx, Version)
		if err != nil {
			logger.Fatal("failed to start HomeKit", "err", err)
		}
	}

	// Start SSH TUI server if configured (before agent init so we can pass agent to SSH server later)
	provider := swkit.NewStateProvider(sk)

	// Initialize agent if API key is available
	var ag *agent.Agent
	agentCfg := agent.DefaultConfig()
	// Apply config from SwKit if present
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

	// Start SSH TUI server if configured
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

	// Run TUI or wait for signal
	if *tuiEnabled {
		configProvider := swkit.NewConfigProvider(sk, *config)
		p := tea.NewProgram(tui.NewModelWithOptions(provider, configProvider, ag, nil), tea.WithAltScreen())
		if _, err := p.Run(); err != nil {
			logger.Error("TUI error", "err", err)
		}
		logger.Info("TUI exited, shutting down...")
		cancel()
		if hkCancel != nil {
			hkCancel()
		}
	} else {
		// Wait for signal or HomeKit termination
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

		if hkErrCh != nil {
			select {
			case sig := <-sigCh:
				logger.Info("received signal, shutting down...", "signal", sig)
				signal.Stop(sigCh)
				cancel()
				if hkCancel != nil {
					hkCancel()
				}
			case err := <-hkErrCh:
				if err != nil {
					logger.Error("HomeKit terminated with error", "err", err)
				} else {
					logger.Info("HomeKit terminated ok")
				}
			}
		} else {
			// No HomeKit, just wait for signal
			sig := <-sigCh
			logger.Info("received signal, shutting down...", "signal", sig)
			signal.Stop(sigCh)
			cancel()
		}
	}

	logger.Info("swkit shutdown complete")
}
