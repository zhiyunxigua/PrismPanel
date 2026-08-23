package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	"PrismPanel/internal/store"
)

type sessionContextKey struct{}
type requestIDContextKey struct{}

func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw := make([]byte, 16)
		if _, err := rand.Read(raw); err != nil {
			writeJSON(writer, http.StatusInternalServerError, response{Success: false, Error: apiError("INTERNAL", "无法生成请求标识")})
			return
		}
		id := hex.EncodeToString(raw)
		writer.Header().Set("X-Request-ID", id)
		ctx := context.WithValue(request.Context(), requestIDContextKey{}, id)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		cookie, err := request.Cookie(s.config.Auth.CookieName)
		if err != nil || cookie.Value == "" {
			writeRequestError(writer, apiError("UNAUTHENTICATED", "请先登录"))
			return
		}
		session, err := s.auth.Authenticate(request.Context(), cookie.Value)
		if err != nil {
			s.clearSessionCookie(writer)
			writeRequestError(writer, publicError(err))
			return
		}
		ctx := context.WithValue(request.Context(), sessionContextKey{}, session)
		next(writer, request.WithContext(ctx))
	}
}

func (s *Server) requireSuperAdmin(next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(writer http.ResponseWriter, request *http.Request) {
		if !currentSession(request).User.IsSuperAdmin() {
			writeRequestError(writer, apiError("FORBIDDEN", "仅超级管理员可以执行此操作"))
			return
		}
		next(writer, request)
	})
}

func (s *Server) requirePermission(permission string, next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(writer http.ResponseWriter, request *http.Request) {
		if err := s.authorize(request, permission); err != nil {
			writeRequestError(writer, err)
			return
		}
		next(writer, request)
	})
}

func (s *Server) authorize(request *http.Request, permission string) error {
	allowed, err := s.store.Can(request.Context(), currentSession(request).User, permission)
	if err != nil {
		s.logger.Error("check permission", "permission", permission, "error", err)
		return err
	}
	if !allowed {
		return apiError("FORBIDDEN", "无权执行此操作")
	}
	return nil
}

func (s *Server) authorizeAny(request *http.Request, permissions ...string) error {
	for _, permission := range permissions {
		allowed, err := s.store.Can(request.Context(), currentSession(request).User, permission)
		if err != nil {
			return err
		}
		if allowed {
			return nil
		}
	}
	return apiError("FORBIDDEN", "无权执行此操作")
}

func currentSession(request *http.Request) store.Session {
	session, _ := request.Context().Value(sessionContextKey{}).(store.Session)
	return session
}

func actor(request *http.Request) string {
	session := currentSession(request)
	if session.User.ID == "" {
		return "anonymous"
	}
	return session.User.Username + " (" + session.User.ID + ")"
}

func requestID(request *http.Request) string {
	value, _ := request.Context().Value(requestIDContextKey{}).(string)
	return value
}

func (s *Server) checkOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet || request.Method == http.MethodHead ||
			request.Method == http.MethodOptions {
			next.ServeHTTP(writer, request)
			return
		}
		origin := request.Header.Get("Origin")
		if origin == "" {
			next.ServeHTTP(writer, request)
			return
		}
		parsed, err := url.Parse(origin)
		if err != nil || !strings.EqualFold(parsed.Host, request.Host) {
			writeRequestError(writer, apiError("FORBIDDEN", "请求来源无效"))
			return
		}
		next.ServeHTTP(writer, request)
	})
}
