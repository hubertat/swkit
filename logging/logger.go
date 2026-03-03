package logging

import (
	"io"
	"os"
	"sync"

	"github.com/charmbracelet/log"
)

// Component prefixes with emoji icons for visual distinction
const (
	PrefixMain    = "swkit 🏠"
	PrefixMqtt    = "mqtt 🐰"
	PrefixShelly  = "she🐢y"
	PrefixWago    = "wago 🔌"
	PrefixGpio    = "gpio 📍"
	PrefixMcpio   = "mcpio 🔲"
	PrefixGrenton = "grenton 🏛️"
	PrefixArduino = "arduino 🤖"
	PrefixPushEvt = "push 👆"
	PrefixMock    = "mock 🎭"
	PrefixAgent   = "agent 🤖"
)

var (
	sinkMu      sync.RWMutex
	defaultSink io.Writer = os.Stderr
)

// Config controls root logger behavior for all component loggers.
type Config struct {
	Writer io.Writer
	Level  log.Level
}

// Init sets process-wide logging defaults.
func Init(cfg Config) {
	sinkMu.Lock()
	if cfg.Writer != nil {
		defaultSink = cfg.Writer
	} else {
		defaultSink = os.Stderr
	}
	sinkMu.Unlock()
	log.SetLevel(cfg.Level)
}

// Factory creates component loggers that share a sink and level policy.
type Factory struct {
	writer io.Writer
}

// NewFactory creates a factory bound to an explicit writer.
// If writer is nil, the current package default sink is used.
func NewFactory(writer io.Writer) *Factory {
	if writer == nil {
		sinkMu.RLock()
		writer = defaultSink
		sinkMu.RUnlock()
	}
	return &Factory{writer: writer}
}

// Logger creates a component logger with the provided prefix.
func (f *Factory) Logger(prefix string) *log.Logger {
	return log.NewWithOptions(f.writer, log.Options{
		Prefix: prefix,
		Level:  log.GetLevel(),
	})
}

// NewLogger creates a component logger with the current package sink.
func NewLogger(prefix string) *log.Logger {
	return NewFactory(nil).Logger(prefix)
}

// SetLevel sets the global log level
func SetLevel(level log.Level) {
	log.SetLevel(level)
}
