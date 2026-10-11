package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/charmbracelet/log"
)

func TestControlServiceConfig(t *testing.T) {
	cfg := controlServiceConfig("My Home", 8080, "/control", "v1.2.3")
	if cfg.Type != "_swkit._tcp" || cfg.Name != "My Home" || cfg.Port != 8080 {
		t.Errorf("got type=%q name=%q port=%d", cfg.Type, cfg.Name, cfg.Port)
	}
	if cfg.Text["path"] != "/control" || cfg.Text["api"] != "1" || cfg.Text["ver"] != "v1.2.3" {
		t.Errorf("unexpected TXT %v", cfg.Text)
	}

	if cfg := controlServiceConfig("", 1, "/c", ""); cfg.Name != "swkit" {
		t.Errorf("empty name should default to swkit, got %q", cfg.Name)
	} else if _, ok := cfg.Text["ver"]; ok {
		t.Errorf("empty version should omit ver, got %v", cfg.Text)
	}
}

func TestControlServerInfo(t *testing.T) {
	cs, err := NewControlServer(&fakeController{}, "/panel/", "Flat", log.New(nil))
	if err != nil {
		t.Fatalf("NewControlServer: %v", err)
	}
	if cs.Endpoint() != "/panel" {
		t.Errorf("Endpoint() = %q, want /panel", cs.Endpoint())
	}

	w := httptest.NewRecorder()
	cs.dispatch(w, httptest.NewRequest("GET", "/panel/api/info", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got struct {
		Name string `json:"name"`
		API  int    `json:"api"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	if got.Name != "Flat" || got.API != controlAPIVersion {
		t.Errorf("info = %+v", got)
	}

	w = httptest.NewRecorder()
	cs.dispatch(w, httptest.NewRequest("POST", "/panel/api/info", nil))
	if w.Code != 405 {
		t.Errorf("POST status = %d, want 405", w.Code)
	}
}

func TestWebServerPort(t *testing.T) {
	ws, err := NewWebServer(nil, 0, log.New(nil))
	if err != nil {
		t.Fatalf("NewWebServer: %v", err)
	}
	if ws.Port() != defaultWebPort {
		t.Errorf("Port() = %d, want %d", ws.Port(), defaultWebPort)
	}
}
