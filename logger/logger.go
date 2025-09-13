package logger

import (
	"os"

	"github.com/charmbracelet/log"
)

type EventType uint16

const (
	EventTypeSetValue EventType = iota
	EventTypeToggle
	EventTypeUpdateState
)

type EventLogger interface {
	LogEvent(eventType EventType, state uint64, source string, target string)
}

func NewNilLogger() EventLogger {
	return &nilLogger{}
}

type nilLogger struct{}

func (nl *nilLogger) LogEvent(eventType EventType, state uint64, source string, target string) {
	// No-op implementation for NilLogger
}

func NewLogEventLogger() EventLogger {
	logger := log.NewWithOptions(os.Stderr, log.Options{
		Prefix: "🪵",
		Level:  log.GetLevel(),
	})
	return &logEventLogger{
		log: logger,
	}
}

type logEventLogger struct {
	log *log.Logger
}

func (ll *logEventLogger) LogEvent(eventType EventType, state uint64, source string, target string) {
	ll.log.Info("Event", "type", eventType, "state", state, "source", source, "target", target)
}
