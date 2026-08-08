package game

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDeleteSignedUsesDeleteAndAcceptsEmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodDelete {
			t.Errorf("method mismatch: %s", request.Method)
		}
		if request.Header.Get("user-id") == "" || request.Header.Get("user-token") == "" {
			t.Error("signed headers are missing")
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client, err := NewClient(AccountState{UserID: "123", UserToken: "token"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.deleteSigned(context.Background(), server.URL, "/game-character", map[string]any{"name": "Steve"})
	if err != nil {
		t.Fatal(err)
	}
	if intValue(response["code"]) != 0 {
		t.Fatalf("empty successful response was not normalized: %+v", response)
	}
}
