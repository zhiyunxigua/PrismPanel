package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"PrismPanel/internal/store"
)

type userGroupRequest struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

func (s *Server) handlePermissions(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	writeSuccess(writer, map[string]any{"items": store.PermissionCatalog()})
}

func (s *Server) handleUserGroups(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if err := s.authorizeAny(request, "user.view", "user.create", "user.update"); err != nil {
			writeRequestError(writer, err)
			return
		}
		items, err := s.store.ListUserGroups(request.Context())
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{"items": items})
	case http.MethodPost:
		if !currentSession(request).User.IsSuperAdmin() {
			writeRequestError(writer, apiError("FORBIDDEN", "仅超级管理员可以创建用户组"))
			return
		}
		input, err := readUserGroupInput(request)
		var created store.UserGroup
		if err == nil {
			created, err = s.store.CreateUserGroup(
				request.Context(), input.Name, input.Description, input.Permissions,
			)
		}
		err = publicError(err)
		s.record(request, "user_group.create", created.Code, input, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeJSON(writer, http.StatusCreated, response{Success: true, Data: created})
	default:
		methodNotAllowed(writer, "GET, POST")
	}
}

func (s *Server) handleUserGroup(writer http.ResponseWriter, request *http.Request) {
	code := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/user-groups/"), "/")
	if code == "" || strings.Contains(code, "/") {
		http.NotFound(writer, request)
		return
	}
	switch request.Method {
	case http.MethodGet:
		if err := s.authorizeAny(request, "user.view", "user.create", "user.update"); err != nil {
			writeRequestError(writer, err)
			return
		}
		group, err := s.store.GetUserGroup(request.Context(), code)
		if err != nil {
			writeRequestError(writer, publicError(err))
			return
		}
		writeSuccess(writer, group)
	case http.MethodPut:
		if !currentSession(request).User.IsSuperAdmin() {
			writeRequestError(writer, apiError("FORBIDDEN", "仅超级管理员可以修改用户组"))
			return
		}
		input, err := readUserGroupInput(request)
		var updated store.UserGroup
		if err == nil {
			updated, err = s.store.UpdateUserGroup(
				request.Context(), code, input.Name, input.Description, input.Permissions,
			)
		}
		err = publicError(err)
		s.record(request, "user_group.update", code, input, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, updated)
	case http.MethodDelete:
		if !currentSession(request).User.IsSuperAdmin() {
			writeRequestError(writer, apiError("FORBIDDEN", "仅超级管理员可以删除用户组"))
			return
		}
		err := publicError(s.store.DeleteUserGroup(request.Context(), code))
		s.record(request, "user_group.delete", code, nil, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{})
	default:
		methodNotAllowed(writer, "GET, PUT, DELETE")
	}
}

func readUserGroupInput(request *http.Request) (userGroupRequest, error) {
	var input userGroupRequest
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if err != nil || input.Name == "" || len([]rune(input.Name)) > 100 ||
		len([]rune(input.Description)) > 500 {
		return input, apiError("INVALID_REQUEST", "用户组名称或描述格式无效")
	}
	seen := make(map[string]struct{}, len(input.Permissions))
	for _, permission := range input.Permissions {
		if !store.ValidPermission(permission) {
			return input, apiError("INVALID_REQUEST", "包含无效的权限节点")
		}
		seen[permission] = struct{}{}
	}
	input.Permissions = input.Permissions[:0]
	for _, permission := range store.PermissionCatalog() {
		if _, exists := seen[permission.Code]; exists {
			input.Permissions = append(input.Permissions, permission.Code)
		}
	}
	return input, nil
}

func (s *Server) handleAudit(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, "GET")
		return
	}
	page, pageSize := queryInt(request, "page", 1), queryInt(request, "page_size", 20)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	var success *bool
	if value := request.URL.Query().Get("success"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			writeRequestError(writer, apiError("INVALID_REQUEST", "操作结果筛选无效"))
			return
		}
		success = &parsed
	}
	result, err := s.store.ListAudit(
		request.Context(),
		strings.TrimSpace(request.URL.Query().Get("search")),
		strings.TrimSpace(request.URL.Query().Get("action")),
		success,
		page,
		pageSize,
	)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, result)
}
