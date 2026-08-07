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
func TestApiStateLiveOverlayFields(t *testing.T) {
	fake := &fakeStateProvider{state: app.AppState{
		Name: "Test Home",
		IoDebug: []app.IoPointDebugState{
			{DriverName: "gpio", Index: 0, Name: "5", Type: "output"},
			{DriverName: "shelly", Index: 1, Name: "shellyplus1-ABC:input0", Type: "input"},
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

	var gpioPt, shellyPt *apiIoPoint
	for i := range resp.IoDebug {
		switch resp.IoDebug[i].DriverName {
		case "gpio":
			gpioPt = &resp.IoDebug[i]
		case "shelly":
			shellyPt = &resp.IoDebug[i]
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
}
