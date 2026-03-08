package server

import (
	"context"
	"errors"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/charmbracelet/wish/activeterm"
	"github.com/charmbracelet/wish/bubbletea"
	"github.com/hubertat/swkit/agent"
	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/logging"
	"github.com/hubertat/swkit/ui/tui"
)

const defaultSSHPort = 2222

// SshTuiServer serves the TUI over SSH using Charm Wish
type SshTuiServer struct {
	provider       app.StateProvider
	configProvider app.ConfigProvider
	agent          *agent.Agent
	broadcaster    *logging.Broadcaster
	server         *ssh.Server
	logger         *log.Logger
}

// NewSshTuiServer creates a new SSH TUI server
func NewSshTuiServer(provider app.StateProvider, port int, hostKeyPath string, logger *log.Logger) (*SshTuiServer, error) {
	return NewSshTuiServerWithAgent(provider, nil, nil, nil, port, hostKeyPath, logger)
}

// NewSshTuiServerWithAgent creates a new SSH TUI server with optional agent and broadcaster support
func NewSshTuiServerWithAgent(provider app.StateProvider, configProvider app.ConfigProvider, ag *agent.Agent, bc *logging.Broadcaster, port int, hostKeyPath string, logger *log.Logger) (*SshTuiServer, error) {
	if port == 0 {
		port = defaultSSHPort
	}

	// Ensure host key exists
	keyPath, err := EnsureHostKey(hostKeyPath)
	if err != nil {
		return nil, errors.Join(err, errors.New("failed to ensure host key"))
	}

	s := &SshTuiServer{
		provider:       provider,
		configProvider: configProvider,
		agent:          ag,
		broadcaster:    bc,
		logger:         logger,
	}

	srv, err := wish.NewServer(
		wish.WithAddress(":"+strconv.Itoa(port)),
		wish.WithHostKeyPath(keyPath),
		wish.WithMiddleware(
			bubbletea.Middleware(s.teaHandler),
			activeterm.Middleware(),
		),
	)
	if err != nil {
		return nil, errors.Join(err, errors.New("failed to create SSH server"))
	}

	s.server = srv
	return s, nil
}

// teaHandler creates a new TUI model for each SSH session
func (s *SshTuiServer) teaHandler(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
	renderer := bubbletea.MakeRenderer(sess)
	model := tui.NewModelWithOptions(s.provider, s.configProvider, s.agent, renderer, s.broadcaster)
	return model, []tea.ProgramOption{tea.WithAltScreen()}
}

// Start starts the SSH server and blocks until context is cancelled
func (s *SshTuiServer) Start(ctx context.Context) error {
	s.logger.Info("starting SSH TUI server", "address", s.server.Addr)

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		s.logger.Info("shutting down SSH TUI server")
		if err := s.server.Close(); err != nil {
			return errors.Join(err, errors.New("failed to close SSH server"))
		}
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, ssh.ErrServerClosed) {
			return errors.Join(err, errors.New("SSH server error"))
		}
		return nil
	}
}

// Addr returns the server address
func (s *SshTuiServer) Addr() string {
	return s.server.Addr
}
