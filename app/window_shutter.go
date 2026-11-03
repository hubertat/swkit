package app

import (
	"fmt"
	"strings"

	"github.com/hubertat/swkit/drivers"
)

// Window shutter configuration limits, shared by runtime setup and the
// config editors' save validation.
const (
	// MinShutterDurationSeconds and MaxShutterDurationSeconds bound the
	// measured full travel time in each direction. The fixed 10% endpoint
	// overrun is added on top, so a 600 s endpoint run energizes for up to
	// 660 s.
	MinShutterDurationSeconds = 1
	MaxShutterDurationSeconds = 600
	// MaxShutterReverseDelayMilliseconds bounds ReverseDelayMilliseconds.
	// 0 selects the 500 ms default; a true zero-delay reversal cannot be
	// configured.
	MaxShutterReverseDelayMilliseconds = 5000
	// DefaultShutterReverseDelayMilliseconds is used when the configured
	// reversal delay is 0.
	DefaultShutterReverseDelayMilliseconds = 500
)

// ShutterReverseDelayHelp is the field help shown by editors.
const ShutterReverseDelayHelp = "0 uses 500 ms; otherwise 1-5000 ms"

// WindowShutterEditConfig represents an editable window shutter: a
// maintained two-output roller shutter (one output opens, the other closes).
type WindowShutterEditConfig struct {
	Name                string
	OpenDigitalOutName  string
	CloseDigitalOutName string
	// OpenDurationSeconds is the full opening time (1-600).
	OpenDurationSeconds int
	// CloseDurationSeconds is the full closing time; 0 uses the opening
	// time, otherwise 1-600.
	CloseDurationSeconds int
	// ReverseDelayMilliseconds: 0 uses 500 ms; otherwise 1-5000 ms.
	ReverseDelayMilliseconds int
	DisableHomekit           bool
}

// ValidateWindowShutterFields checks the per-shutter rules that do not depend
// on the rest of the config: both outputs present, distinct, d_out typed,
// and the duration/delay ranges. configuredDriver, when non-nil, reports
// whether a driver name is configured. Messages carry no device prefix.
func ValidateWindowShutterFields(ws WindowShutterEditConfig, configuredDriver func(name string) bool) []string {
	var errs []string
	checkOut := func(field, ioId string) bool {
		if strings.TrimSpace(ioId) == "" {
			errs = append(errs, fmt.Sprintf("%s must not be empty", field))
			return false
		}
		driverName, ioType, _, err := drivers.ResolveIoIdString(ioId)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s %q is not a valid io id: %v", field, ioId, err))
			return false
		}
		if ioType != drivers.IoTypeDigitalOutput {
			errs = append(errs, fmt.Sprintf("%s %q has io type %q, want d_out", field, ioId, ioType.IdString()))
			return false
		}
		if configuredDriver != nil && !configuredDriver(driverName) {
			errs = append(errs, fmt.Sprintf("%s %q references driver %q, which is not configured", field, ioId, driverName))
			return false
		}
		return true
	}
	okOpen := checkOut("OpenDigitalOutName", ws.OpenDigitalOutName)
	okClose := checkOut("CloseDigitalOutName", ws.CloseDigitalOutName)
	if okOpen && okClose && strings.TrimSpace(ws.OpenDigitalOutName) == strings.TrimSpace(ws.CloseDigitalOutName) {
		errs = append(errs, fmt.Sprintf("OpenDigitalOutName and CloseDigitalOutName must be different outputs (both %q)", ws.OpenDigitalOutName))
	}
	if ws.OpenDurationSeconds < MinShutterDurationSeconds || ws.OpenDurationSeconds > MaxShutterDurationSeconds {
		errs = append(errs, fmt.Sprintf("OpenDurationSeconds %d out of range (%d-%d)", ws.OpenDurationSeconds, MinShutterDurationSeconds, MaxShutterDurationSeconds))
	}
	if ws.CloseDurationSeconds != 0 && (ws.CloseDurationSeconds < MinShutterDurationSeconds || ws.CloseDurationSeconds > MaxShutterDurationSeconds) {
		errs = append(errs, fmt.Sprintf("CloseDurationSeconds %d out of range (0 uses the opening time; otherwise %d-%d)", ws.CloseDurationSeconds, MinShutterDurationSeconds, MaxShutterDurationSeconds))
	}
	if ws.ReverseDelayMilliseconds < 0 || ws.ReverseDelayMilliseconds > MaxShutterReverseDelayMilliseconds {
		errs = append(errs, fmt.Sprintf("ReverseDelayMilliseconds %d out of range (%s)", ws.ReverseDelayMilliseconds, ShutterReverseDelayHelp))
	}
	return errs
}
