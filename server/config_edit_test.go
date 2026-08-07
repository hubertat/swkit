package server

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
)

// fakeSavingConfigProvider records the config passed to SaveConfig, and can
// be told to fail the save.
type fakeSavingConfigProvider struct {
	cfg       app.EditableConfig
	saved     *app.EditableConfig
	saveErr   error
	saveCalls int
}

func (f *fakeSavingConfigProvider) GetEditableConfig() app.EditableConfig { return f.cfg }
func (f *fakeSavingConfigProvider) SaveConfig(config app.EditableConfig) error {
	f.saveCalls++
	if f.saveErr != nil {
		return f.saveErr
	}
	cp := config
	f.saved = &cp
	return nil
}

func newConfigEditTestServer(t *testing.T, state app.AppState, provider app.ConfigProvider) *WebServer {
	t.Helper()
	ws, err := NewWebServerWithConfig(&fakeStateProvider{state: state}, 0, log.New(nil), WebServerOptions{ConfigProvider: provider})
	if err != nil {
		t.Fatalf("NewWebServerWithConfig: %v", err)
	}
	return ws
}

func baseFixtureState() app.AppState {
	return app.AppState{
		Name: "My Home",
		Drivers: []app.DriverState{
			{Name: "gpio", Ready: true},
			{Name: "shelly", Ready: true},
		},
		IoDebug: []app.IoPointDebugState{
			{DriverName: "gpio", Index: 5, Name: "5", Type: "output", ConfiguredAs: "Living Room"},
			{DriverName: "gpio", Index: 1, Name: "1", Type: "analog_output"},
			{DriverName: "shelly", Index: 0, Name: "dev123:input0", Type: "input", CustomName: "wall switch"},
		},
		Devices: []app.DeviceState{
			{Name: "Mood", Type: app.DeviceTypeColorLight, OutputIoId: "gpio|d_out|8", RgbwIoId: "gpio|rgbw_out|1"},
		},
	}
}

func baseFixtureConfig() app.EditableConfig {
	return app.EditableConfig{
		Lights: []app.LightEditConfig{
			{Name: "Living Room", DigitalOutName: "gpio|d_out|5"},
		},
		OutputDeviceNames: []string{"Living Room", "Mood"},
	}
}

// ---- GET ----

func TestConfigEdit_GetNilProvider(t *testing.T) {
	ws := newConfigEditTestServer(t, baseFixtureState(), nil)
	req := httptest.NewRequest("GET", "/api/config/edit", nil)
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, req)

	if w.Code != 503 {
		t.Fatalf("status = %d, want 503 (body: %s)", w.Code, w.Body.String())
	}
	var resp apiConfigEditSaveResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Ok || len(resp.Errors) == 0 {
		t.Errorf("resp = %+v, want ok=false with an error message", resp)
	}
}

func TestConfigEdit_GetShape(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	req := httptest.NewRequest("GET", "/api/config/edit", nil)
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (body: %s)", w.Code, w.Body.String())
	}
	var resp apiConfigEditResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}

	if len(resp.Config.Lights) != 1 || resp.Config.Lights[0].Name != "Living Room" {
		t.Errorf("config.Lights = %+v, want one light named Living Room", resp.Config.Lights)
	}

	wantEvents := app.AllEventTypes()
	if len(resp.Meta.EventTypes) != len(wantEvents) {
		t.Errorf("meta.event_types = %v, want %v", resp.Meta.EventTypes, wantEvents)
	}
	wantVerbs := app.AllActionVerbs()
	if len(resp.Meta.ActionVerbs) != len(wantVerbs) {
		t.Errorf("meta.action_verbs = %v, want %v", resp.Meta.ActionVerbs, wantVerbs)
	}
	if len(resp.Meta.OutputDeviceNames) != 2 {
		t.Errorf("meta.output_device_names = %v, want 2 entries (incl. color light)", resp.Meta.OutputDeviceNames)
	}
	if len(resp.Meta.Drivers) != 2 {
		t.Errorf("meta.drivers = %v, want [gpio shelly]", resp.Meta.Drivers)
	}

	dOut, ok := resp.Meta.IoSuggestions["d_out"]
	if !ok || len(dOut) != 1 || dOut[0].Id != "gpio|d_out|5" || dOut[0].ConfiguredAs != "Living Room" {
		t.Errorf("meta.io_suggestions[d_out] = %+v, want one gpio|d_out|5 configured_as=Living Room", dOut)
	}
	aOut, ok := resp.Meta.IoSuggestions["a_out"]
	if !ok || len(aOut) != 1 || aOut[0].Id != "gpio|a_out|1" {
		t.Errorf("meta.io_suggestions[a_out] = %+v, want one gpio|a_out|1", aOut)
	}
	pushEvt, ok := resp.Meta.IoSuggestions["push_event"]
	if !ok || len(pushEvt) != 1 || pushEvt[0].Id != "shelly|push_event|dev123:0" {
		t.Errorf("meta.io_suggestions[push_event] = %+v, want one shelly|push_event|dev123:0", pushEvt)
	}
	if pushEvt[0].Label != "wall switch [dev123:input0]" {
		t.Errorf("push_event label = %q, want custom-name-bracketed form", pushEvt[0].Label)
	}
}

func TestConfigEdit_MethodNotAllowed(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	req := httptest.NewRequest("DELETE", "/api/config/edit", nil)
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, req)
	if w.Code != 405 {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

// ---- POST: success ----

func doConfigEditPost(t *testing.T, ws *WebServer, body string) (int, apiConfigEditSaveResponse) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/config/edit", strings.NewReader(body))
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, req)
	var resp apiConfigEditSaveResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (body: %s)", err, w.Body.String())
	}
	return w.Code, resp
}

func TestConfigEdit_PostSuccessRoundTrips(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	newCfg := app.EditableConfig{
		Lights: []app.LightEditConfig{
			{Name: "Living Room Renamed", DigitalOutName: "gpio|d_out|5"},
		},
	}
	body, err := json.Marshal(apiConfigEditPostBody{Config: newCfg})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	code, resp := doConfigEditPost(t, ws, string(body))
	if code != 200 {
		t.Fatalf("status = %d, want 200, errors=%v", code, resp.Errors)
	}
	if !resp.Ok || !resp.Reload {
		t.Errorf("resp = %+v, want ok=true reload=true", resp)
	}
	if provider.saveCalls != 1 {
		t.Fatalf("SaveConfig called %d times, want 1", provider.saveCalls)
	}
	if provider.saved == nil || len(provider.saved.Lights) != 1 || provider.saved.Lights[0].Name != "Living Room Renamed" {
		t.Errorf("saved config = %+v, want the renamed light round-tripped faithfully", provider.saved)
	}
}

func TestConfigEdit_PostSaveFailure(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig(), saveErr: assertErr("disk full")}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	newCfg := app.EditableConfig{Lights: []app.LightEditConfig{{Name: "X", DigitalOutName: "gpio|d_out|5"}}}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: newCfg})
	code, resp := doConfigEditPost(t, ws, string(body))
	if code != 500 {
		t.Fatalf("status = %d, want 500", code)
	}
	if resp.Ok {
		t.Errorf("resp.Ok = true, want false on save failure")
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

// ---- POST: transport-level errors ----

func TestConfigEdit_PostNilProvider(t *testing.T) {
	ws := newConfigEditTestServer(t, baseFixtureState(), nil)
	code, resp := doConfigEditPost(t, ws, `{"config":{}}`)
	if code != 503 || resp.Ok {
		t.Fatalf("status=%d resp=%+v, want 503 ok=false", code, resp)
	}
}

func TestConfigEdit_PostMalformedBody(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)
	code, resp := doConfigEditPost(t, ws, `{not valid json`)
	if code != 400 || resp.Ok {
		t.Fatalf("status=%d resp=%+v, want 400 ok=false", code, resp)
	}
}

func TestConfigEdit_PostOversizedBody(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	// Build a body over the 1 MiB cap using a single long device name.
	huge := bytes.Repeat([]byte("a"), maxConfigEditBodyBytes+1024)
	body := `{"config":{"Lights":[{"Name":"` + string(huge) + `"}]}}`

	req := httptest.NewRequest("POST", "/api/config/edit", strings.NewReader(body))
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, req)
	if w.Code != 400 {
		t.Fatalf("status = %d, want 400 (body oversized)", w.Code)
	}
	var resp apiConfigEditSaveResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Ok {
		t.Errorf("resp.Ok = true, want false for oversized body")
	}
}

// ---- POST: validation matrix ----

func postAndExpectErrors(t *testing.T, ws *WebServer, cfg app.EditableConfig, wantSubstrings ...string) []string {
	t.Helper()
	body, err := json.Marshal(apiConfigEditPostBody{Config: cfg})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	code, resp := doConfigEditPost(t, ws, string(body))
	if code != 400 {
		t.Fatalf("status = %d, want 400, resp=%+v", code, resp)
	}
	if resp.Ok {
		t.Fatalf("resp.Ok = true, want false")
	}
	joined := strings.Join(resp.Errors, " | ")
	for _, want := range wantSubstrings {
		if !strings.Contains(joined, want) {
			t.Errorf("errors = %v, want a message containing %q", resp.Errors, want)
		}
	}
	return resp.Errors
}

func newValidationTestServer(t *testing.T) *WebServer {
	t.Helper()
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	return newConfigEditTestServer(t, baseFixtureState(), provider)
}

func TestConfigEdit_ValidationEmptyName(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "", DigitalOutName: "gpio|d_out|5"}},
	}
	postAndExpectErrors(t, ws, cfg, "name must not be empty")
}

func TestConfigEdit_ValidationDuplicateNameWithinList(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights: []app.LightEditConfig{
			{Name: "Kitchen", DigitalOutName: "gpio|d_out|5"},
			{Name: "Kitchen", DigitalOutName: "gpio|d_out|6"},
		},
	}
	postAndExpectErrors(t, ws, cfg, "duplicate name")
}

func TestConfigEdit_ValidationDuplicateNameAcrossTypes(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights:  []app.LightEditConfig{{Name: "Shared", DigitalOutName: "gpio|d_out|5"}},
		Outlets: []app.OutletEditConfig{{Name: "Shared", DigitalOutName: "gpio|d_out|6"}},
	}
	postAndExpectErrors(t, ws, cfg, "used more than once")
}

func TestConfigEdit_ValidationBadIoIdFormat(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: "not-a-valid-id"}},
	}
	postAndExpectErrors(t, ws, cfg, "is not a valid io id")
}

func TestConfigEdit_ValidationWrongIoType(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		// gpio|d_in|5 is a valid id, but the wrong type for DigitalOutName (d_out).
		Lights: []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: "gpio|d_in|5"}},
	}
	postAndExpectErrors(t, ws, cfg, "has io type")
}

func TestConfigEdit_ValidationEmptyIoId(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: ""}},
	}
	postAndExpectErrors(t, ws, cfg, "must not be empty")
}

func TestConfigEdit_ValidationUnconfiguredDriver(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: "ghost|d_out|5"}},
	}
	postAndExpectErrors(t, ws, cfg, "which is not configured")
}

func TestConfigEdit_ValidationDuplicateIoAssignment(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights:  []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: "gpio|d_out|5"}},
		Outlets: []app.OutletEditConfig{{Name: "Fan", DigitalOutName: "gpio|d_out|5"}},
	}
	postAndExpectErrors(t, ws, cfg, "assigned to more than one field")
}

func TestConfigEdit_ValidationDuplicateEventInputAcrossDInAndPushEvent(t *testing.T) {
	ws := newValidationTestServer(t)
	// Same underlying shelly input, expressed once as d_in and once as
	// push_event: must be caught as a duplicate via canonicalization.
	cfg := app.EditableConfig{
		Buttons: []app.ButtonEditConfig{
			{Name: "Btn1", EventInputName: "shelly|d_in|dev123:0"},
			{Name: "Btn2", EventInputName: "shelly|push_event|dev123:0"},
		},
	}
	postAndExpectErrors(t, ws, cfg, "assigned to more than one field")
}

func TestConfigEdit_ValidationButtonEventInputAcceptsDIn(t *testing.T) {
	// d_in must be accepted (not flagged as wrong-type) since SaveConfig
	// normalizes it to push_event.
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Buttons: []app.ButtonEditConfig{{Name: "Btn1", EventInputName: "shelly|d_in|dev123:0"}},
	}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: cfg})
	code, resp := doConfigEditPost(t, ws, string(body))
	if code != 200 {
		t.Fatalf("status = %d, want 200 (d_in should be accepted for EventInputName), errors=%v", code, resp.Errors)
	}
}

func TestConfigEdit_ValidationControlRelationUnknownEvent(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: "gpio|d_out|5"}},
		Buttons: []app.ButtonEditConfig{{
			Name:           "Btn1",
			EventInputName: "shelly|push_event|dev123:0",
			ControlDevices: []app.ControlDeviceEdit{
				{EventType: "quadruple_press", Action: "toggle", DeviceName: "Kitchen"},
			},
		}},
	}
	postAndExpectErrors(t, ws, cfg, "unknown event type")
}

func TestConfigEdit_ValidationControlRelationUnknownVerb(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: "gpio|d_out|5"}},
		Buttons: []app.ButtonEditConfig{{
			Name:           "Btn1",
			EventInputName: "shelly|push_event|dev123:0",
			ControlDevices: []app.ControlDeviceEdit{
				{EventType: "single_press", Action: "explode", DeviceName: "Kitchen"},
			},
		}},
	}
	postAndExpectErrors(t, ws, cfg, "unknown action verb")
}

func TestConfigEdit_ValidationControlRelationLevelOutOfRange(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		DimmableLights: []app.DimmableLightEditConfig{{Name: "Hall", DigitalOutName: "gpio|d_out|5", AnalogOutName: "gpio|a_out|1"}},
		Buttons: []app.ButtonEditConfig{{
			Name:           "Btn1",
			EventInputName: "shelly|push_event|dev123:0",
			ControlDevices: []app.ControlDeviceEdit{
				{EventType: "single_press", Action: "brightness", Level: 140, DeviceName: "Hall"},
			},
		}},
	}
	postAndExpectErrors(t, ws, cfg, "out of range")
}

func TestConfigEdit_ValidationControlRelationDanglingTarget(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Buttons: []app.ButtonEditConfig{{
			Name:           "Btn1",
			EventInputName: "shelly|push_event|dev123:0",
			ControlDevices: []app.ControlDeviceEdit{
				{EventType: "single_press", Action: "toggle", DeviceName: "Nonexistent"},
			},
		}},
	}
	postAndExpectErrors(t, ws, cfg, "does not exist")
}

func TestConfigEdit_ValidationControlRelationAllowsColorLightTarget(t *testing.T) {
	// "Mood" is a color light in baseFixtureState - not part of EditableConfig,
	// but must still be accepted as a valid control-relation target.
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Buttons: []app.ButtonEditConfig{{
			Name:           "Btn1",
			EventInputName: "shelly|push_event|dev123:0",
			ControlDevices: []app.ControlDeviceEdit{
				{EventType: "single_press", Action: "toggle", DeviceName: "Mood"},
			},
		}},
	}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: cfg})
	code, resp := doConfigEditPost(t, ws, string(body))
	if code != 200 {
		t.Fatalf("status = %d, want 200 (color light must be a valid target), errors=%v", code, resp.Errors)
	}
}

func TestConfigEdit_ValidationSceneActionParseFailure(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Scenes: []app.SceneEditConfig{{
			Name: "Evening",
			States: []app.SceneStateEditConfig{
				{Name: "cozy", Actions: []string{"not a valid action"}},
			},
		}},
	}
	postAndExpectErrors(t, ws, cfg, "Evening")
}

func TestConfigEdit_ValidationSceneActionDanglingTarget(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Scenes: []app.SceneEditConfig{{
			Name: "Evening",
			States: []app.SceneStateEditConfig{
				{Name: "cozy", Actions: []string{"toggle:Nonexistent"}},
			},
		}},
	}
	postAndExpectErrors(t, ws, cfg, "does not exist")
}

func TestConfigEdit_ValidationSceneActionLevelOutOfRange(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		DimmableLights: []app.DimmableLightEditConfig{{Name: "Hall", DigitalOutName: "gpio|d_out|5", AnalogOutName: "gpio|a_out|1"}},
		Scenes: []app.SceneEditConfig{{
			Name: "Evening",
			States: []app.SceneStateEditConfig{
				{Name: "cozy", Actions: []string{"brightness:150:Hall"}},
			},
		}},
	}
	postAndExpectErrors(t, ws, cfg, "out of range")
}

func TestConfigEdit_ValidationDefaultSetpointOutOfRange(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		DimmableLights: []app.DimmableLightEditConfig{{
			Name: "Hall", DigitalOutName: "gpio|d_out|5", AnalogOutName: "gpio|a_out|1", DefaultSetpoint: 200,
		}},
	}
	postAndExpectErrors(t, ws, cfg, "DefaultSetpoint")
}

func TestConfigEdit_ValidationCollectsAllErrorsAtOnce(t *testing.T) {
	// Sanity check on the "collect everything, don't stop at the first
	// error" contract: a config with three independent violations must
	// return (at least) three distinct error messages in one response.
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights: []app.LightEditConfig{
			{Name: "", DigitalOutName: "not-a-valid-id"},
		},
		Buttons: []app.ButtonEditConfig{{
			Name:           "Btn1",
			EventInputName: "shelly|push_event|dev123:0",
			ControlDevices: []app.ControlDeviceEdit{
				{EventType: "bogus_event", Action: "toggle", DeviceName: "Nonexistent"},
			},
		}},
	}
	errs := postAndExpectErrors(t, ws, cfg,
		"name must not be empty",
		"is not a valid io id",
		"unknown event type",
		"does not exist",
	)
	if len(errs) < 4 {
		t.Errorf("expected at least 4 collected errors, got %d: %v", len(errs), errs)
	}
}
