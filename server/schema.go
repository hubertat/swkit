package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/hubertat/swkit/app"
	"github.com/hubertat/swkit/drivers"
)

// controllableTargetTypes are the device types that button control relations
// and scene actions may target, matched by name. Buttons themselves are never
// targets. Color lights are included (they can be Controllable at runtime)
// even though they are not part of app.EditableConfig.
var controllableTargetTypes = map[app.DeviceType]bool{
	app.DeviceTypeLight:         true,
	app.DeviceTypeColorLight:    true,
	app.DeviceTypeDimmableLight: true,
	app.DeviceTypeOutlet:        true,
	app.DeviceTypeScene:         true,
}

// handleApiSchema returns the full configuration schema graph as JSON: nodes
// for drivers, IO points and devices, plus edges describing IO wiring, button
// control relations and scene actions. The graph is rebuilt fresh from the
// current state (and editable config, if a ConfigProvider is wired) on every
// request - there is no caching.
func (ws *WebServer) handleApiSchema(w http.ResponseWriter, r *http.Request) {
	state := ws.provider.GetState()

	var cfg *app.EditableConfig
	if ws.opts.ConfigProvider != nil {
		c := ws.opts.ConfigProvider.GetEditableConfig()
		cfg = &c
	}

	resp := buildSchemaGraph(state, cfg)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	json.NewEncoder(w).Encode(resp)
}

// buildSchemaGraph builds the schema graph response from a state snapshot and
// (optionally) the editable config. cfg may be nil: scene action edges are
// then simply omitted, since AppState alone does not carry scene action
// strings (only button ControlRelations, which live on DeviceState directly).
func buildSchemaGraph(state app.AppState, cfg *app.EditableConfig) apiSchemaResponse {
	resp := apiSchemaResponse{
		Name: state.Name,
		HomeKit: apiSchemaHomeKit{
			Enabled:     state.HomeKit.Enabled,
			DeviceCount: state.HomeKit.DeviceCount,
		},
		Nodes: []apiSchemaNode{},
		Edges: []apiSchemaEdge{},
	}

	// ---- Driver nodes ----
	driverPresent := make(map[string]bool, len(state.Drivers))
	driverIoCount := make(map[string]int, len(state.Drivers))
	for _, pt := range state.IoDebug {
		driverIoCount[pt.DriverName]++
	}
	for _, d := range state.Drivers {
		driverPresent[d.Name] = true
		ready := d.Ready
		count := driverIoCount[d.Name]
		resp.Nodes = append(resp.Nodes, apiSchemaNode{
			ID:      "driver:" + d.Name,
			Kind:    "driver",
			Label:   d.Name,
			Ready:   &ready,
			IoTotal: &count,
		})
	}

	// Best-effort custom-name lookup for IO ids, matched against IoDebug
	// entries using the same "<driver>|<type>|<index>" formula state_provider.go
	// uses to annotate ConfiguredAs. Only drivers implementing IoDebugProvider
	// (Wago, Shelly) populate state.IoDebug, so this only resolves for those.
	ioCustomNames := make(map[string]string)
	for _, pt := range state.IoDebug {
		if pt.CustomName == "" {
			continue
		}
		ioTypeStr := "d_in"
		switch pt.Type {
		case "output":
			ioTypeStr = "d_out"
		case "analog_output":
			ioTypeStr = "a_out"
		}
		ioCustomNames[fmt.Sprintf("%s|%s|%d", pt.DriverName, ioTypeStr, pt.Index)] = pt.CustomName
	}

	// ---- Target index: device name -> node id, for control/scene targets ----
	// First match wins across controllable device types (lights, color
	// lights, dimmable lights, outlets, scenes); names may repeat across
	// non-controllable types (e.g. a button sharing a name with a light).
	targetIndex := make(map[string]string)
	for _, d := range state.Devices {
		if !controllableTargetTypes[d.Type] {
			continue
		}
		if _, exists := targetIndex[d.Name]; exists {
			continue
		}
		targetIndex[d.Name] = deviceNodeId(d.Type, d.Name)
	}

	// ---- IO + device nodes, IO edges ----
	ioNodeSeen := make(map[string]bool)
	for _, d := range state.Devices {
		nodeID := deviceNodeId(d.Type, d.Name)

		homekit := d.HomeKitEnabled
		healthy := d.IsHealthy
		faulty := d.IsFaulty
		isOn := d.IsOn
		resp.Nodes = append(resp.Nodes, apiSchemaNode{
			ID:         nodeID,
			Kind:       "device",
			DeviceType: string(d.Type),
			Label:      d.Name,
			HomeKit:    &homekit,
			Healthy:    &healthy,
			Faulty:     &faulty,
			IsOn:       &isOn,
			Detail:     buildDeviceDetail(d),
		})

		for _, ref := range []struct {
			ioId string
			role string
		}{
			{d.OutputIoId, "output"},
			{d.RgbwIoId, "rgbw"},
			{d.AnalogIoId, "analog"},
			{d.EventInputId, "event_input"},
		} {
			if ref.ioId == "" {
				continue
			}
			if !ioNodeSeen[ref.ioId] {
				ioNodeSeen[ref.ioId] = true
				appendIoNode(&resp.Nodes, driverPresent, ioCustomNames, ref.ioId)
			}
			resp.Edges = append(resp.Edges, apiSchemaEdge{
				Kind: "io",
				From: nodeID,
				To:   "io:" + ref.ioId,
				Role: ref.role,
			})
		}
	}

	// ---- Control edges (button ControlRelations) ----
	missingNodes := make(map[string]string)
	for _, d := range state.Devices {
		if d.Type != app.DeviceTypeButton {
			continue
		}
		fromID := deviceNodeId(d.Type, d.Name)
		for _, rel := range d.ControlRelations {
			toID := resolveTarget(rel.DeviceName, targetIndex, missingNodes, &resp.Nodes)
			level := rel.Level
			resp.Edges = append(resp.Edges, apiSchemaEdge{
				Kind:   "control",
				From:   fromID,
				To:     toID,
				Event:  rel.EventType,
				Action: rel.Action,
				Level:  &level,
			})
		}
	}

	// ---- Scene action edges (from EditableConfig, if available) ----
	if cfg != nil {
		seen := make(map[string]bool)
		for _, sc := range cfg.Scenes {
			fromID := deviceNodeId(app.DeviceTypeScene, sc.Name)
			for _, st := range sc.States {
				for _, actionStr := range st.Actions {
					act, err := app.ParseAction(actionStr)
					if err != nil {
						// Unparseable actions are diagnosed elsewhere (e.g.
						// scene setup); silently skip them here.
						continue
					}
					toID := resolveTarget(act.Device, targetIndex, missingNodes, &resp.Nodes)
					key := fmt.Sprintf("%s|%s|%s|%s|%d", fromID, toID, st.Name, act.Verb, act.Level)
					if seen[key] {
						continue
					}
					seen[key] = true
					level := act.Level
					resp.Edges = append(resp.Edges, apiSchemaEdge{
						Kind:   "scene_action",
						From:   fromID,
						To:     toID,
						State:  st.Name,
						Action: act.Verb,
						Level:  &level,
					})
				}
			}
		}
	}

	return resp
}

// deviceNodeId returns the canonical node id for a device: "device:<type>:<name>".
func deviceNodeId(deviceType app.DeviceType, name string) string {
	return fmt.Sprintf("device:%s:%s", deviceType, name)
}

// appendIoNode appends the IO node for ioId to nodes, parsing it with
// drivers.ResolveIoIdString. Unparseable ids still get a node, flagged
// "invalid". If the id's driver has no corresponding driver node yet, a
// placeholder driver node (flagged "missing") is appended too, and
// driverPresent is updated so it is only added once.
func appendIoNode(nodes *[]apiSchemaNode, driverPresent map[string]bool, ioCustomNames map[string]string, ioId string) {
	driverName, ioType, name, err := drivers.ResolveIoIdString(ioId)
	if err != nil {
		*nodes = append(*nodes, apiSchemaNode{
			ID:      "io:" + ioId,
			Kind:    "io",
			Label:   ioId,
			Invalid: true,
		})
		return
	}

	node := apiSchemaNode{
		ID:     "io:" + ioId,
		Kind:   "io",
		Driver: driverName,
		IoType: ioType.IdString(),
		Label:  name,
	}
	if cn, ok := ioCustomNames[ioId]; ok {
		node.CustomName = cn
	}
	*nodes = append(*nodes, node)

	if !driverPresent[driverName] {
		driverPresent[driverName] = true
		*nodes = append(*nodes, apiSchemaNode{
			ID:      "driver:" + driverName,
			Kind:    "driver",
			Label:   driverName,
			Missing: true,
		})
	}
}

// resolveTarget resolves a control/scene action target device name to a node
// id, using targetIndex (real controllable devices). Unknown names get a
// cached "missing" placeholder device node so repeated references share one
// node instead of duplicating it.
func resolveTarget(name string, targetIndex, missingNodes map[string]string, nodes *[]apiSchemaNode) string {
	if id, ok := targetIndex[name]; ok {
		return id
	}
	if id, ok := missingNodes[name]; ok {
		return id
	}

	id := "device:missing:" + name
	missingNodes[name] = id
	*nodes = append(*nodes, apiSchemaNode{
		ID:         id,
		Kind:       "device",
		DeviceType: "missing",
		Label:      name,
		Missing:    true,
	})
	return id
}

// buildDeviceDetail builds the type-specific detail payload for a device
// node. Returns nil (omitted from JSON) when there is nothing to show.
func buildDeviceDetail(d app.DeviceState) map[string]any {
	detail := map[string]any{}

	switch d.Type {
	case app.DeviceTypeLight, app.DeviceTypeOutlet:
		if d.OutputIoId != "" {
			detail["output_io_id"] = d.OutputIoId
		}
	case app.DeviceTypeColorLight:
		if d.OutputIoId != "" {
			detail["output_io_id"] = d.OutputIoId
		}
		if d.RgbwIoId != "" {
			detail["rgbw_io_id"] = d.RgbwIoId
		}
	case app.DeviceTypeDimmableLight:
		if d.OutputIoId != "" {
			detail["output_io_id"] = d.OutputIoId
		}
		if d.AnalogIoId != "" {
			detail["analog_io_id"] = d.AnalogIoId
		}
		detail["brightness"] = d.Brightness
	case app.DeviceTypeButton:
		if d.EventInputId != "" {
			detail["event_input_id"] = d.EventInputId
		}
		if d.LastEventType != "" {
			detail["last_event_type"] = d.LastEventType
		}
		if !d.LastEventTime.IsZero() {
			detail["last_event_time"] = d.LastEventTime
		}
	case app.DeviceTypeScene:
		detail["state_names"] = d.SceneStateNames
		detail["state_index"] = d.SceneStateIndex
	}

	if len(detail) == 0 {
		return nil
	}
	return detail
}

// API response types

type apiSchemaResponse struct {
	Name    string           `json:"name"`
	HomeKit apiSchemaHomeKit `json:"homekit"`
	Nodes   []apiSchemaNode  `json:"nodes"`
	Edges   []apiSchemaEdge  `json:"edges"`
}

type apiSchemaHomeKit struct {
	Enabled     bool `json:"enabled"`
	DeviceCount int  `json:"device_count"`
}

type apiSchemaNode struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"` // "driver" | "io" | "device"
	Label   string `json:"label,omitempty"`
	Missing bool   `json:"missing,omitempty"`

	// driver nodes
	Ready   *bool `json:"ready,omitempty"`
	IoTotal *int  `json:"io_total,omitempty"`

	// io nodes
	Driver     string `json:"driver,omitempty"`
	IoType     string `json:"io_type,omitempty"`
	CustomName string `json:"custom_name,omitempty"`
	Invalid    bool   `json:"invalid,omitempty"`

	// device nodes
	DeviceType string         `json:"device_type,omitempty"`
	HomeKit    *bool          `json:"homekit,omitempty"`
	Healthy    *bool          `json:"healthy,omitempty"`
	Faulty     *bool          `json:"faulty,omitempty"`
	IsOn       *bool          `json:"is_on,omitempty"`
	Detail     map[string]any `json:"detail,omitempty"`
}

type apiSchemaEdge struct {
	Kind   string `json:"kind"` // "io" | "control" | "scene_action"
	From   string `json:"from"`
	To     string `json:"to"`
	Role   string `json:"role,omitempty"`   // io edges
	Event  string `json:"event,omitempty"`  // control edges
	Action string `json:"action,omitempty"` // control + scene_action edges
	Level  *int   `json:"level,omitempty"`  // control + scene_action edges
	State  string `json:"state,omitempty"`  // scene_action edges
}
