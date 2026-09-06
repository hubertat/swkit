package server

import (
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish/testsession"
	gossh "golang.org/x/crypto/ssh"
)

// newTestSshTuiServer builds a bare SshTuiServer with just the fields the
// session limiter middleware needs, avoiding the full wish.NewServer /
// host-key / TUI wiring.
func newTestSshTuiServer(maxSessions int) *SshTuiServer {
	s := &SshTuiServer{
		logger: log.New(io.Discard),
	}
	s.maxSessions = int64(maxSessions)
	return s
}

// TestSessionLimitMiddleware_RefusesOverCapAndReleases verifies that the
// concurrent-session limiter refuses connections once the cap is reached,
// and frees slots as sessions end so later connections succeed again.
func TestSessionLimitMiddleware_RefusesOverCapAndReleases(t *testing.T) {
	const maxSess = 2
	s := newTestSshTuiServer(maxSess)

	release := make(chan struct{})
	started := make(chan struct{}, maxSess)

	next := func(sess ssh.Session) {
		started <- struct{}{}
		<-release
	}

	handler := s.sessionLimitMiddleware()(next)
	addr := testsession.Listen(t, &ssh.Server{Handler: handler})

	// Open `cap` sessions that occupy every slot and block in next().
	var wg sync.WaitGroup
	for i := 0; i < maxSess; i++ {
		sess, err := testsession.NewClientSession(t, addr, nil)
		if err != nil {
			t.Fatalf("failed to open session %d: %v", i, err)
		}
		wg.Add(1)
		go func(sess *gossh.Session) {
			defer wg.Done()
			_ = sess.Run("noop")
		}(sess)
	}
	for i := 0; i < maxSess; i++ {
		<-started
	}

	// One more session should be refused with a human-readable message.
	over, err := testsession.NewClientSession(t, addr, nil)
	if err != nil {
		t.Fatalf("failed to dial over-cap session: %v", err)
	}
	out, runErr := over.CombinedOutput("noop")
	if runErr == nil {
		t.Error("expected over-cap session to exit non-zero")
	}
	if !strings.Contains(string(out), "too many concurrent sessions") {
		t.Errorf("expected refusal message in output, got %q", string(out))
	}
	if got := s.sessionCount.Load(); got != maxSess {
		t.Errorf("sessionCount = %d, want %d after refusal", got, maxSess)
	}

	// Release the two blocked sessions and wait for their slots to free up.
	close(release)
	wg.Wait()

	deadline := time.Now().Add(2 * time.Second)
	for s.sessionCount.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := s.sessionCount.Load(); got != 0 {
		t.Fatalf("sessionCount = %d, want 0 after releasing sessions", got)
	}

	// A fresh session should now be accepted again.
	fresh, err := testsession.NewClientSession(t, addr, nil)
	if err != nil {
		t.Fatalf("failed to dial fresh session: %v", err)
	}
	if err := fresh.Run("noop"); err != nil {
		t.Errorf("expected fresh session to succeed after slots freed, got: %v", err)
	}
}

// TestSessionLimitMiddleware_WarnsWhenUnauthenticated is a light smoke test
// ensuring the unauthenticated flag doesn't interfere with normal session
// handling (the actual log line is best-effort logging, not behavior we
// assert on here).
func TestSessionLimitMiddleware_WarnsWhenUnauthenticated(t *testing.T) {
	s := newTestSshTuiServer(1)
	s.unauthenticated = true

	called := make(chan struct{}, 1)
	next := func(sess ssh.Session) {
		called <- struct{}{}
	}

	handler := s.sessionLimitMiddleware()(next)
	addr := testsession.Listen(t, &ssh.Server{Handler: handler})

	sess, err := testsession.NewClientSession(t, addr, nil)
	if err != nil {
		t.Fatalf("failed to dial session: %v", err)
	}
	if err := sess.Run("noop"); err != nil {
		t.Fatalf("unexpected error running session: %v", err)
	}

	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("next handler was not called")
	}
}
