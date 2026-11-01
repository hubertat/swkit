package swkit

import (
	"sync"
	"time"

	"github.com/brutella/hap/accessory"
	"github.com/brutella/hap/characteristic"
	"github.com/charmbracelet/log"
	drivers "github.com/hubertat/swkit/drivers"
)

type WindowShutterConfig struct {
	Name string

	// Name of the digitial output control - open shutter function
	OpenDigitalOutName string
	// Name of the digitial output control - close shutter function
	CloseDigitalOutName string

	// duration in seconds for shutters to open
	OpenDurationSeconds int
	// duration in seconds for shutter to close
	// if empty - OpenDurationSeconds is used
	CloseDurationSeconds int
	DisableHomekit       bool
}

type WindowShutter struct {
	name           string
	disableHomekit bool
	isFaulty       bool

	openDout   drivers.DigitalOutput
	closeDout  drivers.DigitalOutput
	toggleDout drivers.DigitalOutput

	openDuration               time.Duration
	closeDuration              time.Duration
	currentPositionPercent     int
	lastMovementDirectionClose bool

	withCallback bool
	logger       *log.Logger

	hk    *accessory.WindowCovering
	fault *characteristic.StatusFault
	lock  sync.Mutex
}
