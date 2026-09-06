package app

import (
	"strconv"
	"strings"
)

// IoPointToId converts an IO point debug state to a config IO id string
// (driver|type|name), inferring the IO type from pt.Type: "output" -> d_out,
// "analog_output" -> a_out, anything else (including "input") -> d_in.
//
// Button event inputs are a special case: they share the same underlying
// debug point as a plain digital input, but the config grammar expects the
// "push_event" type for EventInputName fields. Use IoPointToIdWithType with
// an explicit type string for that case.
func IoPointToId(pt IoPointDebugState) string {
	typeStr := "d_in"
	switch pt.Type {
	case "output":
		typeStr = "d_out"
	case "analog_output":
		typeStr = "a_out"
	}
	return IoPointToIdWithType(pt, typeStr)
}

// IoPointToIdWithType converts an IO point debug state to a config IO id
// string using an explicit IO type string (see drivers.IoType.IdString),
// rather than one inferred from pt.Type.
func IoPointToIdWithType(pt IoPointDebugState, typeStr string) string {
	return pt.DriverName + "|" + typeStr + "|" + ioPointNameForConfig(pt)
}

// ioPointNameForConfig converts a debug display name (pt.Name, e.g.
// "shelly1pm-ABCDEF123456:switch0" or "M1:DI3") into the driver-compatible IO
// name expected as the third segment of a config IO id string. Most drivers
// use their debug name verbatim; Shelly and Wago need translation:
//
//   - Shelly: debug names are "<device>:input<n>", "<device>:switch<n>" or
//     "<device>:light<n>"; config ids use "<device>:<n>" for inputs/switches
//     and "<device>:light:<n>" for lights (see drivers/shelly_driver.go).
//   - Wago: config ids use the driver-global point index rather than the
//     "M<module>:D?<port>" debug name, since that index is stable across
//     module topology changes.
func ioPointNameForConfig(pt IoPointDebugState) string {
	switch pt.DriverName {
	case "shelly":
		parts := strings.SplitN(pt.Name, ":", 2)
		if len(parts) != 2 {
			return pt.Name
		}
		// Lights are addressed explicitly as "<device>:light:<index>".
		if lightPort := strings.TrimPrefix(parts[1], "light"); lightPort != parts[1] {
			if _, err := strconv.Atoi(lightPort); err == nil {
				return parts[0] + ":light:" + lightPort
			}
		}
		portPart := strings.TrimPrefix(parts[1], "input")
		portPart = strings.TrimPrefix(portPart, "switch")
		if _, err := strconv.Atoi(portPart); err == nil {
			return parts[0] + ":" + portPart
		}
		return pt.Name
	case "wago":
		// Wago accepts global index format and pt.Index is stable for this.
		return strconv.Itoa(pt.Index)
	default:
		return pt.Name
	}
}
