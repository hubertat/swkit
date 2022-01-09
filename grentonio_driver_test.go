package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mockGrentonIo() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Content-Type"), "application/json") {
			w.WriteHeader(http.StatusNotImplemented)
			return
		}

		type GrentonObject struct {
			Clu   string
			Id    string
			Kind  string
			Cmd   string
			Light struct {
				State bool
			}
		}

		cluId := "CLU_0d1cf087"
		objId := "DOU0302"

		query := []GrentonObject{}
		defer r.Body.Close()
		jsonReader := json.NewDecoder(r.Body)
		err := jsonReader.Decode(&query)
		if err != nil {
			fmt.Fprint(w, err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		for _, obj := range query {
			if !strings.EqualFold(strings.ToLower(obj.Clu), strings.ToLower(cluId)) {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, "clu name mismatch (got %s)", obj.Clu)
				return
			}
			if !strings.EqualFold(strings.ToLower(obj.Kind), "light") {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, "unsupported object kind (got %s)", obj.Kind)
				return
			}
			if !strings.EqualFold(strings.ToLower(obj.Id), strings.ToLower(objId)) {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprintf(w, "unsupported object id (got %s)", obj.Id)
				return
			}
		}

		exampleResponse := `[
			{
			  "Id": "DOU0302",
			  "Kind": "Light",
			  "Light": {
				"State": true
			  },
			  "Clu": "CLU_0d1cf087"
			}
		  ]`

		w.Header().Add("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(exampleResponse))
	}))
}

func TestGrentonHelperFunctions(t *testing.T) {
	grenton := GrentonIO{}
	grenton.CluId = 0x0d1cf087

	cluIdString := grenton.getCluString()

	if !strings.EqualFold(cluIdString, "CLU_0d1cf087") {
		t.Errorf("clu id string mismatch, got: %s want: %s", cluIdString, "CLU_0d1cf087")
	}

	type CluObject struct {
		Clu  string
		Id   string
		Kind string
	}

	grenton.outputs = []*GrentonOutput{{id: 304}}

	cluQuery := grenton.getQueryBody()

	res := []CluObject{}
	err := json.Unmarshal(cluQuery, &res)
	if err != nil {
		t.Error(err)
		return
	}

	if len(res) != 1 {
		t.Errorf("result length %d, expected 1", len(res))
	}

	if !strings.EqualFold(res[0].Clu, "CLU_0d1cf087") {
		t.Errorf("cluQuery mismatch clu, got: %s, want: %s", res[0].Clu, "CLU_0d1cf087")
	}

	if !strings.EqualFold(res[0].Id, "DOU0304") {
		t.Errorf("cluQuery mismatch id, got: %s, want: %s", res[0].Id, "DOU0304")
	}

	if !strings.EqualFold(res[0].Kind, "Light") {
		t.Errorf("cluQuery mismatch kind, got: %s, want: %s", res[0].Kind, "Light")
	}

}
func TestGrentonioSetup(t *testing.T) {
	grenton := GrentonIO{}
	grenton.GateAddress = "incorrect address"
	grenton.CluId = 123

	err := grenton.Setup([]uint16{}, []uint16{3, 4})
	if err == nil {
		t.Error("expected error from grenton io setup (incorrect address)")
	}

	grentonMock := mockGrentonIo()
	grenton.GateAddress = grentonMock.URL

	err = grenton.Setup([]uint16{1}, []uint16{3, 4})
	if err == nil {
		t.Error("expected error from grenton io setup (inputs in setup - should be unsupported)")
	}

	err = grenton.Setup([]uint16{}, []uint16{302})
	if err == nil {
		t.Error("expected error from grenton io setup (wrong clu id provided)")
	}

	grenton.CluId = 0x0d1cf087
	err = grenton.Setup([]uint16{}, []uint16{3, 2})
	if err == nil {
		t.Error("expected error from grenton io setup (wrong object id provided)")
	}

	err = grenton.Setup([]uint16{}, []uint16{302})
	if err != nil {
		t.Errorf("received error from grenton io setup: %v", err)
	}

	out, err := grenton.GetOutput(302)
	if err != nil {
		t.Errorf("output not found: %v", err)
		return
	}

	state, err := out.GetState()
	if err != nil {
		t.Errorf("failed to get output state %v", err)
		return
	}

	if !state {
		t.Error("state is not true (should be always true)")
	}

}
