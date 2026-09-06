package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
)

// TestApiStateLiveOverlayFields covers the fields added to /api/state for the
// schema tab's live overlay (round 2): apiDevice.Brightness/SceneStateIndex/
// SceneStateNames, and apiIoPoint.IoIds (the canonical driver|type|name id(s)
// the schema builder keys io nodes by - see app.IoPointToId/IoPointToIdWithType
// and schema.go's ioCustomNames). Uses fakeStateProvider from schema_test.go.
//
// Besides decoding into the typed apiStateResponse (which would silently
// tolerate a struct tag rename), it also decodes the raw body into
// map[string]any and asserts on the literal JSON key names the schema.js
// client actually reads off the wire (brightness, scene_state_index,
// scene_state_names, io_ids) - see schema.js's liveSignature/
// updateDeviceOverlay/buildIoStateIndex - plus that "brightness" is omitted
// entirely for a device that never sets it (omitempty), so the client's
// `d.brightness || 0` fallback path is exercised rather than assumed.
func TestApiStateLiveOverlayFields(t *testing.T) {
	fake := &fakeStateProvider{state: app.AppState{
		Name: "Test Home",
		IoDebug: []app.IoPointDebugState{
			{DriverName: "gpio", Index: 0, Name: "5", Type: "output"},
			{DriverName: "shelly", Index: 1, Name: "shellyplus1-ABC:input0", Type: "input"},
			{DriverName: "wago", Index: 2, Type: "analog_output"},
		},
		Devices: []app.DeviceState{
			{
				Name: "Hall", Type: app.DeviceTypeDimmableLight,
				OutputIoId: "gpio|d_out|5", Brightness: 42,
			},
			{
				Name: "Evening", Type: app.DeviceTypeScene,
				SceneStateIndex: 1, SceneStateNames: []string{"off", "cozy"},
			},
		},
	}}

	ws, err := NewWebServerWithConfig(fake, 0, log.New(nil), WebServerOptions{})
	if err != nil {
		t.Fatalf("NewWebServerWithConfig: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	ws.handleApiState(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var resp apiStateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, w.Body.String())
	}

	var hall, evening *apiDevice
	for i := range resp.Devices {
		switch resp.Devices[i].Name {
		case "Hall":
			hall = &resp.Devices[i]
		case "Evening":
			evening = &resp.Devices[i]
		}
	}
	if hall == nil {
		t.Fatal("Hall device not found in response")
	}
	if hall.Brightness != 42 {
		t.Errorf("Hall brightness = %d, want 42", hall.Brightness)
	}
	if evening == nil {
		t.Fatal("Evening device not found in response")
	}
	if evening.SceneStateIndex != 1 {
		t.Errorf("Evening scene_state_index = %d, want 1", evening.SceneStateIndex)
	}
	if len(evening.SceneStateNames) != 2 || evening.SceneStateNames[1] != "cozy" {
		t.Errorf("Evening scene_state_names = %v, want [off cozy]", evening.SceneStateNames)
	}

	var gpioPt, shellyPt, wagoPt *apiIoPoint
	for i := range resp.IoDebug {
		switch resp.IoDebug[i].DriverName {
		case "gpio":
			gpioPt = &resp.IoDebug[i]
		case "shelly":
			shellyPt = &resp.IoDebug[i]
		case "wago":
			wagoPt = &resp.IoDebug[i]
		}
	}
	if gpioPt == nil {
		t.Fatal("gpio io_debug point not found")
	}
	if len(gpioPt.IoIds) != 1 || gpioPt.IoIds[0] != "gpio|d_out|5" {
		t.Errorf("gpio io_ids = %v, want [gpio|d_out|5]", gpioPt.IoIds)
	}
	if shellyPt == nil {
		t.Fatal("shelly io_debug point not found")
	}
	if len(shellyPt.IoIds) != 2 {
		t.Fatalf("shelly io_ids = %v, want 2 entries (d_in + push_event)", shellyPt.IoIds)
	}
	wantIds := map[string]bool{"shelly|d_in|shellyplus1-ABC:0": true, "shelly|push_event|shellyplus1-ABC:0": true}
	for _, id := range shellyPt.IoIds {
		if !wantIds[id] {
			t.Errorf("unexpected shelly io id %q, want one of %v", id, wantIds)
		}
	}
	// analog_output -> a_out (app.IoPointToId), exercised end-to-end through
	// the /api/state handler rather than just at the app.IoPointToId unit
	// level (see app/io_id_test.go).
	if wagoPt == nil {
		t.Fatal("wago io_debug point not found")
	}
	if len(wagoPt.IoIds) != 1 || wagoPt.IoIds[0] != "wago|a_out|2" {
		t.Errorf("wago io_ids = %v, want [wago|a_out|2]", wagoPt.IoIds)
	}

	// ---- Raw JSON key-name assertions ----
	// Decode independently of apiStateResponse's Go struct tags so a tag typo
	// (e.g. renaming the json tag without updating schema.js) would actually
	// fail this test instead of round-tripping silently through the same
	// struct on both ends.
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw response: %v (body: %s)", err, w.Body.String())
	}

	devicesRaw, _ := raw["devices"].([]any)
	var hallRaw, eveningRaw map[string]any
	for _, dr := range devicesRaw {
		m, ok := dr.(map[string]any)
		if !ok {
			continue
		}
		switch m["name"] {
		case "Hall":
			hallRaw = m
		case "Evening":
			eveningRaw = m
		}
	}
	if hallRaw == nil {
		t.Fatal("raw JSON: Hall device not found")
	}
	if v, ok := hallRaw["brightness"]; !ok || v != float64(42) {
		t.Errorf(`raw JSON: Hall["brightness"] = %v (present=%v), want 42`, v, ok)
	}
	if eveningRaw == nil {
		t.Fatal("raw JSON: Evening device not found")
	}
	if v, ok := eveningRaw["scene_state_index"]; !ok || v != float64(1) {
		t.Errorf(`raw JSON: Evening["scene_state_index"] = %v (present=%v), want 1`, v, ok)
	}
	if v, ok := eveningRaw["scene_state_names"]; !ok {
		t.Errorf("raw JSON: Evening missing scene_state_names key, got %v", v)
	}
	// Evening never sets Brightness (zero value) - omitempty must drop the
	// key entirely rather than serializing "brightness":0, since schema.js
	// distinguishes "no live brightness for this device type" from
	// "brightness is 0" only by key absence in some read paths.
	if v, ok := eveningRaw["brightness"]; ok {
		t.Errorf(`raw JSON: Evening["brightness"] present = %v, want key absent (Brightness is 0)`, v)
	}

	ioDebugRaw, _ := raw["io_debug"].([]any)
	var wagoRaw map[string]any
	for _, pr := range ioDebugRaw {
		m, ok := pr.(map[string]any)
		if !ok {
			continue
		}
		if m["driver_name"] == "wago" {
			wagoRaw = m
		}
	}
	if wagoRaw == nil {
		t.Fatal("raw JSON: wago io_debug point not found")
	}
	ioIdsRaw, ok := wagoRaw["io_ids"].([]any)
	if !ok || len(ioIdsRaw) != 1 || ioIdsRaw[0] != "wago|a_out|2" {
		t.Errorf("raw JSON: wago io_ids = %v, want [wago|a_out|2]", wagoRaw["io_ids"])
	}
}

// TestApiSummaryScenesCount covers finding 4: app.StateSummary.ScenesCount
// (computed by AppState.Summary from DeviceTypeScene devices) must reach the
// client on /api/state's summary object as scenes_count, alongside the
// dimmable_lights_count field that was already wired through. Both were
// previously dropped when building apiSummary in handleApiState, so the
// dashboard under-reported (or entirely omitted) these device types.
func TestApiSummaryScenesCount(t *testing.T) {
	fake := &fakeStateProvider{state: app.AppState{
		Name: "Test Home",
		Devices: []app.DeviceState{
			{Name: "Living Room", Type: app.DeviceTypeLight},
			{Name: "Hall", Type: app.DeviceTypeDimmableLight, Brightness: 10},
			{Name: "Evening", Type: app.DeviceTypeScene, SceneStateNames: []string{"off", "cozy"}},
			{Name: "Morning", Type: app.DeviceTypeScene, SceneStateNames: []string{"off", "bright"}},
		},
	}}

	ws, err := NewWebServerWithConfig(fake, 0, log.New(nil), WebServerOptions{})
	if err != nil {
		t.Fatalf("NewWebServerWithConfig: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/state", nil)
	w := httptest.NewRecorder()
	ws.handleApiState(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}

	var resp apiStateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v (body: %s)", err, w.Body.String())
	}
	if resp.Summary.ScenesCount != 2 {
		t.Errorf("Summary.ScenesCount = %d, want 2", resp.Summary.ScenesCount)
	}
	if resp.Summary.DimmableLightsCount != 1 {
		t.Errorf("Summary.DimmableLightsCount = %d, want 1", resp.Summary.DimmableLightsCount)
	}
	if resp.Summary.LightsCount != 1 {
		t.Errorf("Summary.LightsCount = %d, want 1", resp.Summary.LightsCount)
	}

	// Raw JSON key-name assertion, same rationale as TestApiStateLiveOverlayFields:
	// guards the literal wire key scenes_count that app.js's renderDashboard
	// reads, independent of Go struct tags round-tripping through the same code.
	var raw map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal raw response: %v (body: %s)", err, w.Body.String())
	}
	summaryRaw, ok := raw["summary"].(map[string]any)
	if !ok {
		t.Fatal("raw JSON: summary object not found")
	}
	if v, ok := summaryRaw["scenes_count"]; !ok || v != float64(2) {
		t.Errorf(`raw JSON: summary["scenes_count"] = %v (present=%v), want 2`, v, ok)
	}
	if v, ok := summaryRaw["dimmable_lights_count"]; !ok || v != float64(1) {
		t.Errorf(`raw JSON: summary["dimmable_lights_count"] = %v (present=%v), want 1`, v, ok)
	}
}
