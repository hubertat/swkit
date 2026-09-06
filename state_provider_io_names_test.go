package swkit

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/drivers"
)

// newIoNamesTestProvider builds a minimal SwKitProvider (no hardware) for
// exercising LoadIoNames in isolation.
func newIoNamesTestProvider(t *testing.T) *SwKitProvider {
	t.Helper()
	sw := &SwKit{Name: "t", ioDrivers: map[string]drivers.IoDriver{}}
	return NewStateProvider(sw)
}

// TestLoadIoNamesRejectsFifo guards against the FIFO case from finding 3: a
// blocking special file must be rejected up front rather than hanging the
// read forever.
func TestLoadIoNamesRejectsFifo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("FIFOs are not supported on windows")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Skipf("Mkfifo unsupported on this platform: %v", err)
	}

	p := newIoNamesTestProvider(t)
	err := p.LoadIoNames(path)
	if err == nil {
		t.Fatal("expected an error loading a FIFO, got nil")
	}
	if !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("error = %q, want it to mention the file is not regular", err.Error())
	}
}

// TestLoadIoNamesRejectsOversizedFile guards against the /dev/zero /
// huge-file case from finding 3: a file over maxIoNamesFileSize must be
// rejected rather than read into memory in full.
func TestLoadIoNamesRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.json")

	// One byte over the cap is enough to exercise the boundary.
	data := make([]byte, maxIoNamesFileSize+1)
	for i := range data {
		data[i] = ' '
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := newIoNamesTestProvider(t)
	err := p.LoadIoNames(path)
	if err == nil {
		t.Fatal("expected an error loading an oversized file, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("error = %q, want it to mention the size limit", err.Error())
	}
}

// TestLoadIoNamesAcceptsValidSmallFile is the control case: a well-formed,
// small names file must still load successfully.
func TestLoadIoNamesAcceptsValidSmallFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "names.json")

	const body = `[{"driver":"gpio","type":"d_out","index":1,"name":"Kitchen Light"}]`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	p := newIoNamesTestProvider(t)
	if err := p.LoadIoNames(path); err != nil {
		t.Fatalf("LoadIoNames: %v", err)
	}

	key := app.IoDebugKey("gpio", "d_out", 1)
	if got := p.GetIoName(key); got != "Kitchen Light" {
		t.Errorf("GetIoName(%q) = %q, want %q", key, got, "Kitchen Light")
	}
}
