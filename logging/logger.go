package logging

import (
	"os"

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
)

// NewLogger creates a child logger with the given prefix
func NewLogger(prefix string) *log.Logger {
	return log.NewWithOptions(os.Stderr, log.Options{
		Prefix: prefix,
		Level:  log.GetLevel(),
	})
}

// SetLevel sets the global log level
func SetLevel(level log.Level) {
	log.SetLevel(level)
}
