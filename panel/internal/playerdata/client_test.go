package playerdata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSendUsesBearerAndParsesEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/api/v1/mail/send" {
			t.Fatalf("unexpected request: %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer secret-token" {
			t.Fatalf("unexpected authorization header: %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"success":true,"data":{"mail_id":"mail_test"}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/", "secret-token")
	data, err := client.Send(context.Background(), map[string]string{"subject": "test"})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err := json.Unmarshal(data, &result); err != nil || result["mail_id"] != "mail_test" {
		t.Fatalf("unexpected response: %s, %v", data, err)
	}
}

func TestClientSendMapsRemoteError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"success":false,"error":{"code":"FORBIDDEN","message":"denied"}}`))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, "secret-token").Send(context.Background(), map[string]string{})
	remote, ok := err.(*Error)
	if !ok || remote.StatusCode != http.StatusForbidden || remote.Code != "FORBIDDEN" {
		t.Fatalf("unexpected error: %#v", err)
	}
}
