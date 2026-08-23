package game

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	ErrAuthPending = errors.New("device login pending")
	ErrAuthExpired = errors.New("device login expired")
)

func defaultMSAClientID() string {
	if value := strings.TrimSpace(os.Getenv("PRISMPANEL_MSA_CLIENT_ID")); value != "" {
		return value
	}
	return "00000000-0000-0000-0000-000000000000"
}

type deviceState struct {
	clientID   string
	deviceCode string
	expiresAt  time.Time
}

var mcDeviceStates sync.Map

// MCStartDeviceLogin 发起微软设备码登录，返回用户码供前端展示。
func MCStartDeviceLogin(ctx context.Context) (MCDeviceLogin, error) {
	clientID := defaultMSAClientID()
	form := url.Values{}
	form.Set("client_id", clientID)
	form.Set("scope", "XboxLive.signin offline_access")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://login.microsoftonline.com/consumers/oauth2/v2.0/devicecode",
		strings.NewReader(form.Encode()))
	if err != nil {
		return MCDeviceLogin{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return MCDeviceLogin{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return MCDeviceLogin{}, fmt.Errorf("device code request failed: %s", response.Status)
	}
	var payload struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
		Message         string `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return MCDeviceLogin{}, err
	}
	stateID, _ := randomHex(16)
	mcDeviceStates.Store(stateID, deviceState{
		clientID: clientID, deviceCode: payload.DeviceCode,
		expiresAt: time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second),
	})
	return MCDeviceLogin{
		StateID: stateID, DeviceCode: payload.DeviceCode, UserCode: payload.UserCode,
		VerificationURI: payload.VerificationURI, ExpiresIn: payload.ExpiresIn,
		Interval: payload.Interval, Message: payload.Message,
	}, nil
}

// MCPollDeviceLogin 轮询设备码登录结果；未完成时返回 ErrAuthPending。
func MCPollDeviceLogin(ctx context.Context, stateID string) (MCAccount, error) {
	value, ok := mcDeviceStates.Load(stateID)
	if !ok {
		return MCAccount{}, errors.New("device login state not found")
	}
	state := value.(deviceState)
	if time.Now().After(state.expiresAt) {
		mcDeviceStates.Delete(stateID)
		return MCAccount{}, ErrAuthExpired
	}
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
	form.Set("client_id", state.clientID)
	form.Set("device_code", state.deviceCode)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://login.microsoftonline.com/consumers/oauth2/v2.0/token",
		strings.NewReader(form.Encode()))
	if err != nil {
		return MCAccount{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return MCAccount{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return MCAccount{}, err
	}
	var tokenPayload struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
		Error        string `json:"error"`
	}
	if err := json.Unmarshal(body, &tokenPayload); err != nil {
		return MCAccount{}, err
	}
	if tokenPayload.Error != "" {
		if tokenPayload.Error == "authorization_pending" || tokenPayload.Error == "slow_down" {
			return MCAccount{}, ErrAuthPending
		}
		if tokenPayload.Error == "authorization_declined" || tokenPayload.Error == "expired_token" {
			mcDeviceStates.Delete(stateID)
			return MCAccount{}, ErrAuthExpired
		}
		return MCAccount{}, fmt.Errorf("device login failed: %s", tokenPayload.Error)
	}
	mcDeviceStates.Delete(stateID)
	return MCMicrosoftAuthenticate(ctx, tokenPayload.AccessToken, tokenPayload.RefreshToken)
}

// MCThirdPartyLogin 通过第三方认证服务器（authlib-injector / Yggdrasil，如 LittleSkin）登录。
// server 为认证服务器根地址，如 https://littleskin.cn/api/yggdrasil
func MCThirdPartyLogin(ctx context.Context, server, username, password string) (MCAccount, error) {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	username = strings.TrimSpace(username)
	if server == "" || !strings.HasPrefix(strings.ToLower(server), "http") {
		return MCAccount{}, errors.New("第三方认证服务器地址无效（需以 http(s):// 开头）")
	}
	if username == "" || password == "" {
		return MCAccount{}, errors.New("请输入第三方认证的账号与密码")
	}
	// 校验服务器元信息（可选）：确认该地址是认证服务器根
	if request, err := http.NewRequestWithContext(ctx, http.MethodGet, server+"/authlib-injector", nil); err == nil {
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				var meta struct {
					Meta struct {
						ServerName string `json:"serverName"`
					} `json:"meta"`
				}
				_ = json.Unmarshal(body, &meta)
			}
		}
	}
	clientToken, _ := randomHex(32)
	payload, _ := json.Marshal(map[string]any{
		"agent":    map[string]any{"name": "Minecraft", "version": 1},
		"username": username, "password": password,
		"clientToken": clientToken, "requestUser": true,
	})
	var out struct {
		AccessToken     string `json:"accessToken"`
		ClientToken     string `json:"clientToken"`
		SelectedProfile struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"selectedProfile"`
		Error        string `json:"error"`
		ErrorMessage string `json:"errorMessage"`
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, server+"/authserver/authenticate", strings.NewReader(string(payload)))
	if err != nil {
		return MCAccount{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return MCAccount{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return MCAccount{}, err
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return MCAccount{}, fmt.Errorf("第三方认证响应无法解析：%w", err)
	}
	if out.Error != "" {
		return MCAccount{}, fmt.Errorf("第三方认证失败：%s %s", out.Error, out.ErrorMessage)
	}
	if out.AccessToken == "" || out.SelectedProfile.ID == "" || out.SelectedProfile.Name == "" {
		return MCAccount{}, errors.New("第三方认证响应缺少 accessToken / selectedProfile")
	}
	account := MCAccount{
		Mode: MCAuthThirdParty, Name: out.SelectedProfile.Name,
		UUID:        dashUUID(out.SelectedProfile.ID),
		AccessToken: out.AccessToken, ClientToken: out.ClientToken,
		AuthServer: server, UpdatedAt: time.Now().UTC(),
	}
	store := NewMCLocalStore()
	if err := store.Save(account); err != nil {
		return MCAccount{}, err
	}
	return account, nil
}

// dashUUID 把无横线的 UUID 规范化为 8-4-4-4-12 形式。
func dashUUID(value string) string {
	clean := strings.ReplaceAll(strings.TrimSpace(value), "-", "")
	if len(clean) != 32 {
		return value
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		clean[0:8], clean[8:12], clean[12:16], clean[16:20], clean[20:32])
}

// MCSetOffline 设置离线账号（无需微软登录）。
func MCSetOffline(ctx context.Context, name string) (MCAccount, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return MCAccount{}, errors.New("offline username is required")
	}
	account := MCAccount{
		Mode: MCAuthOffline, Name: name, UUID: offlineUUID(name),
		AccessToken: func() string { value, _ := randomHex(16); return value }(),
		UpdatedAt:   time.Now().UTC(),
	}
	store := NewMCLocalStore()
	if err := store.Save(account); err != nil {
		return MCAccount{}, err
	}
	return account, nil
}

// MCMicrosoftAuthenticate 用微软 access_token 走 Xbox → XSTS → Minecraft 认证链。
func MCMicrosoftAuthenticate(ctx context.Context, msAccessToken, refreshToken string) (MCAccount, error) {
	xboxToken, uhs, err := xboxLiveAuthenticate(ctx, msAccessToken)
	if err != nil {
		return MCAccount{}, err
	}
	xstsToken, err := xstsAuthorize(ctx, xboxToken)
	if err != nil {
		return MCAccount{}, err
	}
	mcToken, err := minecraftLogin(ctx, uhs, xstsToken)
	if err != nil {
		return MCAccount{}, err
	}
	profileID, profileName, err := minecraftProfile(ctx, mcToken)
	if err != nil {
		return MCAccount{}, err
	}
	account := MCAccount{
		Mode: MCAuthMicrosoft, Name: profileName, UUID: profileID,
		AccessToken: mcToken, RefreshToken: refreshToken,
		ExpiresAt: time.Now().Add(time.Hour), UpdatedAt: time.Now().UTC(),
	}
	store := NewMCLocalStore()
	if err := store.Save(account); err != nil {
		return MCAccount{}, err
	}
	return account, nil
}

func xboxLiveAuthenticate(ctx context.Context, msAccessToken string) (token, uhs string, err error) {
	payload, _ := json.Marshal(map[string]any{
		"RelyingParty": "http://auth.xboxlive.com", "TokenType": "JWT",
		"Properties": map[string]any{
			"AuthMethod": "RPS", "SiteName": "user.auth.xboxlive.com", "RpsTicket": msAccessToken,
		},
	})
	var out struct {
		Token         string `json:"Token"`
		DisplayClaims struct {
			XUI []struct {
				UHS string `json:"uhs"`
			} `json:"xui"`
		} `json:"DisplayClaims"`
	}
	if err := postJSON(ctx, "https://user.auth.xboxlive.com/user/authenticate", payload, &out); err != nil {
		return "", "", err
	}
	if out.Token == "" || len(out.DisplayClaims.XUI) == 0 {
		return "", "", errors.New("xbox live authentication returned no token")
	}
	return out.Token, out.DisplayClaims.XUI[0].UHS, nil
}

func xstsAuthorize(ctx context.Context, xboxToken string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"RelyingParty": "rp://api.minecraftservices.com/", "TokenType": "JWT",
		"Properties": map[string]any{"SandboxId": "RETAIL", "UserTokens": []string{xboxToken}},
	})
	var out struct {
		Token string `json:"Token"`
	}
	if err := postJSON(ctx, "https://xsts.auth.xboxlive.com/xsts/authorize", payload, &out); err != nil {
		return "", err
	}
	if out.Token == "" {
		return "", errors.New("xsts authorize returned no token")
	}
	return out.Token, nil
}

func minecraftLogin(ctx context.Context, uhs, xstsToken string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"identityToken": "XBL3.0 x=" + uhs + ";" + xstsToken,
	})
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := postJSON(ctx, "https://api.minecraftservices.com/authentication/login_with_xbox", payload, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", errors.New("minecraft login returned no token")
	}
	return out.AccessToken, nil
}

func minecraftProfile(ctx context.Context, mcToken string) (id, name string, err error) {
	var out struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.minecraftservices.com/minecraft/profile", nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Authorization", "Bearer "+mcToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return "", "", errors.New("该微软账号未购买 Minecraft 国际版")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", fmt.Errorf("minecraft profile request failed: %s", response.Status)
	}
	if err := json.NewDecoder(response.Body).Decode(&out); err != nil {
		return "", "", err
	}
	return out.ID, out.Name, nil
}

func postJSON(ctx context.Context, endpoint string, payload []byte, out any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return fmt.Errorf("%s failed: %s %s", endpoint, response.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(response.Body).Decode(out)
}

// offlineUUID 按离线玩家名生成稳定的版本 3 UUID。
func offlineUUID(name string) string {
	hash := md5.Sum([]byte("OfflinePlayer:" + name))
	hash[6] = (hash[6] & 0x0f) | 0x30
	hash[8] = (hash[8] & 0x3f) | 0x80
	raw := hash[:]
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(raw[0:4]), hex.EncodeToString(raw[4:6]),
		hex.EncodeToString(raw[6:8]), hex.EncodeToString(raw[8:10]), hex.EncodeToString(raw[10:16]))
}
