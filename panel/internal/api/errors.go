package api

import (
	"errors"
	"net/http"

	"PrismPanel/internal/auth"
	"PrismPanel/internal/daemon"
	"PrismPanel/internal/store"
)

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *APIError) Error() string {
	return e.Code + ": " + e.Message
}

func apiError(code, message string) *APIError {
	return &APIError{Code: code, Message: message}
}

func writeRequestError(writer http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var panelError *APIError
	var daemonError *daemon.APIError
	switch {
	case errors.As(err, &panelError):
		switch panelError.Code {
		case "UNAUTHENTICATED":
			status = http.StatusUnauthorized
		case "FORBIDDEN":
			status = http.StatusForbidden
		case "NOT_FOUND":
			status = http.StatusNotFound
		case "CONFLICT", "LAST_SUPER_ADMIN", "PROTECTED", "RESOURCE_IN_USE":
			status = http.StatusConflict
		case "RATE_LIMITED":
			status = http.StatusTooManyRequests
		case "INTERNAL":
			status = http.StatusInternalServerError
		}
	case errors.Is(err, daemon.ErrDisconnected):
		status = http.StatusServiceUnavailable
		panelError = apiError("DAEMON_UNAVAILABLE", "守护进程当前不可用")
	case errors.As(err, &daemonError):
		switch daemonError.Code {
		case "SERVER_NOT_FOUND", "INSTANCE_NOT_FOUND":
			status = http.StatusNotFound
		case "INSTANCE_BUSY", "PORT_CONFLICT", "SERVER_ID_CONFLICT", "DEPLOYMENT_ALREADY_RUNNING":
			status = http.StatusConflict
		case "INTERNAL", "CONFIG_WRITE_FAILED":
			status = http.StatusInternalServerError
		}
		writeJSON(writer, status, response{Success: false, Error: daemonError})
		return
	default:
		status = http.StatusInternalServerError
		panelError = apiError("INTERNAL", "面板内部错误")
	}
	writeJSON(writer, status, response{Success: false, Error: panelError})
}

func publicError(err error) error {
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		return apiError("INVALID_CREDENTIALS", "用户名或密码错误")
	case errors.Is(err, auth.ErrLoginLimited):
		return apiError("RATE_LIMITED", "登录尝试过于频繁，请稍后再试")
	case errors.Is(err, auth.ErrUnauthenticated):
		return apiError("UNAUTHENTICATED", "登录状态已失效")
	case errors.Is(err, auth.ErrForbidden):
		return apiError("FORBIDDEN", "无权执行此操作")
	case errors.Is(err, auth.ErrInvalidInput):
		return apiError("INVALID_REQUEST", err.Error())
	case errors.Is(err, store.ErrNotFound):
		return apiError("NOT_FOUND", "目标不存在")
	case errors.Is(err, store.ErrConflict):
		return apiError("CONFLICT", "目标已存在或与现有配置冲突")
	case errors.Is(err, store.ErrLastSuperAdmin):
		return apiError("LAST_SUPER_ADMIN", "系统必须保留至少一个可登录的超级管理员")
	case errors.Is(err, store.ErrProtected):
		return apiError("PROTECTED", "该内置资源不允许执行此操作")
	case errors.Is(err, store.ErrInUse):
		return apiError("RESOURCE_IN_USE", "该用户组仍有用户，不能删除")
	default:
		return err
	}
}
