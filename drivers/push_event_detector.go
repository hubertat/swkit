package drivers

import (
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/logging"
)

const (
	defaultPollInterval      = 10 * time.Millisecond
	defaultDoubleClickWindow = 300 * time.Millisecond
	defaultLongPressDuration = 1000 * time.Millisecond
)

type detectorState int

const (
	stateIdle detectorState = iota
	statePressed
	stateWaitingForClick
)

// PushEventDetector detects button press patterns (single, double, triple, long press)
// by polling a DigitalInput and running a state machine.
type PushEventDetector struct {
	name   string
	input  DigitalInput
	logger *log.Logger

	// Configuration
	pollInterval      time.Duration
	doubleClickWindow time.Duration
	longPressDuration time.Duration

	// Subscription management
	subscribers map[PushEvent][]func(PushEvent)
	subMutex    sync.RWMutex

	// State machine
	state            detectorState
	pressStartTime   time.Time
	lastReleaseTime  time.Time
	clickCount       int
	longPressEmitted bool

	// Lifecycle
	ticker  *time.Ticker
	quit    chan bool
	healthy bool
	stopped bool
	started bool
	mu      sync.Mutex
}

// PushEventDetectorConfig holds configuration for creating a detector.
type PushEventDetectorConfig struct {
	PollInterval      time.Duration
	DoubleClickWindow time.Duration
	LongPressDuration time.Duration
}

// NewPushEventDetector creates a new push event detector for the given input.
// If config is nil, default timing values are used.
// If logger is nil, a default logger with PrefixPushEvt is created.
func NewPushEventDetector(input DigitalInput, name string, config *PushEventDetectorConfig, logger *log.Logger) *PushEventDetector {
	if logger == nil {
		logger = logging.NewLogger(logging.PrefixPushEvt)
	}

	ped := &PushEventDetector{
		name:              name,
		input:             input,
		logger:            logger,
		pollInterval:      defaultPollInterval,
		doubleClickWindow: defaultDoubleClickWindow,
		longPressDuration: defaultLongPressDuration,
		subscribers:       make(map[PushEvent][]func(PushEvent)),
		quit:              make(chan bool),
		state:             stateIdle,
		healthy:           true,
	}

	if config != nil {
		if config.PollInterval > 0 {
			ped.pollInterval = config.PollInterval
		}
		if config.DoubleClickWindow > 0 {
			ped.doubleClickWindow = config.DoubleClickWindow
		}
		if config.LongPressDuration > 0 {
			ped.longPressDuration = config.LongPressDuration
		}
	}

	return ped
}

// Start begins the polling goroutine that detects events.
func (ped *PushEventDetector) Start() {
	ped.mu.Lock()
	defer ped.mu.Unlock()

	if ped.stopped || ped.started {
		return
	}

	ped.started = true
	ped.ticker = time.NewTicker(ped.pollInterval)
	go ped.pollLoop()
}

// Stop stops the polling goroutine and cleans up resources.
func (ped *PushEventDetector) Stop() {
	ped.mu.Lock()
	defer ped.mu.Unlock()

	if ped.stopped {
		return
	}

	ped.stopped = true
	if ped.ticker != nil {
		ped.ticker.Stop()
		ped.quit <- true
		close(ped.quit)
	}
}

// Subscribe registers a handler for specific event types.
// Implements PushEventEmitter interface.
func (ped *PushEventDetector) Subscribe(eventTypes PushEvent, handler func(PushEvent)) error {
	if handler == nil {
		return fmt.Errorf("handler cannot be nil")
	}

	ped.subMutex.Lock()
	defer ped.subMutex.Unlock()

	ped.logger.Debug("subscribing", "name", ped.name)

	// Subscribe to each event type in the bitmask
	for _, evt := range AllPushEvents() {
		if eventTypes&evt == evt {
			ped.logger.Debug("subscribing to event", "event", evt.String())
			ped.subscribers[evt] = append(ped.subscribers[evt], handler)
		}
	}

	return nil
}

// String returns the identifier for this detector.
// Implements PushEventEmitter interface.
func (ped *PushEventDetector) String() string {
	return ped.name
}

// IsHealthy returns the health status of the detector.
// Implements PushEventEmitter interface.
func (ped *PushEventDetector) IsHealthy() bool {
	ped.mu.Lock()
	defer ped.mu.Unlock()
	return ped.healthy && ped.input.IsHealthy()
}

// pollLoop is the main goroutine that polls the input and runs the state machine.
func (ped *PushEventDetector) pollLoop() {
	for {
		select {
		case <-ped.quit:
			return
		case <-ped.ticker.C:
			ped.detectEvents()
		}
	}
}

// detectEvents reads the current input state and runs the state machine.
func (ped *PushEventDetector) detectEvents() {
	pressed, err := ped.input.GetState()
	if err != nil {
		ped.mu.Lock()
		ped.healthy = false
		ped.mu.Unlock()
		return
	}

	ped.mu.Lock()
	ped.healthy = true
	ped.mu.Unlock()

	now := time.Now()

	switch ped.state {
	case stateIdle:
		if pressed {
			// Button pressed, start tracking
			ped.state = statePressed
			ped.pressStartTime = now
			ped.clickCount = 0
			ped.longPressEmitted = false
		}

	case statePressed:
		if !pressed {
			// Button released
			if ped.longPressEmitted {
				// Was a long press that we already emitted, just go back to idle
				ped.state = stateIdle
				ped.clickCount = 0
				ped.longPressEmitted = false
			} else {
				pressDuration := now.Sub(ped.pressStartTime)
				if pressDuration >= ped.longPressDuration {
					// Released after long press duration (edge case: detected on release)
					ped.emit(PushEventLongPress)
					ped.state = stateIdle
					ped.clickCount = 0
				} else {
					// Was a click, increment count and wait for possible next click
					ped.clickCount++
					ped.lastReleaseTime = now
					ped.state = stateWaitingForClick
				}
			}
		} else {
			// Still pressed, check if it's been long enough for long press
			if !ped.longPressEmitted {
				pressDuration := now.Sub(ped.pressStartTime)
				if pressDuration >= ped.longPressDuration {
					// Long press detected, emit once and stay in pressed state
					ped.emit(PushEventLongPress)
					ped.longPressEmitted = true
					// Stay in statePressed until button is released
				}
			}
		}

	case stateWaitingForClick:
		if pressed {
			// Another press started, continue counting clicks
			ped.state = statePressed
			ped.pressStartTime = now
		} else {
			// Still waiting, check if window expired
			timeSinceRelease := now.Sub(ped.lastReleaseTime)
			if timeSinceRelease >= ped.doubleClickWindow {
				// Window expired, emit event based on click count
				ped.emitClickEvent()
				ped.state = stateIdle
				ped.clickCount = 0
			}
		}
	}
}

// emitClickEvent emits the appropriate event based on click count.
func (ped *PushEventDetector) emitClickEvent() {
	switch ped.clickCount {
	case 1:
		ped.emit(PushEventSinglePress)
	case 2:
		ped.emit(PushEventDoublePress)
	case 3:
		ped.emit(PushEventTriplePress)
	default:
		// Cap at triple press for 4+ clicks
		if ped.clickCount > 3 {
			ped.emit(PushEventTriplePress)
		}
	}
}

// emit sends an event to all subscribed handlers.
func (ped *PushEventDetector) emit(event PushEvent) {
	ped.logger.Debug("emitting event", "event", event.String(), "name", ped.name)

	ped.subMutex.RLock()
	handlers, exists := ped.subscribers[event]
	ped.subMutex.RUnlock()

	ped.logger.Debug("handlers status", "exists", exists, "count", len(handlers))
	if !exists || len(handlers) == 0 {
		return
	}

	// Call all handlers for this event type
	for _, handler := range handlers {
		handler(event)
	}
}
