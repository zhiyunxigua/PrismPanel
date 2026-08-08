package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleRootRendersEscapedNodeID(t *testing.T) {
	server := &Server{nodeID: "node<&"}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://daemon/", nil)

	server.handleRoot(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("content type = %q", contentType)
	}
	if !strings.Contains(recorder.Body.String(), "node&lt;&amp;") {
		t.Fatalf("root page did not escape node ID: %s", recorder.Body.String())
	}
}

func TestHandleRootRejectsNonRootPath(t *testing.T) {
	server := &Server{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://daemon/healthz", nil)

	server.handleRoot(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotFound)
	}
}
