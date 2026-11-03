package server

import (
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/hubertat/swkit/app"
)

func TestConfigEdit_GetPublishesPerTypeVerbs(t *testing.T) {
	cfg := baseFixtureConfig()
	cfg.WindowShutters = []app.WindowShutterEditConfig{{Name: "Sh", OpenDigitalOutName: "gpio|d_out|20", CloseDigitalOutName: "gpio|d_out|21", OpenDurationSeconds: 20}}
	ws := newConfigEditTestServer(t, baseFixtureState(), &fakeSavingConfigProvider{cfg: cfg})
	w := httptest.NewRecorder()
	ws.handleApiConfigEdit(w, httptest.NewRequest("GET", "/api/config/edit", nil))
	var resp apiConfigEditResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	by := resp.Meta.ActionVerbsByType
	if !slices.Equal(by["window_shutter"], []string{"open", "close", "stop", "position", "toggle"}) {
		t.Fatalf("shutter verbs: %v", by["window_shutter"])
	}
	if slices.Contains(by["light"], "open") || slices.Contains(by["scene"], "position") {
		t.Fatal("lights/scenes must never get shutter verbs")
	}
	if resp.Meta.TargetTypes["Sh"] != "window_shutter" || resp.Meta.TargetTypes["Mood"] != "color_light" || resp.Meta.TargetTypes["Living Room"] != "light" {
		t.Fatalf("target types: %v", resp.Meta.TargetTypes)
	}
	if resp.Meta.ShutterLimits.MaxDurationSeconds != 600 || !strings.Contains(resp.Meta.ShutterLimits.ReverseDelayHelp, "0 uses 500 ms") {
		t.Fatalf("limits: %+v", resp.Meta.ShutterLimits)
	}
	if len(resp.Config.WindowShutters) != 1 {
		t.Fatal("shutters must be in the editable config")
	}
}

func TestConfigEdit_PostIgnoresPostedColorLightOwners(t *testing.T) {
	cfg := baseFixtureConfig()
	cfg.ColorLightOutputOwners = map[string]string{"gpio|d_out|8": "Mood"}
	provider := &fakeSavingConfigProvider{cfg: cfg}
	ws := newConfigEditTestServer(t, baseFixtureState(), provider)

	posted := baseFixtureConfig()
	posted.WindowShutters = []app.WindowShutterEditConfig{{Name: "Sh", OpenDigitalOutName: "gpio|d_out|8", CloseDigitalOutName: "gpio|d_out|21", OpenDurationSeconds: 20}}
	posted.ColorLightOutputOwners = map[string]string{} // attempt to hide the conflict
	body, _ := json.Marshal(map[string]any{"config": posted, "revision": provider.Revision()})
	code, resp := doConfigEditPost(t, ws, string(body))
	if code != 400 {
		t.Fatalf("status = %d, want 400 (%+v)", code, resp)
	}
	found := false
	for _, e := range resp.Errors {
		if strings.Contains(e, `already used by color light "Mood"`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected color light conflict, got %v", resp.Errors)
	}
}
