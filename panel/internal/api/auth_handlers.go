package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"

	"PrismPanel/internal/store"
)

func (s *Server) handleAuthStatus(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	initialized, err := s.store.HasUsers(request.Context())
	if err != nil {
		s.logger.Error("read initialization status", "error", err)
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, map[string]bool{"initialized": initialized})
}

func (s *Server) handleLogin(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	if err != nil {
		writeRequestError(writer, apiError("INVALID_REQUEST", "用户名和密码格式无效"))
		return
	}
	user, token, initialized, err := s.auth.LoginOrInitialize(
		request.Context(), input.Username, input.Password, clientIP(request), request.UserAgent(),
	)
	if err != nil {
		s.writeAudit(store.AuditLog{
			RequestID: requestID(request), ActorUsername: "anonymous", ActorDisplayName: "匿名用户",
			SourceIP: clientIP(request), UserAgent: request.UserAgent(), Action: "auth.login",
			ResourceType: "session", ResourceName: strings.ToLower(strings.TrimSpace(input.Username)),
			RiskLevel: "normal", Success: false, ErrorCode: errorCode(publicError(err)),
		})
		writeRequestError(writer, publicError(err))
		return
	}
	s.setSessionCookie(writer, token)
	if initialized {
		s.writeAudit(store.AuditLog{
			RequestID: requestID(request), ActorUserID: user.ID, ActorUsername: user.Username,
			ActorDisplayName: user.DisplayName, SourceIP: clientIP(request), UserAgent: request.UserAgent(),
			Action: "system.initialize", ResourceType: "user", ResourceID: user.ID,
			ResourceName: user.Username, RiskLevel: "critical", Success: true,
		})
	}
	s.writeAudit(store.AuditLog{
		RequestID: requestID(request), ActorUserID: user.ID, ActorUsername: user.Username,
		ActorDisplayName: user.DisplayName, SourceIP: clientIP(request), UserAgent: request.UserAgent(),
		Action: "auth.login", ResourceType: "session", ResourceID: user.ID,
		ResourceName: user.Username, RiskLevel: "normal", Success: true,
	})
	writeSuccess(writer, map[string]any{"user": user, "initialized": initialized})
}

func (s *Server) handleSession(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	session := currentSession(request)
	writeSuccess(writer, map[string]any{
		"user": session.User, "expires_at": session.ExpiresAt,
		"idle_expires_at": session.IdleExpiresAt,
	})
}

func (s *Server) handleLogout(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	cookie, _ := request.Cookie(s.config.Auth.CookieName)
	if cookie != nil {
		if err := s.auth.Logout(request.Context(), cookie.Value); err != nil {
			s.logger.Error("revoke session", "error", err)
		}
	}
	s.clearSessionCookie(writer)
	s.record(request, "auth.logout", currentSession(request).User.ID, nil, nil)
	writeSuccess(writer, map[string]any{})
}

func (s *Server) handlePassword(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPut {
		methodNotAllowed(writer, "PUT")
		return
	}
	var input struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	if err == nil {
		err = s.auth.ChangePassword(
			request.Context(), currentSession(request), input.CurrentPassword, input.NewPassword,
		)
	}
	s.record(request, "auth.password.change", currentSession(request).User.ID, nil, publicError(err))
	if err != nil {
		writeRequestError(writer, publicError(err))
		return
	}
	writeSuccess(writer, map[string]any{})
}

func (s *Server) setSessionCookie(writer http.ResponseWriter, token string) {
	lifetime, _ := s.config.SessionLifetime()
	http.SetCookie(writer, &http.Cookie{
		Name: s.config.Auth.CookieName, Value: token, Path: "/",
		HttpOnly: true, Secure: s.config.Auth.CookieSecure, SameSite: http.SameSiteLaxMode,
		MaxAge: int(lifetime.Seconds()),
	})
}

func (s *Server) clearSessionCookie(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{
		Name: s.config.Auth.CookieName, Value: "", Path: "/",
		HttpOnly: true, Secure: s.config.Auth.CookieSecure, SameSite: http.SameSiteLaxMode,
		MaxAge: -1,
	})
}

func clientIP(request *http.Request) string {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err == nil {
		return host
	}
	return request.RemoteAddr
}

func methodNotAllowed(writer http.ResponseWriter, allow string) {
	writer.Header().Set("Allow", allow)
	writeJSON(writer, http.StatusMethodNotAllowed, response{
		Success: false, Error: apiError("METHOD_NOT_ALLOWED", "请求方法不受支持"),
	})
}

func errorCode(err error) string {
	if err == nil {
		return ""
	}
	var panelError *APIError
	if errors.As(err, &panelError) {
		return panelError.Code
	}
	return "INTERNAL"
}

func (s *Server) writeAudit(entry store.AuditLog) {
	if err := s.store.CreateAudit(context.Background(), entry); err != nil {
		s.logger.Error("write audit log", "error", err)
	}
}
