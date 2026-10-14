package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/hubertat/swkit/app"
)

// maxSceneRunBodyBytes caps a POST /api/scenes/run body; it only carries a
// scene name and a state index.
const maxSceneRunBodyBytes = 4 << 10

// sceneRunner is the optional provider capability behind POST
// /api/scenes/run. The web server is otherwise read-only towards devices
// (it only takes an app.StateProvider), so this is discovered by assertion.
type sceneRunner interface {
	ActivateSceneState(name string, stateIndex int) app.ControlResult
}

func (ws *WebServer) sceneRunner() sceneRunner {
	r, _ := ws.provider.(sceneRunner)
	return r
}

// configEditMeta is buildConfigEditMeta plus capabilities that depend on the
// server's wiring rather than on the config itself.
func (ws *WebServer) configEditMeta(cfg app.EditableConfig, state app.AppState) apiConfigEditMeta {
	meta := buildConfigEditMeta(cfg, state)
	meta.SceneRun = ws.sceneRunner() != nil
	return meta
}

type apiSceneRunBody struct {
	Scene string `json:"scene"`
	State int    `json:"state"`
}

type apiSceneRunResponse struct {
	Ok     bool   `json:"ok"`
	Action string `json:"action,omitempty"`
	Error  string `json:"error,omitempty"`
}

// handleApiSceneRun activates one state of a saved scene, so the Scene Studio
// can try a state on the real devices. It drives hardware, so it gets the
// same CSRF hardening as a config save (see handleConfigEditPost).
func (ws *WebServer) handleApiSceneRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	runner := ws.sceneRunner()
	if runner == nil {
		writeSceneRun(w, http.StatusServiceUnavailable, apiSceneRunResponse{Error: "running scenes is not available on this server"})
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(ct), "application/json") {
		writeSceneRun(w, http.StatusUnsupportedMediaType, apiSceneRunResponse{Error: "Content-Type must be application/json"})
		return
	}
	if err := checkSameOriginPost(r); err != nil {
		writeSceneRun(w, http.StatusForbidden, apiSceneRunResponse{Error: err.Error()})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxSceneRunBodyBytes)
	var body apiSceneRunBody
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeSceneRun(w, http.StatusBadRequest, apiSceneRunResponse{Error: "invalid request body: " + err.Error()})
		return
	}

	res := runner.ActivateSceneState(strings.TrimSpace(body.Scene), body.State)
	if res.Error != nil {
		status := http.StatusBadGateway // the state ran, but some device steps failed
		if errors.Is(res.Error, app.ErrSceneNotFound) {
			status = http.StatusNotFound
		} else {
			ws.logger.Error("scene run failed", "scene", body.Scene, "state", body.State, "err", res.Error)
		}
		writeSceneRun(w, status, apiSceneRunResponse{Action: res.Action, Error: res.Error.Error()})
		return
	}
	writeSceneRun(w, http.StatusOK, apiSceneRunResponse{Ok: true, Action: res.Action})
}

func writeSceneRun(w http.ResponseWriter, status int, resp apiSceneRunResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(resp)
}
