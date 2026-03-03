package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/charmbracelet/log"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hubertat/swkit"
	"github.com/hubertat/swkit/logging"
	"github.com/hubertat/swkit/ui/tui"
)

var (
	Version string
	Build   string

	config = flag.String("config", "config.json", "path of the configuration file")
	debug  = flag.Bool("debug", false, "debug mode")
)

func main() {
	flag.Parse()
	logLevel := log.InfoLevel
	if *debug {
		logLevel = log.DebugLevel
	}
	logging.Init(logging.Config{
		Writer: os.Stderr,
		Level:  logLevel,
	})
	loggerFactory := logging.NewFactory(nil)
	logger := loggerFactory.Logger(logging.PrefixMain)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load configuration
	sk := &swkit.SwKit{}
	configFile, err := os.Open(*config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot open config file %s: %v\n", *config, err)
		os.Exit(1)
	}
	defer configFile.Close()

	cBuff, err := io.ReadAll(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed reading config file: %v\n", err)
		os.Exit(1)
	}

	err = json.Unmarshal(cBuff, sk)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed parsing config JSON: %v\n", err)
		os.Exit(1)
	}

	// Setup SwKit (but don't start HomeKit or ticker)
	err = sk.Setup(ctx, logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: failed to setup swkit: %v\n", err)
		os.Exit(1)
	}
	defer sk.Close()

	// Create state and config providers
	provider := swkit.NewStateProvider(sk)
	configProvider := swkit.NewConfigProvider(sk, *config)

	// Create and run TUI
	model := tui.NewModelWithOptions(provider, configProvider, nil, nil)
	p := tea.NewProgram(model, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running TUI: %v\n", err)
		os.Exit(1)
	}
}
