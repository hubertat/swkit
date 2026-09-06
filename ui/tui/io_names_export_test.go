package tui

import (
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/hubertat/swkit/app"
)

// fakeIoNamesProvider is a fakeStateProvider that also implements
// app.IoNamesManager, so Model picks it up as m.ioNamesMan (see
// NewModelFromOptions). SaveIoNames records whether/how many times it was
// called instead of touching disk.
type fakeIoNamesProvider struct {
	fakeStateProvider

	mu       sync.Mutex
	names    map[string]string
	saveHits int
}

func newFakeIoNamesProvider() *fakeIoNamesProvider {
	return &fakeIoNamesProvider{names: map[string]string{"driver|input|0": "Front Door"}}
}

func (f *fakeIoNamesProvider) SetIoName(key, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if name == "" {
		delete(f.names, key)
	} else {
		f.names[key] = name
	}
}

func (f *fakeIoNamesProvider) GetIoName(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.names[key]
}

func (f *fakeIoNamesProvider) GetIoNames() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make(map[string]string, len(f.names))
	for k, v := range f.names {
		cp[k] = v
	}
	return cp
}

func (f *fakeIoNamesProvider) LoadIoNames(path string) error { return nil }

func (f *fakeIoNamesProvider) SaveIoNames(path string) error {
	f.mu.Lock()
	f.saveHits++
	f.mu.Unlock()
	return nil
}

func (f *fakeIoNamesProvider) hits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.saveHits
}

// TestExportIoNamesKeyDoesNotBlockUpdate guards finding B: pressing the
// export key on the IO Debug tab must return a tea.Cmd for Update to run
// later, rather than calling SaveIoNames synchronously inside Update itself.
// A real SaveIoNames does GetState() plus a blocking os.WriteFile, either of
// which could otherwise freeze the whole session's event loop.
func TestExportIoNamesKeyDoesNotBlockUpdate(t *testing.T) {
	provider := newFakeIoNamesProvider()
	m := NewModelFromOptions(Options{Provider: provider})
	defer m.Close()

	m.activeTab = TabIoDebug

	if !m.hasIoNames() {
		t.Fatal("test setup: expected hasIoNames() to be true")
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("w")})
	m = updated.(Model)

	if cmd == nil {
		t.Fatal("expected Update to return a command for the export key, got nil")
	}
	if got := provider.hits(); got != 0 {
		t.Fatalf("SaveIoNames must not run synchronously inside Update, but was called %d times before the returned command ran", got)
	}

	// Running the returned command is where the write actually happens.
	msg := cmd()
	if got := provider.hits(); got != 1 {
		t.Fatalf("expected SaveIoNames to run exactly once after executing the export command, got %d", got)
	}

	result, ok := msg.(ioNamesResultMsg)
	if !ok {
		t.Fatalf("expected export command to produce ioNamesResultMsg, got %T", msg)
	}
	if result.message == "" {
		t.Error("expected a non-empty export status message")
	}

	// Feed the result back through Update, mirroring the real event loop,
	// and confirm the status line gets set.
	updated, _ = m.Update(result)
	m = updated.(Model)
	if m.ioExportMsg != result.message {
		t.Errorf("ioExportMsg = %q, want %q", m.ioExportMsg, result.message)
	}
}

// keep app import used even if only for type assertion clarity below.
var _ app.IoNamesManager = (*fakeIoNamesProvider)(nil)
