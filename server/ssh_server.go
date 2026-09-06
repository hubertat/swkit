package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync/atomic"
	"time"

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

const (
	defaultSSHPort = 2222

	// defaultMaxSessions caps concurrent SSH sessions when MaxSessions is
	// not configured.
	defaultMaxSessions = 8

	// defaultIdleTimeoutSeconds is applied when IdleTimeoutSeconds is not
	// configured.
	defaultIdleTimeoutSeconds = 900
)

// SshServerOptions carries the additional, optional configuration knobs for
// SSH TUI server construction, beyond the legacy positional arguments kept
// for backwards compatibility.
type SshServerOptions struct {
	// BindAddress restricts the listener to a single interface. Empty keeps
	// the historical all-interfaces behaviour.
	BindAddress string
	// AuthorizedKeysPath is the authorized_keys file used for public-key
	// auth. Empty means the default ".ssh/authorized_keys". See
	// ResolveAuthorizedKeys for the exact policy.
	AuthorizedKeysPath string
	// MaxSessions caps concurrent SSH sessions. <= 0 means
	// defaultMaxSessions.
	MaxSessions int
	// IdleTimeoutSeconds bounds real user inactivity: it is applied both as
	// wish.WithIdleTimeout (a transport-level backstop against a connection
	// with no traffic at all) and as the TUI model's own input watchdog
	// (tui.Options.IdleTimeout), which is what actually enforces it in
	// practice — the TUI's periodic repaints count as transport traffic and
	// keep the transport-level deadline alive on their own. <= 0 means
	// defaultIdleTimeoutSeconds. See SshServerConfig.IdleTimeoutSeconds.
	IdleTimeoutSeconds int
	// MaxTimeoutSeconds sets a hard cap on session lifetime. <= 0 disables
	// the cap entirely.
	MaxTimeoutSeconds int
}

// SshTuiServer serves the TUI over SSH using Charm Wish
type SshTuiServer struct {
	provider       app.StateProvider
	configProvider app.ConfigProvider
	agent          *agent.Agent
	broadcaster    *logging.Broadcaster
	server         *ssh.Server
	logger         *log.Logger

	maxSessions     int64
	sessionCount    atomic.Int64
	unauthenticated bool

	// idleTimeout is applied to every session's TUI model (tui.Options.IdleTimeout)
	// as an application-level watchdog on real user input; see the
	// SshServerConfig.IdleTimeoutSeconds doc comment for why the
	// transport-level wish.WithIdleTimeout above cannot do this alone.
	idleTimeout time.Duration
}

// NewSshTuiServer creates a new SSH TUI server
func NewSshTuiServer(provider app.StateProvider, port int, hostKeyPath string, logger *log.Logger) (*SshTuiServer, error) {
	return NewSshTuiServerWithAgent(provider, nil, nil, nil, port, hostKeyPath, logger)
}

// NewSshTuiServerWithAgent creates a new SSH TUI server with optional agent and broadcaster support
func NewSshTuiServerWithAgent(provider app.StateProvider, configProvider app.ConfigProvider, ag *agent.Agent, bc *logging.Broadcaster, port int, hostKeyPath string, logger *log.Logger) (*SshTuiServer, error) {
	return NewSshTuiServerWithConfig(provider, configProvider, ag, bc, port, hostKeyPath, SshServerOptions{}, logger)
}

// NewSshTuiServerWithConfig creates a new SSH TUI server with full control
// over bind address, authorized-keys policy, and session limits. See
// SshServerOptions for defaults applied to zero values.
func NewSshTuiServerWithConfig(provider app.StateProvider, configProvider app.ConfigProvider, ag *agent.Agent, bc *logging.Broadcaster, port int, hostKeyPath string, opts SshServerOptions, logger *log.Logger) (*SshTuiServer, error) {
	if port == 0 {
		port = defaultSSHPort
	}

	// Ensure host key exists
	keyPath, err := EnsureHostKey(hostKeyPath)
	if err != nil {
		return nil, errors.Join(err, errors.New("failed to ensure host key"))
	}

	maxSessions := opts.MaxSessions
	if maxSessions <= 0 {
		maxSessions = defaultMaxSessions
	}

	idleTimeoutSeconds := opts.IdleTimeoutSeconds
	if idleTimeoutSeconds <= 0 {
		idleTimeoutSeconds = defaultIdleTimeoutSeconds
	}

	decision, err := ResolveAuthorizedKeys(opts.AuthorizedKeysPath)
	if err != nil {
		return nil, errors.Join(err, errors.New("failed to resolve SSH authorized keys"))
	}

	s := &SshTuiServer{
		provider:       provider,
		configProvider: configProvider,
		agent:          ag,
		broadcaster:    bc,
		logger:         logger,
		maxSessions:    int64(maxSessions),
		idleTimeout:    time.Duration(idleTimeoutSeconds) * time.Second,
	}

	if decision.Enabled {
		logger.Info("SSH public-key authentication enabled", "path", decision.Path)
	} else {
		s.unauthenticated = true
		logger.Warn(
			"SSH server starting WITHOUT authentication, any client can connect",
			"fix", fmt.Sprintf("create %s or set SshServer.AuthorizedKeysPath", decision.Path),
		)
	}

	address := ":" + strconv.Itoa(port)
	if opts.BindAddress != "" {
		address = net.JoinHostPort(opts.BindAddress, strconv.Itoa(port))
	}

	serverOpts := []ssh.Option{
		wish.WithAddress(address),
		wish.WithHostKeyPath(keyPath),
		wish.WithIdleTimeout(time.Duration(idleTimeoutSeconds) * time.Second),
		wish.WithMiddleware(
			bubbletea.Middleware(s.teaHandler),
			s.sessionLimitMiddleware(),
			activeterm.Middleware(),
		),
	}

	if opts.MaxTimeoutSeconds > 0 {
		serverOpts = append(serverOpts, wish.WithMaxTimeout(time.Duration(opts.MaxTimeoutSeconds)*time.Second))
	}

	if decision.Enabled {
		serverOpts = append(serverOpts, wish.WithAuthorizedKeys(decision.Path))
	}

	srv, err := wish.NewServer(serverOpts...)
	if err != nil {
		return nil, errors.Join(err, errors.New("failed to create SSH server"))
	}

	s.server = srv
	return s, nil
}

// sessionLimitMiddleware enforces the configured concurrent session cap and,
// when the server is running unauthenticated, warns loudly about every
// accepted connection. It must run before the bubbletea middleware so it can
// refuse a session without ever starting a TUI program; see wish.WithMiddleware
// ordering notes on NewSshTuiServerWithConfig.
func (s *SshTuiServer) sessionLimitMiddleware() wish.Middleware {
	return func(next ssh.Handler) ssh.Handler {
		return func(sess ssh.Session) {
			if s.unauthenticated {
				s.logger.Warn("accepted unauthenticated SSH connection",
					"remote", sess.RemoteAddr().String(), "user", sess.User())
			}

			if s.sessionCount.Add(1) > s.maxSessions {
				s.sessionCount.Add(-1)
				s.logger.Warn("SSH session limit reached, refusing connection",
					"remote", sess.RemoteAddr().String(), "user", sess.User(), "max_sessions", s.maxSessions)
				wish.Println(sess, fmt.Sprintf("too many concurrent sessions (max %d), please try again later", s.maxSessions))
				_ = sess.Exit(1)
				return
			}
			defer s.sessionCount.Add(-1)

			next(sess)
		}
	}
}

// teaHandler creates a new TUI model for each SSH session
func (s *SshTuiServer) teaHandler(sess ssh.Session) (tea.Model, []tea.ProgramOption) {
	renderer := bubbletea.MakeRenderer(sess)
	model := tui.NewModelFromOptions(tui.Options{
		Provider:       s.provider,
		ConfigProvider: s.configProvider,
		Agent:          s.agent.NewSession(),
		Renderer:       renderer,
		Broadcaster:    s.broadcaster,
		Ctx:            sess.Context(),
		IdleTimeout:    s.idleTimeout,
	})
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
