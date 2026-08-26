package application

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"PrismPanel-winapp/internal/credentials"
	"PrismPanel-winapp/internal/settings"
)

func TestLoginSavesCredentialOnlyAfterSuccess(t *testing.T) {
	panel := loginPanel(t, "secret")
	defer panel.Close()
	vault := newMemoryCredentialStore()
	service := New(settings.NewStore(filepath.Join(t.TempDir(), "settings.json")), vault)
	if _, err := service.ConfigurePanelURL(context.Background(), panel.URL); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close(context.Background()) }()

	if _, err := service.Login(context.Background(), "admin", "wrong", true); err == nil {
		t.Fatal("expected invalid login to fail")
	}
	if accounts, _ := vault.List(panel.URL); len(accounts) != 0 {
		t.Fatalf("failed login saved credentials: %#v", accounts)
	}
	result, err := service.Login(context.Background(), "admin", "secret", true)
	if err != nil {
		t.Fatal(err)
	}
	if result.User["username"] != "admin" || result.CredentialWarning != "" {
		t.Fatalf("unexpected login result: %#v", result)
	}
	accounts, err := service.SavedAccounts()
	if err != nil || len(accounts) != 1 || accounts[0].Username != "admin" {
		t.Fatalf("unexpected saved accounts: %#v, %v", accounts, err)
	}
	if automatic, _ := vault.AutoLoginAccount(panel.URL); automatic != accounts[0].ID {
		t.Fatalf("automatic account = %q", automatic)
	}
}

func TestStartAutomaticallyLogsInSavedAccount(t *testing.T) {
	panel := loginPanel(t, "secret")
	defer panel.Close()
	settingsStore := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	if err := settingsStore.Save(settings.Settings{PanelURL: panel.URL}); err != nil {
		t.Fatal(err)
	}
	vault := newMemoryCredentialStore()
	account, _ := vault.Save(panel.URL, "admin", "secret", time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
	_ = vault.SetAutoLoginAccount(panel.URL, account.ID)
	service := New(settingsStore, vault)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close(context.Background()) }()
	if runtime := service.RuntimeConfig(); runtime.AutoLoginErr != "" || !runtime.Configured {
		t.Fatalf("unexpected runtime: %#v", runtime)
	}
	updated, _ := vault.Get(panel.URL, account.ID)
	if !updated.LastLoginAt.After(account.LastLoginAt) {
		t.Fatalf("automatic login did not refresh login time: %#v", updated)
	}
}

func TestInvalidAutomaticCredentialIsDisabledButPreserved(t *testing.T) {
	panel := loginPanel(t, "new-secret")
	defer panel.Close()
	settingsStore := settings.NewStore(filepath.Join(t.TempDir(), "settings.json"))
	if err := settingsStore.Save(settings.Settings{PanelURL: panel.URL}); err != nil {
		t.Fatal(err)
	}
	vault := newMemoryCredentialStore()
	account, _ := vault.Save(panel.URL, "admin", "old-secret", time.Now())
	_ = vault.SetAutoLoginAccount(panel.URL, account.ID)
	service := New(settingsStore, vault)
	if err := service.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close(context.Background()) }()
	if runtime := service.RuntimeConfig(); !strings.Contains(runtime.AutoLoginErr, "密码已失效") {
		t.Fatalf("unexpected automatic login error: %#v", runtime)
	}
	if automatic, _ := vault.AutoLoginAccount(panel.URL); automatic != "" {
		t.Fatalf("automatic account was not cleared: %q", automatic)
	}
	if accounts, _ := vault.List(panel.URL); len(accounts) != 1 {
		t.Fatalf("saved account should remain available: %#v", accounts)
	}
}

func TestLoginWithoutRememberDisablesAutomaticLoginButKeepsSavedAccounts(t *testing.T) {
	panel := loginPanel(t, "secret")
	defer panel.Close()
	vault := newMemoryCredentialStore()
	account, _ := vault.Save(panel.URL, "admin", "secret", time.Now())
	_ = vault.SetAutoLoginAccount(panel.URL, account.ID)
	service := New(settings.NewStore(filepath.Join(t.TempDir(), "settings.json")), vault)
	if _, err := service.ConfigurePanelURL(context.Background(), panel.URL); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close(context.Background()) }()

	if _, err := service.Login(context.Background(), "admin", "secret", false); err != nil {
		t.Fatal(err)
	}
	if automatic, _ := vault.AutoLoginAccount(panel.URL); automatic != "" {
		t.Fatalf("automatic login was not disabled: %q", automatic)
	}
	if accounts, _ := vault.List(panel.URL); len(accounts) != 1 {
		t.Fatalf("saved accounts were removed: %#v", accounts)
	}
}

func TestUpdateSavedPasswordPreservesLoginOrderTimestamp(t *testing.T) {
	panel := loginPanel(t, "secret")
	defer panel.Close()
	vault := newMemoryCredentialStore()
	loginTime := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	account, _ := vault.Save(panel.URL, "admin", "old-secret", loginTime)
	service := New(settings.NewStore(filepath.Join(t.TempDir(), "settings.json")), vault)
	if _, err := service.ConfigurePanelURL(context.Background(), panel.URL); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close(context.Background()) }()

	updated, err := service.UpdateSavedPassword("ADMIN", "new-secret")
	if err != nil || !updated {
		t.Fatalf("password update = %v, %v", updated, err)
	}
	credential, err := vault.Get(panel.URL, account.ID)
	if err != nil || credential.Password != "new-secret" || !credential.LastLoginAt.Equal(loginTime) {
		t.Fatalf("unexpected updated credential: %#v, %v", credential, err)
	}
}

func loginPanel(t *testing.T, password string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/v1/auth/status":
			_, _ = writer.Write([]byte(`{"success":true,"data":{"initialized":true}}`))
		case "/api/v1/auth/login":
			var body struct {
				Username string `json:"username"`
				Password string `json:"password"`
			}
			if err := decodeJSON(request, &body); err != nil {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			if body.Username != "admin" || body.Password != password {
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write([]byte(`{"success":false,"error":{"code":"INVALID_CREDENTIALS","message":"用户名或密码错误"}}`))
				return
			}
			_, _ = writer.Write([]byte(`{"success":true,"data":{"user":{"username":"admin","last_login_at":"2026-07-18T10:00:00Z"},"initialized":false}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
}

func decodeJSON(request *http.Request, output any) error {
	defer request.Body.Close()
	return json.NewDecoder(request.Body).Decode(output)
}

type memoryCredentialStore struct {
	accounts map[string]map[string]credentials.Credential
	auto     map[string]string
}

func newMemoryCredentialStore() *memoryCredentialStore {
	return &memoryCredentialStore{
		accounts: make(map[string]map[string]credentials.Credential), auto: make(map[string]string),
	}
}

func (s *memoryCredentialStore) List(panelURL string) ([]credentials.Account, error) {
	items := make([]credentials.Account, 0, len(s.accounts[panelURL]))
	for _, item := range s.accounts[panelURL] {
		items = append(items, item.Account)
	}
	sort.Slice(items, func(left, right int) bool { return items[left].LastLoginAt.After(items[right].LastLoginAt) })
	return items, nil
}

func (s *memoryCredentialStore) Get(panelURL, accountID string) (credentials.Credential, error) {
	item, exists := s.accounts[panelURL][accountID]
	if !exists {
		return credentials.Credential{}, credentials.ErrNotFound
	}
	return item, nil
}

func (s *memoryCredentialStore) Save(
	panelURL string, username string, password string, lastLoginAt time.Time,
) (credentials.Account, error) {
	if s.accounts[panelURL] == nil {
		s.accounts[panelURL] = make(map[string]credentials.Credential)
	}
	id := strings.ToLower(strings.TrimSpace(username))
	account := credentials.Account{ID: id, Username: username, LastLoginAt: lastLoginAt}
	s.accounts[panelURL][id] = credentials.Credential{Account: account, Password: password}
	return account, nil
}

func (s *memoryCredentialStore) Delete(panelURL, accountID string) error {
	delete(s.accounts[panelURL], accountID)
	if s.auto[panelURL] == accountID {
		delete(s.auto, panelURL)
	}
	return nil
}

func (s *memoryCredentialStore) ClearAll(panelURL string) error {
	delete(s.accounts, panelURL)
	delete(s.auto, panelURL)
	return nil
}

func (s *memoryCredentialStore) AutoLoginAccount(panelURL string) (string, error) {
	return s.auto[panelURL], nil
}

func (s *memoryCredentialStore) SetAutoLoginAccount(panelURL, accountID string) error {
	if accountID == "" {
		delete(s.auto, panelURL)
	} else {
		s.auto[panelURL] = accountID
	}
	return nil
}

var _ credentials.Store = (*memoryCredentialStore)(nil)
