package api

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"PrismPanel/internal/netgames"
	"PrismPanel/internal/store"
)

func TestSplitMCServerKeys(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		{"  ", []string{}},
		{"a.example.com:25565", []string{"a.example.com:25565"}},
		{"a:1, b:2 ,c:3", []string{"a:1", "b:2", "c:3"}},
		{",,x:1,,", []string{"x:1"}},
	}
	for _, c := range cases {
		got := splitMCServerKeys(c.in)
		if len(got) != len(c.want) {
			t.Fatalf("splitMCServerKeys(%q) = %v, want %v", c.in, got, c.want)
		}
		for index := range got {
			if got[index] != c.want[index] {
				t.Fatalf("splitMCServerKeys(%q) = %v, want %v", c.in, got, c.want)
			}
		}
	}
}

func TestParseMCServerInput(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		wantHost  string
		wantPort  uint16
		wantError bool
	}{
		{"address 完整", `{"name":"s","address":"play.example.com:19132"}`, "play.example.com:19132", 0, false},
		{"address 默认端口", `{"name":"s","address":"play.example.com"}`, "play.example.com", 0, false},
		{"host+port 分开", `{"name":"s","host":"mc.example.com","port":25566}`, "mc.example.com", 25566, false},
		{"address 优先于 host", `{"name":"s","address":"a.example.com:1","host":"b.example.com","port":2}`, "a.example.com:1", 0, false},
		{"空地址", `{"name":"s"}`, "", 0, true},
		{"非法端口", `{"name":"s","host":"x","port":0}`, "", 0, true},
		{"非法 JSON", `{`, "", 0, true},
	}
	for _, c := range cases {
		request := httptest.NewRequest("POST", "/api/v1/net-games/mc-servers", strings.NewReader(c.body))
		input, err := parseMCServerInput(request)
		if c.wantError {
			if err == nil {
				t.Fatalf("%s: expected error", c.name)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if input.Host != c.wantHost {
			t.Fatalf("%s: host = %q, want %q", c.name, input.Host, c.wantHost)
		}
		// host+port 分开形式才会直接填 Port；address 形式由服务层解析端口
		if input.Port != c.wantPort {
			t.Fatalf("%s: port = %d, want %d", c.name, input.Port, c.wantPort)
		}
	}
}

func TestMCPublicError(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
	}{
		{"nil", nil, ""},
		{"not found", store.ErrNotFound, "NOT_FOUND"},
		{"conflict", store.ErrConflict, "CONFLICT"},
		{"collector busy", netgames.ErrMCCollectorBusy, "CONFLICT"},
		{"validation", errors.New("服务器地址不能为空"), "INVALID_REQUEST"},
	}
	for _, c := range cases {
		result := mcPublicError(c.err)
		if c.wantCode == "" {
			if result != nil {
				t.Fatalf("%s: expected nil, got %v", c.name, result)
			}
			continue
		}
		if result == nil {
			t.Fatalf("%s: expected error", c.name)
		}
		var apiErr *APIError
		if !errors.As(result, &apiErr) || apiErr.Code != c.wantCode {
			t.Fatalf("%s: got %v (code %v), want code %s", c.name, result, apiErr, c.wantCode)
		}
	}
}
