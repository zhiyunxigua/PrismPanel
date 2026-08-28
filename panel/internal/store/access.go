package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
)

var permissionDefinitions = []PermissionDefinition{
	{Code: "dashboard.view", Name: "查看总览", Category: "总览"},
	{Code: "node.view", Name: "查看节点", Category: "节点"},
	{Code: "node.create", Name: "添加节点", Category: "节点"},
	{Code: "node.update", Name: "编辑节点", Category: "节点"},
	{Code: "node.delete", Name: "删除节点", Category: "节点"},
	{Code: "node.terminal", Name: "使用节点终端", Category: "节点"},
	{Code: "node.terminal.root", Name: "使用 root 节点终端", Category: "节点"},
	{Code: "server.view", Name: "查看服务器", Category: "服务器"},
	{Code: "server.create", Name: "创建服务器", Category: "服务器"},
	{Code: "server.configure", Name: "配置服务器", Category: "服务器"},
	{Code: "server.deploy", Name: "部署镜像服务器组", Category: "服务器"},
	{Code: "server.delete", Name: "删除服务器", Category: "服务器"},
	{Code: "instance.start", Name: "启动实例", Category: "实例"},
	{Code: "instance.stop", Name: "停止实例", Category: "实例"},
	{Code: "instance.restart", Name: "重启实例", Category: "实例"},
	{Code: "instance.kill", Name: "强制停止实例", Category: "实例"},
	{Code: "console.read", Name: "查看控制台", Category: "控制台"},
	{Code: "console.command", Name: "执行控制台命令", Category: "控制台"},
	{Code: "file.read", Name: "查看文件", Category: "文件"},
	{Code: "file.write", Name: "上传和编辑文件", Category: "文件"},
	{Code: "file.delete", Name: "删除文件", Category: "文件"},
	{Code: "player.view", Name: "查看玩家", Category: "玩家"},
	{Code: "player.kick", Name: "踢出玩家", Category: "玩家"},
	{Code: "player.message", Name: "发送管理消息", Category: "玩家"},
	{Code: "player.transfer", Name: "跨服转移玩家", Category: "玩家"},
	{Code: "player.whitelist.manage", Name: "管理玩家白名单", Category: "玩家"},
	{Code: "player.op.manage", Name: "全服 OP", Category: "玩家"},
	{Code: "mail.send", Name: "发送邮件", Category: "邮件"},
	{Code: "plugin.view", Name: "查看插件", Category: "插件"},
	{Code: "plugin.upload", Name: "上传插件", Category: "插件"},
	{Code: "plugin.deploy", Name: "部署和回滚插件", Category: "插件"},
	{Code: "plugin.remove", Name: "移除插件", Category: "插件"},
	{Code: "firewall.view", Name: "查看网络白名单", Category: "网络"},
	{Code: "firewall.manage", Name: "管理网络白名单", Category: "网络"},
	{Code: "task.view", Name: "查看任务", Category: "任务"},
	{Code: "task.cancel", Name: "取消任务", Category: "任务"},
	{Code: "task.retry", Name: "重试任务", Category: "任务"},
	{Code: "schedule.view", Name: "查看定时任务", Category: "定时任务"},
	{Code: "schedule.manage", Name: "管理定时任务", Category: "定时任务"},
	{Code: "alert.view", Name: "查看告警", Category: "治理"},
	{Code: "alert.acknowledge", Name: "确认告警", Category: "治理"},
	{Code: "audit.view", Name: "查看操作日志", Category: "治理"},
	{Code: "user.view", Name: "查看用户", Category: "用户"},
	{Code: "user.create", Name: "创建用户", Category: "用户"},
	{Code: "user.update", Name: "编辑和禁用用户", Category: "用户"},
	{Code: "user.delete", Name: "删除用户", Category: "用户"},
	{Code: "user.password.reset", Name: "重置用户密码", Category: "用户"},
	{Code: "user.sessions.revoke", Name: "强制用户下线", Category: "用户"},
	{Code: "permission.manage", Name: "管理用户组和个人权限", Category: "系统"},
	{Code: "system.settings", Name: "修改系统设置", Category: "系统"},
}

var instanceAdminPermissions = map[string]struct{}{
	"instance.start": {}, "instance.stop": {}, "instance.restart": {}, "instance.kill": {},
	"console.read": {}, "console.command": {},
	"file.read": {}, "file.write": {}, "file.delete": {},
	"player.view": {}, "player.kick": {}, "player.message": {}, "player.whitelist.manage": {}, "player.op.manage": {},
	"plugin.view": {}, "plugin.upload": {}, "plugin.deploy": {}, "plugin.remove": {},
}

func InstanceAdminPermissions() []string {
	permissions := make([]string, 0, len(instanceAdminPermissions))
	for permission := range instanceAdminPermissions {
		permissions = append(permissions, permission)
	}
	sort.Strings(permissions)
	return permissions
}

func newID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func PermissionCatalog() []PermissionDefinition {
	result := make([]PermissionDefinition, len(permissionDefinitions))
	copy(result, permissionDefinitions)
	return result
}

func permissionCodes() []string {
	result := make([]string, 0, len(permissionDefinitions))
	for _, item := range permissionDefinitions {
		result = append(result, item.Code)
	}
	return result
}

func ValidPermission(code string) bool {
	for _, item := range permissionDefinitions {
		if item.Code == code {
			return true
		}
	}
	return false
}

func (s *Store) DecorateUser(ctx context.Context, user User) (User, error) {
	group, err := s.GetUserGroup(ctx, user.GroupCode)
	if err != nil {
		return User{}, err
	}
	permissions, err := s.EffectivePermissions(ctx, user.ID, user.GroupCode)
	if err != nil {
		return User{}, err
	}
	var overrideCount, activeSessions int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_permission_overrides WHERE user_id = ?", user.ID).Scan(&overrideCount); err != nil {
		return User{}, err
	}
	now := time.Now().UTC()
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sessions
		WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ? AND idle_expires_at > ?`,
		user.ID, now, now).Scan(&activeSessions); err != nil {
		return User{}, err
	}
	user.Group = UserGroupSummary{Code: group.Code, Name: group.Name}
	user.Permissions = permissions
	user.HasOverrides = overrideCount > 0
	user.ActiveSessions = activeSessions
	return user, nil
}

func (s *Store) EffectivePermissions(ctx context.Context, userID, groupCode string) ([]string, error) {
	if groupCode == GroupSuperAdmin {
		return []string{"*"}, nil
	}
	permissions, err := s.groupPermissionSet(ctx, groupCode)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT permission_code, allowed
		FROM user_permission_overrides WHERE user_id = ?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var code string
		var allowed bool
		if err := rows.Scan(&code, &allowed); err != nil {
			return nil, err
		}
		if allowed {
			permissions[code] = struct{}{}
		} else {
			delete(permissions, code)
		}
	}
	result := make([]string, 0, len(permissions))
	for code := range permissions {
		result = append(result, code)
	}
	sort.Strings(result)
	return result, rows.Err()
}

func (s *Store) Can(ctx context.Context, user User, permission string) (bool, error) {
	if user.GroupCode == GroupSuperAdmin {
		return true, nil
	}
	var allowed bool
	err := s.db.QueryRowContext(ctx, `SELECT allowed FROM user_permission_overrides
		WHERE user_id = ? AND permission_code = ?`, user.ID, permission).Scan(&allowed)
	if err == nil {
		return allowed, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var count int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM group_permissions
		WHERE group_code = ? AND permission_code = ?`, user.GroupCode, permission).Scan(&count)
	return count > 0, err
}

func (s *Store) CanInstance(ctx context.Context, user User, permission, nodeID, instanceID string) (bool, error) {
	allowed, err := s.Can(ctx, user, permission)
	if err != nil || allowed {
		return allowed, err
	}
	if _, scoped := instanceAdminPermissions[permission]; !scoped {
		return false, nil
	}
	return s.IsInstanceAdmin(ctx, nodeID, instanceID, user.ID)
}

func (s *Store) ListUserGroups(ctx context.Context) ([]UserGroup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT r.code, r.name, r.description, r.built_in,
		r.created_at, r.updated_at, COUNT(u.id)
		FROM user_groups r LEFT JOIN users u ON u.group_code = r.code AND u.deleted_at IS NULL
		GROUP BY r.code, r.name, r.description, r.built_in, r.created_at, r.updated_at
		ORDER BY r.sort_order, r.created_at, r.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	groups := make([]UserGroup, 0)
	for rows.Next() {
		var group UserGroup
		if err := rows.Scan(&group.Code, &group.Name, &group.Description, &group.BuiltIn,
			&group.CreatedAt, &group.UpdatedAt, &group.UserCount); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range groups {
		if groups[index].Code == GroupSuperAdmin {
			groups[index].Permissions = permissionCodes()
			continue
		}
		set, err := s.groupPermissionSet(ctx, groups[index].Code)
		if err != nil {
			return nil, err
		}
		for code := range set {
			groups[index].Permissions = append(groups[index].Permissions, code)
		}
		sort.Strings(groups[index].Permissions)
	}
	return groups, rows.Err()
}

func (s *Store) GetUserGroup(ctx context.Context, code string) (UserGroup, error) {
	var group UserGroup
	err := s.db.QueryRowContext(ctx, `SELECT code, name, description, built_in, created_at, updated_at,
		(SELECT COUNT(*) FROM users WHERE group_code = user_groups.code AND deleted_at IS NULL)
		FROM user_groups WHERE code = ?`, code).Scan(&group.Code, &group.Name, &group.Description,
		&group.BuiltIn, &group.CreatedAt, &group.UpdatedAt, &group.UserCount)
	if errors.Is(err, sql.ErrNoRows) {
		return UserGroup{}, ErrNotFound
	}
	if err != nil {
		return UserGroup{}, err
	}
	if group.Code == GroupSuperAdmin {
		group.Permissions = permissionCodes()
	} else {
		set, err := s.groupPermissionSet(ctx, group.Code)
		if err != nil {
			return UserGroup{}, err
		}
		for permission := range set {
			group.Permissions = append(group.Permissions, permission)
		}
		sort.Strings(group.Permissions)
	}
	return group, nil
}

func (s *Store) UserGroupExists(ctx context.Context, code string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_groups WHERE code = ?", code).Scan(&count)
	return count > 0, err
}

func (s *Store) CreateUserGroup(ctx context.Context, name, description string, permissions []string) (UserGroup, error) {
	code, err := newID()
	if err != nil {
		return UserGroup{}, err
	}
	return s.saveUserGroup(ctx, code, name, description, permissions, true)
}

func (s *Store) UpdateUserGroup(ctx context.Context, code, name, description string, permissions []string) (UserGroup, error) {
	return s.saveUserGroup(ctx, code, name, description, permissions, false)
}

func (s *Store) saveUserGroup(ctx context.Context, code, name, description string, permissions []string, create bool) (UserGroup, error) {
	name, description = strings.TrimSpace(name), strings.TrimSpace(description)
	if name == "" || len([]rune(name)) > 100 || len([]rune(description)) > 500 {
		return UserGroup{}, ErrConflict
	}
	if err := validateGroupPermissions(code, permissions); err != nil {
		return UserGroup{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserGroup{}, err
	}
	defer tx.Rollback()
	now := time.Now().UTC()
	if create {
		_, err = tx.ExecContext(ctx, `INSERT INTO user_groups
			(code, name, description, sort_order, built_in, created_at, updated_at)
			VALUES (?, ?, ?, 100, FALSE, ?, ?)`, code, name, description, now, now)
	} else {
		var builtIn bool
		if err = tx.QueryRowContext(ctx, "SELECT built_in FROM user_groups WHERE code = ? FOR UPDATE", code).Scan(&builtIn); errors.Is(err, sql.ErrNoRows) {
			return UserGroup{}, ErrNotFound
		}
		if err == nil && code == GroupSuperAdmin {
			return UserGroup{}, ErrProtected
		}
		if err == nil && builtIn {
			_, err = tx.ExecContext(ctx, "UPDATE user_groups SET updated_at = ? WHERE code = ?", now, code)
		} else if err == nil {
			_, err = tx.ExecContext(ctx, "UPDATE user_groups SET name = ?, description = ?, updated_at = ? WHERE code = ?", name, description, now, code)
		}
	}
	if err != nil {
		var mysqlError *mysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return UserGroup{}, ErrConflict
		}
		return UserGroup{}, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM group_permissions WHERE group_code = ?", code); err != nil {
		return UserGroup{}, err
	}
	seen := make(map[string]struct{})
	for _, permission := range permissions {
		if _, exists := seen[permission]; exists {
			continue
		}
		seen[permission] = struct{}{}
		if _, err := tx.ExecContext(ctx, `INSERT INTO group_permissions (group_code, permission_code)
			VALUES (?, ?)`, code, permission); err != nil {
			return UserGroup{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return UserGroup{}, err
	}
	return s.GetUserGroup(ctx, code)
}

func (s *Store) DeleteUserGroup(ctx context.Context, code string) error {
	group, err := s.GetUserGroup(ctx, code)
	if err != nil {
		return err
	}
	if group.BuiltIn {
		return ErrProtected
	}
	if group.UserCount > 0 {
		return ErrInUse
	}
	result, err := s.db.ExecContext(ctx, "DELETE FROM user_groups WHERE code = ?", code)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UserPermissionProfile(ctx context.Context, userID string) (UserPermissionProfile, error) {
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return UserPermissionProfile{}, err
	}
	group, err := s.GetUserGroup(ctx, user.GroupCode)
	if err != nil {
		return UserPermissionProfile{}, err
	}
	groupSet := make(map[string]struct{}, len(group.Permissions))
	for _, code := range group.Permissions {
		groupSet[code] = struct{}{}
	}
	effective, err := s.EffectivePermissions(ctx, user.ID, user.GroupCode)
	if err != nil {
		return UserPermissionProfile{}, err
	}
	effectiveSet := make(map[string]struct{}, len(effective))
	if user.GroupCode == GroupSuperAdmin {
		for _, code := range permissionCodes() {
			effectiveSet[code] = struct{}{}
		}
	} else {
		for _, code := range effective {
			effectiveSet[code] = struct{}{}
		}
	}
	items := make([]UserPermissionItem, 0, len(permissionDefinitions))
	for _, definition := range permissionDefinitions {
		_, groupValue := groupSet[definition.Code]
		_, effectiveValue := effectiveSet[definition.Code]
		items = append(items, UserPermissionItem{
			Code: definition.Code, Name: definition.Name, Category: definition.Category,
			GroupValue: groupValue, Effective: effectiveValue,
		})
	}
	return UserPermissionProfile{
		Group:       UserGroupSummary{Code: group.Code, Name: group.Name},
		Permissions: items,
	}, nil
}

func (s *Store) SetUserPermissions(ctx context.Context, userID string, desired []string) (UserPermissionProfile, error) {
	user, err := s.GetUser(ctx, userID)
	if err != nil {
		return UserPermissionProfile{}, err
	}
	if user.GroupCode == GroupSuperAdmin {
		return UserPermissionProfile{}, ErrProtected
	}
	desiredSet := make(map[string]struct{}, len(desired))
	for _, code := range desired {
		if !ValidPermission(code) || code == "permission.manage" {
			return UserPermissionProfile{}, ErrProtected
		}
		desiredSet[code] = struct{}{}
	}
	defaults, err := s.groupPermissionSet(ctx, user.GroupCode)
	if err != nil {
		return UserPermissionProfile{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserPermissionProfile{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM user_permission_overrides WHERE user_id = ?", userID); err != nil {
		return UserPermissionProfile{}, err
	}
	now := time.Now().UTC()
	for _, definition := range permissionDefinitions {
		_, defaultAllowed := defaults[definition.Code]
		_, desiredAllowed := desiredSet[definition.Code]
		if defaultAllowed == desiredAllowed {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_permission_overrides
			(user_id, permission_code, allowed, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			userID, definition.Code, desiredAllowed, now, now); err != nil {
			return UserPermissionProfile{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return UserPermissionProfile{}, err
	}
	return s.UserPermissionProfile(ctx, userID)
}

func (s *Store) groupPermissionSet(ctx context.Context, code string) (map[string]struct{}, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT permission_code FROM group_permissions WHERE group_code = ?", code)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[string]struct{})
	for rows.Next() {
		var permission string
		if err := rows.Scan(&permission); err != nil {
			return nil, err
		}
		if ValidPermission(permission) {
			result[permission] = struct{}{}
		}
	}
	return result, rows.Err()
}

func validateGroupPermissions(groupCode string, permissions []string) error {
	for _, permission := range permissions {
		if !ValidPermission(permission) {
			return ErrNotFound
		}
		if permission == "permission.manage" && groupCode != GroupSuperAdmin {
			return ErrProtected
		}
	}
	return nil
}
