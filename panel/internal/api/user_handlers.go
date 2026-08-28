package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"PrismPanel/internal/auth"
	"PrismPanel/internal/store"
)

func (s *Server) handleUsers(writer http.ResponseWriter, request *http.Request) {
	switch request.Method {
	case http.MethodGet:
		if err := s.authorizeAny(request, "user.view", "server.configure"); err != nil {
			writeRequestError(writer, err)
			return
		}
		s.listUsers(writer, request)
	case http.MethodPost:
		if err := s.authorize(request, "user.create"); err != nil {
			writeRequestError(writer, err)
			return
		}
		s.createUser(writer, request)
	default:
		methodNotAllowed(writer, "GET, POST")
	}
}

func (s *Server) listUsers(writer http.ResponseWriter, request *http.Request) {
	page, pageSize := queryInt(request, "page", 1), queryInt(request, "page_size", 20)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	status := request.URL.Query().Get("status")
	if status != "" && !auth.ValidStatus(status) {
		writeRequestError(writer, apiError("INVALID_REQUEST", "用户状态筛选无效"))
		return
	}
	result, err := s.store.ListUsers(request.Context(), store.UserFilter{
		Search:   strings.TrimSpace(request.URL.Query().Get("search")),
		Status:   status,
		Page:     page,
		PageSize: pageSize,
	})
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	for index := range result.Items {
		result.Items[index], err = s.store.DecorateUser(request.Context(), result.Items[index])
		if err != nil {
			writeRequestError(writer, err)
			return
		}
	}
	writeSuccess(writer, result)
}

func (s *Server) createUser(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		GroupCode   string `json:"group_code"`
		Password    string `json:"password"`
	}
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	if err == nil {
		err = s.authorizeGroupAssignment(request, input.GroupCode)
	}
	var created store.User
	if err == nil {
		created, err = s.auth.CreateUser(
			request.Context(), input.Username, input.DisplayName, input.GroupCode, input.Password,
		)
	}
	err = publicError(err)
	s.record(request, "user.create", created.ID, map[string]any{
		"username": input.Username, "display_name": input.DisplayName, "group_code": input.GroupCode,
	}, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	created, err = s.store.DecorateUser(request.Context(), created)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeJSON(writer, http.StatusCreated, response{Success: true, Data: created})
}

func (s *Server) handleUser(writer http.ResponseWriter, request *http.Request) {
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/users/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) < 1 || len(parts) > 3 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	userID := parts[0]
	if len(parts) == 3 && parts[1] == "api-key" && parts[2] == "rotate" {
		s.handleUserAPIKey(writer, request, userID, true)
		return
	}
	if len(parts) == 3 {
		http.NotFound(writer, request)
		return
	}
	if len(parts) == 2 {
		if parts[1] == "api-key" {
			s.handleUserAPIKey(writer, request, userID, false)
			return
		}
		s.handleUserAction(writer, request, userID, parts[1])
		return
	}
	switch request.Method {
	case http.MethodGet:
		if err := s.authorize(request, "user.view"); err != nil {
			writeRequestError(writer, err)
			return
		}
		user, err := s.store.GetUser(request.Context(), userID)
		if err == nil {
			user, err = s.store.DecorateUser(request.Context(), user)
		}
		if err != nil {
			writeRequestError(writer, publicError(err))
			return
		}
		writeSuccess(writer, user)
	case http.MethodPut:
		if err := s.authorize(request, "user.update"); err != nil {
			writeRequestError(writer, err)
			return
		}
		s.updateUser(writer, request, userID)
	case http.MethodDelete:
		if err := s.authorize(request, "user.delete"); err != nil {
			writeRequestError(writer, err)
			return
		}
		s.deleteUser(writer, request, userID)
	default:
		methodNotAllowed(writer, "GET, PUT, DELETE")
	}
}

func (s *Server) handleUserAPIKey(writer http.ResponseWriter, request *http.Request, userID string, rotate bool) {
	if err := s.authorizeAPIKeyManagement(request, userID); err != nil {
		writeRequestError(writer, err)
		return
	}
	switch request.Method {
	case http.MethodGet:
		if rotate {
			methodNotAllowed(writer, "POST")
			return
		}
		key, err := s.store.GetAPIKey(request.Context(), userID)
		if err != nil {
			writeRequestError(writer, publicError(err))
			return
		}
		writeSuccess(writer, key)
	case http.MethodPost:
		var key store.APIKey
		var token string
		var err error
		if rotate {
			key, token, err = s.auth.RotateAPIKey(request.Context(), userID)
		} else {
			key, token, err = s.auth.CreateAPIKey(request.Context(), userID)
		}
		action := "api_key.create"
		if rotate {
			action = "api_key.rotate"
		}
		s.record(request, action, userID, nil, publicError(err))
		if err != nil {
			writeRequestError(writer, publicError(err))
			return
		}
		writeJSON(writer, http.StatusCreated, response{Success: true, Data: map[string]any{
			"key": key, "api_key": token,
		}})
	case http.MethodDelete:
		if rotate {
			methodNotAllowed(writer, "POST")
			return
		}
		err := publicError(s.auth.RevokeAPIKey(request.Context(), userID))
		s.record(request, "api_key.revoke", userID, nil, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{})
	default:
		methodNotAllowed(writer, "GET, POST, DELETE")
	}
}

func (s *Server) authorizeAPIKeyManagement(request *http.Request, userID string) error {
	actor := currentSession(request).User
	if !actor.IsSuperAdmin() && actor.GroupCode != store.GroupAdmin {
		return apiError("FORBIDDEN", "仅超级管理员和管理员可以管理 API Key")
	}
	_, err := s.manageableUser(request, userID)
	return err
}

func (s *Server) updateUser(writer http.ResponseWriter, request *http.Request, userID string) {
	var input struct {
		DisplayName string `json:"display_name"`
		GroupCode   string `json:"group_code"`
		Status      string `json:"status"`
	}
	body, err := readBody(request)
	if err == nil {
		err = json.Unmarshal(body, &input)
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.GroupCode = strings.TrimSpace(input.GroupCode)
	if err == nil && (input.DisplayName == "" || len([]rune(input.DisplayName)) > 100) {
		err = apiError("INVALID_REQUEST", "显示名称不能为空且不能超过 100 个字符")
	}
	if err == nil && !auth.ValidStatus(input.Status) {
		err = apiError("INVALID_REQUEST", "用户状态无效")
	}
	var current store.User
	if err == nil {
		current, err = s.manageableUser(request, userID)
	}
	if err == nil && current.GroupCode != input.GroupCode {
		err = s.authorizeGroupAssignment(request, input.GroupCode)
	}
	if err == nil && current.ID == currentSession(request).User.ID && input.Status != store.UserActive {
		err = apiError("FORBIDDEN", "不能禁用当前登录用户")
	}
	var updated store.User
	if err == nil {
		updated, err = s.store.UpdateUser(request.Context(), userID, store.UserChanges{
			DisplayName: input.DisplayName, GroupCode: input.GroupCode, Status: input.Status,
		})
	}
	err = publicError(err)
	s.record(request, "user.update", userID, input, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	updated, err = s.store.DecorateUser(request.Context(), updated)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, updated)
}

func (s *Server) deleteUser(writer http.ResponseWriter, request *http.Request, userID string) {
	var err error
	if userID == currentSession(request).User.ID {
		err = apiError("FORBIDDEN", "不能删除当前登录用户")
	} else {
		_, err = s.manageableUser(request, userID)
	}
	if err == nil {
		err = s.store.DeleteUser(request.Context(), userID)
	}
	err = publicError(err)
	s.record(request, "user.delete", userID, nil, err)
	if err != nil {
		writeRequestError(writer, err)
		return
	}
	writeSuccess(writer, map[string]any{})
}

func (s *Server) handleUserAction(
	writer http.ResponseWriter,
	request *http.Request,
	userID string,
	action string,
) {
	if action == "permissions" {
		s.handleUserPermissions(writer, request, userID)
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, "POST")
		return
	}
	permission := map[string]string{
		"reset-password":  "user.password.reset",
		"revoke-sessions": "user.sessions.revoke",
	}[action]
	if permission == "" {
		http.NotFound(writer, request)
		return
	}
	if err := s.authorize(request, permission); err != nil {
		writeRequestError(writer, err)
		return
	}
	if _, err := s.manageableUser(request, userID); err != nil {
		writeRequestError(writer, err)
		return
	}
	switch action {
	case "reset-password":
		var input struct {
			Password string `json:"password"`
		}
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &input)
		}
		if err == nil {
			err = s.auth.ResetPassword(request.Context(), userID, input.Password)
		}
		err = publicError(err)
		s.record(request, "user.password.reset", userID, nil, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{})
	case "revoke-sessions":
		err := publicError(s.store.RevokeUserSessions(request.Context(), userID))
		s.record(request, "user.sessions.revoke", userID, nil, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, map[string]any{})
	}
}

func (s *Server) handleUserPermissions(writer http.ResponseWriter, request *http.Request, userID string) {
	if !currentSession(request).User.IsSuperAdmin() {
		writeRequestError(writer, apiError("FORBIDDEN", "仅超级管理员可以修改个人权限"))
		return
	}
	switch request.Method {
	case http.MethodGet:
		profile, err := s.store.UserPermissionProfile(request.Context(), userID)
		if err != nil {
			writeRequestError(writer, publicError(err))
			return
		}
		writeSuccess(writer, profile)
	case http.MethodPut:
		var input struct {
			Permissions []string `json:"permissions"`
		}
		body, err := readBody(request)
		if err == nil {
			err = json.Unmarshal(body, &input)
		}
		var profile store.UserPermissionProfile
		if err == nil {
			profile, err = s.store.SetUserPermissions(request.Context(), userID, input.Permissions)
		}
		err = publicError(err)
		s.record(request, "user.permission.update", userID, input, err)
		if err != nil {
			writeRequestError(writer, err)
			return
		}
		writeSuccess(writer, profile)
	default:
		methodNotAllowed(writer, "GET, PUT")
	}
}

func (s *Server) manageableUser(request *http.Request, userID string) (store.User, error) {
	target, err := s.store.GetUser(request.Context(), userID)
	if err != nil {
		return store.User{}, publicError(err)
	}
	target, err = s.store.DecorateUser(request.Context(), target)
	if err != nil {
		return store.User{}, err
	}
	actor := currentSession(request).User
	if actor.IsSuperAdmin() {
		return target, nil
	}
	if target.IsSuperAdmin() || !permissionSetContains(actor.Permissions, target.Permissions) {
		return store.User{}, apiError("FORBIDDEN", "不能管理权限高于当前用户的账户")
	}
	return target, nil
}

func (s *Server) authorizeGroupAssignment(request *http.Request, groupCode string) error {
	group, err := s.store.GetUserGroup(request.Context(), strings.TrimSpace(groupCode))
	if err != nil {
		return publicError(err)
	}
	actor := currentSession(request).User
	if actor.IsSuperAdmin() {
		return nil
	}
	if group.Code == store.GroupSuperAdmin ||
		!permissionSetContains(actor.Permissions, group.Permissions) {
		return apiError("FORBIDDEN", "不能分配权限高于当前用户的用户组")
	}
	return nil
}

func permissionSetContains(available, required []string) bool {
	set := make(map[string]struct{}, len(available))
	for _, permission := range available {
		if permission == "*" {
			return true
		}
		set[permission] = struct{}{}
	}
	for _, permission := range required {
		if _, exists := set[permission]; !exists {
			return false
		}
	}
	return true
}

func queryInt(request *http.Request, name string, fallback int) int {
	value, err := strconv.Atoi(request.URL.Query().Get(name))
	if err != nil {
		return fallback
	}
	return value
}
