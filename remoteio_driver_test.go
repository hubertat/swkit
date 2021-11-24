package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
)

func makeTestRemoteServer(config struct {
	Inputs  []uint8
	Outputs []uint8
}) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("remoteio-token") == "==this-token-should-be-valid==" {

			confJson, err := json.Marshal(config)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.Header().Add("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write(confJson)
		} else {
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
}
func TestRemoteIoSetup(t *testing.T) {
	badRequestServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	remoteBad := RemoteIO{
		Host:  badRequestServer.URL,
		Token: "not important now",
	}

	ins := []uint8{1, 3}
	ous := []uint8{}

	err := remoteBad.Setup(ins, ous)
	log.Printf("error expected: %v\n", err)
	if err == nil {
		t.Error("error expected, got nil")
	}

	validToken := "==this-token-should-be-valid=="
	config := struct {
		Inputs  []uint8
		Outputs []uint8
	}{
		Inputs:  []uint8{1, 3},
		Outputs: []uint8{5},
	}
	okServer := makeTestRemoteServer(config)
	remote := RemoteIO{
		Host:  okServer.URL,
		Token: validToken,
	}

	err = remote.Setup(ins, ous)
	if err != nil {
		t.Errorf("received error: %v", err)
	}

	emptyServer := makeTestRemoteServer(struct {
		Inputs  []uint8
		Outputs []uint8
	}{})
	remote.Host = emptyServer.URL
	err = remote.Setup(ins, ous)
	log.Printf("error expected: %v\n", err)
	if err == nil {
		t.Error("expected error on response from empty remoteio server")
	}

	remote.Token = "not valid"
	err = remote.Setup(ins, ous)
	log.Printf("error expected: %v\n", err)
	if err == nil {
		t.Error("error expected, got nil")
	}
}

func TestRemoteIoName