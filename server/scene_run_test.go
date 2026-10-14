package server

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/hubertat/swkit/app"
)

// runningStateProvider is a fakeStateProvider that can also activate scenes.
type runningStateProvider struct {
	fakeStateProvider
	gotScene string
	gotState int
	err      error
}

func (p *runningStateProvider) ActivateSceneState(name string, state int) app.ControlResult {
	p.gotScene, p.gotState = name, state
	return app.ControlResult{DeviceName: name, Action: "activate cozy", Error: p.err}
}

func postSceneRun(t *testing.T, ws *WebServer, body, contentType string) (int, apiSceneRunResponse) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/scenes/run", strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	w := httptest.NewRecorder()
	ws.Mux().ServeHTTP(w, req)
	var resp apiSceneRunResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func TestSceneRunActivatesState(t *testing.T) {
	p := &runningStateProvider{}
	ws, err := NewWebServerWithConfig(p, 0, log.New(nil), WebServerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	code, resp := postSceneRun(t, ws, `{"scene":" Evening ","state":1}`, "application/json")
	if code != 200 || !resp.Ok {
		t.Fatalf("status = %d, resp = %+v; want 200 ok", code, resp)
	}
	if p.gotScene != "Evening" || p.gotState != 1 {
		t.Errorf("provider got (%q, %d), want (Evening, 1)", p.gotScene, p.gotState)
	}
}

func TestSceneRunErrors(t *testing.T) {
	notFound := &runningStateProvider{err: fmt.Errorf("%w: %q", app.ErrSceneNotFound, "Nope")}
	failing := &runningStateProvider{err: fmt.Errorf("device Hall: driver offline")}
	cases := []struct {
		name     string
		provider app.StateProvider
		body, ct string
		want     int
	}{
		{"no runner", &fakeStateProvider{}, `{"scene":"Evening","state":1}`, "application/json", 503},
		{"form post (CSRF)", &runningStateProvider{}, `{"scene":"Evening","state":1}`, "text/plain", 415},
		{"unknown field", &runningStateProvider{}, `{"scene":"Evening","state":1,"x":1}`, "application/json", 400},
		{"unknown scene", notFound, `{"scene":"Nope","state":1}`, "application/json", 404},
		{"device failure", failing, `{"scene":"Evening","state":1}`, "application/json", 502},
	}
	for _, tc := range cases {
		ws, err := NewWebServerWithConfig(tc.provider, 0, log.New(nil), WebServerOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if code, resp := postSceneRun(t, ws, tc.body, tc.ct); code != tc.want {
			t.Errorf("%s: status = %d (%+v), want %d", tc.name, code, resp, tc.want)
		}
	}

	ws, _ := NewWebServerWithConfig(&runningStateProvider{}, 0, log.New(nil), WebServerOptions{})
	w := httptest.NewRecorder()
	ws.Mux().ServeHTTP(w, httptest.NewRequest("GET", "/api/scenes/run", nil))
	if w.Code != 405 {
		t.Errorf("GET: status = %d, want 405", w.Code)
	}
}

func TestScenesPageRoute(t *testing.T) {
	ws, _ := NewWebServerWithConfig(&fakeStateProvider{}, 0, log.New(nil), WebServerOptions{})
	w := httptest.NewRecorder()
	ws.Mux().ServeHTTP(w, httptest.NewRequest("GET", "/scenes", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "scenes.js") {
		t.Fatalf("GET /scenes: status %d, scenes.js referenced: %v", w.Code, strings.Contains(w.Body.String(), "scenes.js"))
	}
}
