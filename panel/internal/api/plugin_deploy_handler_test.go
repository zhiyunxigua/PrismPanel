package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"PrismPanel/internal/config"
	"PrismPanel/internal/nodes"
	"PrismPanel/internal/store"
)

// newEmptyCatalogTestServer 构造无节点目录的测试 Server：loadCatalog 返回空，
// 使规则部署解析后 targets 为空（模拟所有节点离线/无匹配平台服务器）。
func newEmptyCatalogTestServer(t *testing.T) *Server {
	t.Helper()
	repository, err := store.Open(context.Background(), config.DatabaseConfig{
		Type: "sqlite", SQLitePath: ":memory:", TablePrefix: "prism_",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { repository.Close() })
	nodeService, err := nodes.NewService(repository, nil, bytes.Repeat([]byte{0x5a}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		store: repository, nodes: nodeService,
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// withSuperAdminSession 注入超级管理员会话，使 authorize 校验通过（Can 对超管直接放行）。
func withSuperAdminSession(request *http.Request) *http.Request {
	ctx := context.WithValue(request.Context(), sessionContextKey{}, store.Session{
		User: store.User{ID: "user-test", Username: "tester", DisplayName: "Tester",
			GroupCode: store.GroupSuperAdmin, Status: store.UserActive},
	})
	return request.WithContext(ctx)
}

// TestPluginDeployEmptyTargetErrorDistinguishesRulesAndDirect 验证 P3-3：
// 规则部署解析后无可用目标时报「部署规则未选中任何可用的目标服务器」，
// nodeID 直连路径参数缺失时保留原错误「node_id and server_id are required」。
func TestPluginDeployEmptyTargetErrorDistinguishesRulesAndDirect(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		query      string
		wantPrefix string
	}{
		{
			name:       "rules path reports no selectable target",
			body:       `{"rules":[{"node_id":"node-x","server_id":"lobby","enabled":true}]}`,
			wantPrefix: "部署规则未选中任何可用的目标服务器",
		},
		{
			name:       "direct path keeps original error",
			body:       `{}`,
			query:      "?node_id=node-x",
			wantPrefix: "node_id and server_id are required",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := newEmptyCatalogTestServer(t)
			request := httptest.NewRequest(http.MethodPost,
				"/api/v1/plugins/spigot/essentials/1/deploy"+c.query,
				bytes.NewBufferString(c.body))
			request = withSuperAdminSession(request)
			recorder := httptest.NewRecorder()

			server.handlePluginDeployment(recorder, request, "spigot", "essentials", "1", bundleKindPlugin)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			var response struct {
				Success bool `json:"success"`
				Error   struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Error.Code != "INVALID_REQUEST" {
				t.Fatalf("error code = %q, body = %s", response.Error.Code, recorder.Body.String())
			}
			if !strings.Contains(response.Error.Message, c.wantPrefix) {
				t.Fatalf("error message = %q, want prefix %q", response.Error.Message, c.wantPrefix)
			}
		})
	}
}
