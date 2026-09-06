package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
)

// fakeSavingConfigProvider records the config passed to SaveConfig, and can
// be told to fail the save. Revision/SaveConfigIfRevision mirror
// SwKitConfigProvider's real content-hash + compare-and-swap behavior (see
// config_provider.go) closely enough to exercise the handler's lost-update
// protection: f.cfg is the "currently persisted" config, and its revision is
// a content hash of it, recomputed fresh on every call (not cached), so a
// direct mutation of f.cfg between calls (as a test simulating an
// out-of-band change might do) is picked up exactly like the real provider
// would pick up a concurrent save.
type fakeSavingConfigProvider struct {
	cfg       app.EditableConfig
	saved     *app.EditableConfig
	saveErr   error
	saveCalls int
}

func (f *fakeSavingConfigProvider) GetEditableConfig() app.EditableConfig { return f.cfg }

func (f *fakeSavingConfigProvider) Revision() string { return fakeConfigRevision(f.cfg) }

func fakeConfigRevision(cfg app.EditableConfig) string {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "unknown"
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])[:16]
}

func (f *fakeSavingConfigProvider) SaveConfig(config app.EditableConfig) error {
	f.saveCalls++
	if f.saveErr != nil {
		return f.saveErr
	}
	cp := config
	f.saved = &cp
	f.cfg = config
	return nil
}

func (f *fakeSavingConfigProvider) SaveConfigIfRevision(config app.EditableConfig, expectedRevision string) error {
	if expectedRevision != f.Revision() {
		return app.ErrConfigRevisionMismatch
	}
	return f.SaveConfig(config)
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

// ---- Revision / lost-update protection ----

func TestConfigEdit_GetIncludesRevision(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	req := httptest.NewRequest("GET", "/api/config/edit", nil)
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, req)

	var resp apiConfigEditResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if resp.Revision == "" {
		t.Fatal("resp.Revision is empty, want a non-empty content hash")
	}
	if want := provider.Revision(); resp.Revision != want {
		t.Errorf("resp.Revision = %q, want %q (provider.Revision())", resp.Revision, want)
	}
}

func TestConfigEdit_PostMatchingRevisionSaves(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	newCfg := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "Renamed", DigitalOutName: "gpio|d_out|5"}},
	}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: newCfg, Revision: provider.Revision()})

	code, resp := doConfigEditPost(t, ws, string(body))
	if code != 200 || !resp.Ok {
		t.Fatalf("status = %d resp = %+v, want 200 ok=true (revision matched the just-fetched config)", code, resp)
	}
	if provider.saved == nil || len(provider.saved.Lights) != 1 || provider.saved.Lights[0].Name != "Renamed" {
		t.Errorf("saved = %+v, want the renamed light persisted", provider.saved)
	}
}

func TestConfigEdit_PostStaleRevisionRejectedWithConflict(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	// Simulate a concurrent editor session (or an out-of-band config
	// reload) landing between this session's GET and its save: the
	// provider's underlying config changes, so the revision this POST
	// carries (still the pre-change one) is now stale.
	staleRevision := provider.Revision()
	provider.cfg = app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "Changed Elsewhere", DigitalOutName: "gpio|d_out|5"}},
	}

	newCfg := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "ShouldNotSave", DigitalOutName: "gpio|d_out|5"}},
	}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: newCfg, Revision: staleRevision})

	code, resp := doConfigEditPost(t, ws, string(body))
	if code != http.StatusConflict {
		t.Fatalf("status = %d, want 409, resp = %+v", code, resp)
	}
	if resp.Ok {
		t.Errorf("resp.Ok = true, want false on a revision conflict")
	}
	if len(resp.Errors) == 0 {
		t.Error("resp.Errors is empty, want a message explaining the conflict")
	}
	if provider.saved != nil {
		t.Errorf("SaveConfig persisted a config despite a stale revision: %+v", provider.saved)
	}
	if provider.saveCalls != 0 {
		t.Errorf("SaveConfig called %d times on a stale revision, want 0 (must not mutate the underlying config)", provider.saveCalls)
	}
	if len(provider.cfg.Lights) != 1 || provider.cfg.Lights[0].Name != "Changed Elsewhere" {
		t.Errorf("underlying config changed unexpectedly: %+v, want the out-of-band change left untouched", provider.cfg)
	}
}

// midGetChangeProvider applies a one-shot config change right after the first
// provider read the GET handler makes, simulating another writer (a second
// edit session, the TUI, or an out-of-band reload) landing between the
// handler's two separate reads of Revision and GetEditableConfig.
type midGetChangeProvider struct {
	*fakeSavingConfigProvider
	after   app.EditableConfig
	applied bool
}

func (m *midGetChangeProvider) applyOnce() {
	if m.applied {
		return
	}
	m.applied = true
	m.cfg = m.after
}

func (m *midGetChangeProvider) GetEditableConfig() app.EditableConfig {
	cfg := m.fakeSavingConfigProvider.GetEditableConfig()
	m.applyOnce()
	return cfg
}

func (m *midGetChangeProvider) Revision() string {
	rev := m.fakeSavingConfigProvider.Revision()
	m.applyOnce()
	return rev
}

// TestConfigEdit_GetPairingIsFailSafeUnderConcurrentSave proves the (config,
// revision) pair the GET hands out can never be accepted by a later POST once
// a save has landed between the handler's two provider reads. Whichever read
// happens first is the one served from the pre-change config, so posting the
// GET's own pair straight back must be rejected - never silently saved over
// the change that landed. Reading the config first and the revision second
// would fail this: the response would carry the *new* revision with the *old*
// config, and the POST's compare-and-swap would happily match and overwrite.
func TestConfigEdit_GetPairingIsFailSafeUnderConcurrentSave(t *testing.T) {
	base := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	provider := &midGetChangeProvider{
		fakeSavingConfigProvider: base,
		after: app.EditableConfig{
			Lights: []app.LightEditConfig{{Name: "Changed Mid GET", DigitalOutName: "gpio|d_out|5"}},
		},
	}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	req := httptest.NewRequest("GET", "/api/config/edit", nil)
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, req)

	var got apiConfigEditResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (body: %s)", err, w.Body.String())
	}
	if !provider.applied {
		t.Fatal("the simulated concurrent save never fired; the handler read neither Revision nor GetEditableConfig")
	}

	// Post the GET's own pair back, edited as a user would.
	edited := got.Config
	edited.Lights = []app.LightEditConfig{{Name: "User Edit", DigitalOutName: "gpio|d_out|5"}}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: edited, Revision: got.Revision})

	code, resp := doConfigEditPost(t, ws, string(body))
	if code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: the GET paired a snapshot with a revision from a different config version, so this save must be rejected, resp = %+v", code, resp)
	}
	if base.saveCalls != 0 || base.saved != nil {
		t.Errorf("save persisted %+v (%d calls), want none: this is the silent overwrite the revision handshake exists to prevent", base.saved, base.saveCalls)
	}
}

func TestConfigEdit_PostMissingRevisionRejected(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	newCfg := app.EditableConfig{Lights: []app.LightEditConfig{{Name: "X", DigitalOutName: "gpio|d_out|5"}}}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: newCfg}) // Revision left zero-valued ("").

	code, resp := doConfigEditPost(t, ws, string(body))
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (revision is required), resp = %+v", code, resp)
	}
	if provider.saveCalls != 0 {
		t.Errorf("SaveConfig called %d times with a missing revision, want 0", provider.saveCalls)
	}
}

func TestConfigEdit_RevisionChangesAfterSuccessfulSave(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	before := provider.Revision()

	newCfg := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "Changed", DigitalOutName: "gpio|d_out|5"}},
	}
	if err := provider.SaveConfigIfRevision(newCfg, before); err != nil {
		t.Fatalf("SaveConfigIfRevision: %v", err)
	}

	after := provider.Revision()
	if after == before {
		t.Errorf("Revision() unchanged after a save that altered the config: before=%q after=%q", before, after)
	}

	// The old (pre-save) revision must no longer be accepted.
	if err := provider.SaveConfigIfRevision(newCfg, before); err == nil {
		t.Error("SaveConfigIfRevision with the pre-save revision succeeded after a save, want ErrConfigRevisionMismatch")
	} else if !errors.Is(err, app.ErrConfigRevisionMismatch) {
		t.Errorf("SaveConfigIfRevision error = %v, want app.ErrConfigRevisionMismatch", err)
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
	req.Header.Set("Content-Type", "application/json")
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
	body, err := json.Marshal(apiConfigEditPostBody{Config: newCfg, Revision: provider.Revision()})
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
	body, _ := json.Marshal(apiConfigEditPostBody{Config: newCfg, Revision: provider.Revision()})
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
	req.Header.Set("Content-Type", "application/json")
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
	if fp, ok := ws.opts.ConfigProvider.(*fakeSavingConfigProvider); ok && fp.saveCalls != 0 {
		t.Errorf("SaveConfig called %d times on a validation failure, want 0 (a rejected config must never be persisted)", fp.saveCalls)
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
	body, _ := json.Marshal(apiConfigEditPostBody{Config: cfg, Revision: fakeConfigRevision(baseFixtureConfig())})
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
	body, _ := json.Marshal(apiConfigEditPostBody{Config: cfg, Revision: fakeConfigRevision(baseFixtureConfig())})
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
	postAndExpectErrors(t, ws, cfg, `unknown action "not a valid action"`)
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

// ---- Colon-in-name rejection (finding 1) ----

func TestConfigEdit_ValidationNameWithColonRejectedLight(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "Kitchen: Main", DigitalOutName: "gpio|d_out|5"}},
	}
	postAndExpectErrors(t, ws, cfg, "must not contain ':'")
}

func TestConfigEdit_ValidationNameWithColonRejectedEveryList(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights:         []app.LightEditConfig{{Name: "L:1", DigitalOutName: "gpio|d_out|5"}},
		DimmableLights: []app.DimmableLightEditConfig{{Name: "D:1", DigitalOutName: "gpio|d_out|6", AnalogOutName: "gpio|a_out|1"}},
		Outlets:        []app.OutletEditConfig{{Name: "O:1", DigitalOutName: "gpio|d_out|7"}},
		Buttons:        []app.ButtonEditConfig{{Name: "B:1", EventInputName: "shelly|push_event|dev123:0"}},
		Scenes:         []app.SceneEditConfig{{Name: "S:1"}},
	}
	errs := postAndExpectErrors(t, ws, cfg, "must not contain ':'")
	count := 0
	for _, e := range errs {
		if strings.Contains(e, "must not contain ':'") {
			count++
		}
	}
	if count != 5 {
		t.Errorf("expected a colon-rejection error for all 5 lists, got %d: %v", count, errs)
	}
}

// ---- Scene ordering: only earlier scenes are valid targets (finding 2) ----

func TestConfigEdit_ValidationSceneActionSelfReference(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Scenes: []app.SceneEditConfig{{
			Name: "Evening",
			States: []app.SceneStateEditConfig{
				{Name: "cozy", Actions: []string{"toggle:Evening"}},
			},
		}},
	}
	postAndExpectErrors(t, ws, cfg, "cannot target itself")
}

func TestConfigEdit_ValidationSceneActionForwardReference(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Scenes: []app.SceneEditConfig{
			{
				Name: "Evening",
				States: []app.SceneStateEditConfig{
					{Name: "cozy", Actions: []string{"toggle:Night"}},
				},
			},
			{Name: "Night", States: []app.SceneStateEditConfig{{Name: "on", Actions: nil}}},
		},
	}
	postAndExpectErrors(t, ws, cfg, "may only target earlier scenes")
}

func TestConfigEdit_ValidationSceneActionAllowsEarlierScene(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Scenes: []app.SceneEditConfig{
			{Name: "Night", States: []app.SceneStateEditConfig{{Name: "on", Actions: nil}}},
			{
				Name: "Evening",
				States: []app.SceneStateEditConfig{
					{Name: "cozy", Actions: []string{"toggle:Night"}},
				},
			},
		},
	}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: cfg, Revision: fakeConfigRevision(baseFixtureConfig())})
	code, resp := doConfigEditPost(t, ws, string(body))
	if code != 200 {
		t.Fatalf("status = %d, want 200 (targeting an earlier scene must be allowed), errors=%v", code, resp.Errors)
	}
}

// ---- Scene state validation (finding 12) ----

func TestConfigEdit_ValidationSceneStateEmptyName(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Scenes: []app.SceneEditConfig{{
			Name:   "Evening",
			States: []app.SceneStateEditConfig{{Name: "  ", Actions: nil}},
		}},
	}
	postAndExpectErrors(t, ws, cfg, "state name must not be empty")
}

func TestConfigEdit_ValidationSceneStateDuplicateName(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Scenes: []app.SceneEditConfig{{
			Name: "Evening",
			States: []app.SceneStateEditConfig{
				{Name: "cozy", Actions: nil},
				{Name: "cozy", Actions: nil},
			},
		}},
	}
	postAndExpectErrors(t, ws, cfg, `duplicate state name "cozy"`)
}

// ---- Brightness targeting a non-Dimmable device (finding 13) ----

func TestConfigEdit_ValidationControlRelationBrightnessOnPlainLight(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Lights: []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: "gpio|d_out|5"}},
		Buttons: []app.ButtonEditConfig{{
			Name:           "Btn1",
			EventInputName: "shelly|push_event|dev123:0",
			ControlDevices: []app.ControlDeviceEdit{
				{EventType: "single_press", Action: "brightness", Level: 50, DeviceName: "Kitchen"},
			},
		}},
	}
	postAndExpectErrors(t, ws, cfg, `"Kitchen" does not support brightness (target type: light)`)
}

func TestConfigEdit_ValidationSceneActionBrightnessOnOutlet(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Outlets: []app.OutletEditConfig{{Name: "Fan", DigitalOutName: "gpio|d_out|5"}},
		Scenes: []app.SceneEditConfig{{
			Name: "Evening",
			States: []app.SceneStateEditConfig{
				{Name: "cozy", Actions: []string{"brightness:50:Fan"}},
			},
		}},
	}
	postAndExpectErrors(t, ws, cfg, `"Fan" does not support brightness (target type: outlet)`)
}

func TestConfigEdit_ValidationSceneActionBrightnessOnScene(t *testing.T) {
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		Scenes: []app.SceneEditConfig{
			{Name: "Night", States: []app.SceneStateEditConfig{{Name: "on", Actions: nil}}},
			{
				Name: "Evening",
				States: []app.SceneStateEditConfig{
					{Name: "cozy", Actions: []string{"brightness:50:Night"}},
				},
			},
		},
	}
	postAndExpectErrors(t, ws, cfg, `"Night" does not support brightness (target type: scene)`)
}

func TestConfigEdit_ValidationBrightnessOnDimmableLightStillAllowed(t *testing.T) {
	// Sanity check: the dimmable-target check must not regress the existing
	// (already-passing) dimmable light case.
	ws := newValidationTestServer(t)
	cfg := app.EditableConfig{
		DimmableLights: []app.DimmableLightEditConfig{{Name: "Hall", DigitalOutName: "gpio|d_out|5", AnalogOutName: "gpio|a_out|1"}},
		Buttons: []app.ButtonEditConfig{{
			Name:           "Btn1",
			EventInputName: "shelly|push_event|dev123:0",
			ControlDevices: []app.ControlDeviceEdit{
				{EventType: "single_press", Action: "brightness", Level: 50, DeviceName: "Hall"},
			},
		}},
	}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: cfg, Revision: fakeConfigRevision(baseFixtureConfig())})
	code, resp := doConfigEditPost(t, ws, string(body))
	if code != 200 {
		t.Fatalf("status = %d, want 200 (dimmable light must be a valid brightness target), errors=%v", code, resp.Errors)
	}
}

// ---- CSRF / transport hardening (finding 3) ----

func TestConfigEdit_PostWrongContentTypeRejected(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	cfg := app.EditableConfig{Lights: []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: "gpio|d_out|5"}}}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: cfg})

	req := httptest.NewRequest("POST", "/api/config/edit", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", w.Code)
	}
	if provider.saveCalls != 0 {
		t.Errorf("SaveConfig called on a text/plain POST, want 0 calls")
	}
}

func TestConfigEdit_PostCrossSiteSecFetchRejected(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	cfg := app.EditableConfig{Lights: []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: "gpio|d_out|5"}}}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: cfg})

	req := httptest.NewRequest("POST", "/api/config/edit", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if provider.saveCalls != 0 {
		t.Errorf("SaveConfig called on a cross-site POST, want 0 calls")
	}
}

func TestConfigEdit_PostCrossOriginRejected(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	cfg := app.EditableConfig{Lights: []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: "gpio|d_out|5"}}}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: cfg})

	req := httptest.NewRequest("POST", "/api/config/edit", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://evil.example.com")
	req.Host = "swkit.local"
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", w.Code)
	}
	if provider.saveCalls != 0 {
		t.Errorf("SaveConfig called on a cross-origin POST, want 0 calls")
	}
}

func TestConfigEdit_PostSameOriginAllowed(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	cfg := app.EditableConfig{Lights: []app.LightEditConfig{{Name: "Kitchen", DigitalOutName: "gpio|d_out|5"}}}
	body, _ := json.Marshal(apiConfigEditPostBody{Config: cfg, Revision: provider.Revision()})

	req := httptest.NewRequest("POST", "/api/config/edit", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	req.Host = "swkit.local"
	req.Header.Set("Origin", "http://swkit.local")
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (same-origin Origin header must be allowed), body=%s", w.Code, w.Body.String())
	}
	if provider.saveCalls != 1 {
		t.Errorf("SaveConfig called %d times, want 1", provider.saveCalls)
	}
}

// ---- Unknown-field rejection (finding 15d) ----

func TestConfigEdit_PostUnknownFieldRejected(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	body := `{"config":{"Lights":[{"Name":"Kitchen","DigitalOutName":"gpio|d_out|5","DisbaleHomekit":true}]}}`
	code, resp := doConfigEditPost(t, ws, body)
	if code != 400 {
		t.Fatalf("status = %d, want 400, resp=%+v", code, resp)
	}
	if resp.Ok {
		t.Errorf("resp.Ok = true, want false for a typo'd field")
	}
	joined := strings.Join(resp.Errors, " | ")
	if !strings.Contains(joined, "DisbaleHomekit") {
		t.Errorf("errors = %v, want a message naming the unknown field DisbaleHomekit", resp.Errors)
	}
	if provider.saveCalls != 0 {
		t.Errorf("SaveConfig called on a request with an unknown field, want 0 calls")
	}
}

// ---- Success-path round trip: no lossy re-formatting (finding 14) ----

func TestConfigEdit_PostSuccessRoundTripsControlRelationsAndSceneActionsByteIdentical(t *testing.T) {
	provider := &fakeSavingConfigProvider{cfg: baseFixtureConfig()}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	cfg := app.EditableConfig{
		Lights:  []app.LightEditConfig{{Name: "Living Room", DigitalOutName: "gpio|d_out|5"}},
		Outlets: []app.OutletEditConfig{{Name: "Fan", DigitalOutName: "gpio|d_out|6"}},
		DimmableLights: []app.DimmableLightEditConfig{
			{Name: "Hall", DigitalOutName: "gpio|d_out|7", AnalogOutName: "gpio|a_out|1"},
		},
		Buttons: []app.ButtonEditConfig{{
			Name:           "Wall Switch",
			EventInputName: "shelly|push_event|dev123:0",
			ControlDevices: []app.ControlDeviceEdit{
				{EventType: "single_press", Action: "toggle", DeviceName: "Living Room"},
				{EventType: "double_press", Action: "brightness", Level: 40, DeviceName: "Hall"},
				{EventType: "long_press", Action: "off", DeviceName: "Fan"},
			},
		}},
		Scenes: []app.SceneEditConfig{{
			Name: "Evening",
			States: []app.SceneStateEditConfig{
				{Name: "cozy", Actions: []string{"brightness:40:Hall", "on:Fan", "toggle:Living Room"}},
			},
		}},
	}

	body, err := json.Marshal(apiConfigEditPostBody{Config: cfg, Revision: provider.Revision()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	code, resp := doConfigEditPost(t, ws, string(body))
	if code != 200 {
		t.Fatalf("status = %d, want 200, errors=%v", code, resp.Errors)
	}
	if provider.saved == nil {
		t.Fatal("SaveConfig was not called")
	}

	saved := *provider.saved
	if len(saved.Buttons) != 1 || len(saved.Buttons[0].ControlDevices) != len(cfg.Buttons[0].ControlDevices) {
		t.Fatalf("saved buttons = %+v, want control relations preserved", saved.Buttons)
	}
	for i, want := range cfg.Buttons[0].ControlDevices {
		got := saved.Buttons[0].ControlDevices[i]
		if got != want {
			t.Errorf("control relation #%d = %+v, want byte-identical %+v", i, got, want)
		}
	}

	if len(saved.Scenes) != 1 || len(saved.Scenes[0].States) != 1 {
		t.Fatalf("saved scenes = %+v, want one scene with one state", saved.Scenes)
	}
	gotActions := saved.Scenes[0].States[0].Actions
	wantActions := cfg.Scenes[0].States[0].Actions
	if len(gotActions) != len(wantActions) {
		t.Fatalf("saved scene actions = %v, want %v", gotActions, wantActions)
	}
	for i := range wantActions {
		if gotActions[i] != wantActions[i] {
			t.Errorf("scene action #%d = %q, want byte-identical %q", i, gotActions[i], wantActions[i])
		}
	}
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
