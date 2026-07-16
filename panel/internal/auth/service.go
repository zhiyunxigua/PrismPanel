package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"PrismPanel/internal/store"
)

var (
	ErrInvalidCredentials = errors.New("用户名或密码错误")
	ErrLoginLimited       = errors.New("登录尝试过于频繁，请稍后再试")
	ErrUnauthenticated    = errors.New("登录状态已失效")
	ErrForbidden          = errors.New("无权执行此操作")
	ErrInvalidInput       = errors.New("输入内容无效")
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

type Options struct {
	SessionLifetime time.Duration
	IdleTimeout     time.Duration
}

type Service struct {
	store           *store.Store
	sessionLifetime time.Duration
	idleTimeout     time.Duration
	dummyHash       string
	limiter         *loginLimiter
}

func NewService(repository *store.Store, options Options) (*Service, error) {
	dummyHash, err := HashPassword("invalid-password-placeholder")
	if err != nil {
		return nil, err
	}
	return &Service{
		store: repository, sessionLifetime: options.SessionLifetime,
		idleTimeout: options.IdleTimeout, dummyHash: dummyHash, limiter: newLoginLimiter(),
	}, nil
}

func (s *Service) Login(
	ctx context.Context,
	username string,
	password string,
	sourceIP string,
	userAgent string,
) (store.User, string, error) {
	username = normalizeUsername(username)
	now := time.Now().UTC()
	keys := []string{"account:" + username, "ip:" + sourceIP}
	if !s.limiter.allowed(keys, now) {
		return store.User{}, "", ErrLoginLimited
	}
	user, err := s.store.FindUserByUsername(ctx, username)
	hash := s.dummyHash
	if err == nil {
		hash = user.PasswordHash
	} else if !errors.Is(err, store.ErrNotFound) {
		return store.User{}, "", err
	}
	valid := VerifyPassword(hash, password)
	if err != nil || !valid || user.Status != store.UserActive || user.DeletedAt != nil {
		s.limiter.failure(keys, now)
		return store.User{}, "", ErrInvalidCredentials
	}
	token, tokenHash, err := newSessionToken()
	if err != nil {
		return store.User{}, "", err
	}
	expiresAt := now.Add(s.sessionLifetime)
	idleExpiresAt := minTime(now.Add(s.idleTimeout), expiresAt)
	if err := s.store.CreateSession(
		ctx, tokenHash, user.ID, now, expiresAt, idleExpiresAt, sourceIP, truncate(userAgent, 255),
	); err != nil {
		return store.User{}, "", err
	}
	if err := s.store.MarkLogin(ctx, user.ID, now); err != nil {
		_ = s.store.RevokeSession(ctx, tokenHash)
		return store.User{}, "", err
	}
	user.LastLoginAt = &now
	s.limiter.success(keys)
	user, err = s.store.DecorateUser(ctx, user)
	return user, token, err
}

func (s *Service) LoginOrInitialize(
	ctx context.Context,
	username string,
	password string,
	sourceIP string,
	userAgent string,
) (store.User, string, bool, error) {
	hasUsers, err := s.store.HasUsers(ctx)
	if err != nil {
		return store.User{}, "", false, err
	}
	created := false
	if !hasUsers {
		_, err = s.CreateInitialAdmin(ctx, username, strings.TrimSpace(username), password)
		if err == nil {
			created = true
		} else if !errors.Is(err, store.ErrConflict) {
			return store.User{}, "", false, err
		}
	}
	user, token, err := s.Login(ctx, username, password, sourceIP, userAgent)
	return user, token, created, err
}

func (s *Service) Authenticate(ctx context.Context, token string) (store.Session, error) {
	tokenHash, err := sessionTokenHash(token)
	if err != nil {
		return store.Session{}, ErrUnauthenticated
	}
	now := time.Now().UTC()
	session, err := s.store.FindSession(ctx, tokenHash, now)
	if errors.Is(err, store.ErrNotFound) {
		return store.Session{}, ErrUnauthenticated
	}
	if err != nil {
		return store.Session{}, err
	}
	if now.Sub(session.LastSeenAt) >= 5*time.Minute {
		idleExpiresAt := minTime(now.Add(s.idleTimeout), session.ExpiresAt)
		if err := s.store.TouchSession(ctx, tokenHash, now, idleExpiresAt); err != nil {
			return store.Session{}, err
		}
		session.LastSeenAt = now
		session.IdleExpiresAt = idleExpiresAt
	}
	session.User, err = s.store.DecorateUser(ctx, session.User)
	if err != nil {
		return store.Session{}, err
	}
	return session, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	tokenHash, err := sessionTokenHash(token)
	if err != nil {
		return nil
	}
	return s.store.RevokeSession(ctx, tokenHash)
}

func (s *Service) CreateInitialAdmin(
	ctx context.Context,
	username string,
	displayName string,
	password string,
) (store.User, error) {
	user, err := newUser(username, displayName, store.GroupSuperAdmin, password)
	if err != nil {
		return store.User{}, err
	}
	return s.store.CreateInitialAdmin(ctx, user)
}

func (s *Service) CreateUser(
	ctx context.Context,
	username string,
	displayName string,
	groupCode string,
	password string,
) (store.User, error) {
	groupCode = strings.TrimSpace(groupCode)
	exists, err := s.store.UserGroupExists(ctx, groupCode)
	if err != nil {
		return store.User{}, err
	}
	if !exists {
		return store.User{}, fmt.Errorf("%w: 用户组不存在", ErrInvalidInput)
	}
	user, err := newUser(username, displayName, groupCode, password)
	if err != nil {
		return store.User{}, err
	}
	return s.store.CreateUser(ctx, user)
}

func (s *Service) ChangePassword(
	ctx context.Context,
	session store.Session,
	currentPassword string,
	newPassword string,
) error {
	if !VerifyPassword(session.User.PasswordHash, currentPassword) {
		return ErrInvalidCredentials
	}
	hash, err := validatedPasswordHash(newPassword)
	if err != nil {
		return err
	}
	return s.store.SetPassword(ctx, session.User.ID, hash, session.TokenHash)
}

func (s *Service) ResetPassword(ctx context.Context, userID string, password string) error {
	hash, err := validatedPasswordHash(password)
	if err != nil {
		return err
	}
	return s.store.SetPassword(ctx, userID, hash, nil)
}

func newUser(username, displayName, groupCode, password string) (store.NewUser, error) {
	username = normalizeUsername(username)
	displayName = strings.TrimSpace(displayName)
	if !usernamePattern.MatchString(username) {
		return store.NewUser{}, fmt.Errorf("%w: 用户名需为 3-32 位小写字母、数字、点、下划线或连字符", ErrInvalidInput)
	}
	if displayName == "" || len([]rune(displayName)) > 100 {
		return store.NewUser{}, fmt.Errorf("%w: 显示名称不能为空且不能超过 100 个字符", ErrInvalidInput)
	}
	if strings.TrimSpace(groupCode) == "" {
		return store.NewUser{}, fmt.Errorf("%w: 用户组不能为空", ErrInvalidInput)
	}
	passwordHash, err := validatedPasswordHash(password)
	if err != nil {
		return store.NewUser{}, err
	}
	id, err := randomHex(16)
	if err != nil {
		return store.NewUser{}, err
	}
	return store.NewUser{
		ID: id, Username: username, DisplayName: displayName,
		GroupCode: groupCode, PasswordHash: passwordHash,
	}, nil
}

func ValidStatus(status string) bool {
	return status == store.UserActive || status == store.UserDisabled
}

func validatedPasswordHash(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	return HashPassword(password)
}

func normalizeUsername(username string) string {
	return strings.ToLower(strings.TrimSpace(username))
}

func newSessionToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

func sessionTokenHash(token string) ([]byte, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("invalid session token")
	}
	hash := sha256.Sum256([]byte(token))
	return hash[:], nil
}

func randomHex(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func minTime(left, right time.Time) time.Time {
	if left.Before(right) {
		return left
	}
	return right
}

func truncate(value string, max int) string {
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max])
}
