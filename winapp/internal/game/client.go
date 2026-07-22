package game

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	mpayBaseURL    = "https://service.mkey.163.com"
	mpayGameID     = "aecfrxodyqaaaajp-g-x19"
	updateBaseURL  = "https://x19.update.netease.com"
	coreBaseURL    = "https://x19obtcore.nie.netease.com:8443"
	gatewayBaseURL = "https://x19apigatewayobt.nie.netease.com"
	clientBaseURL  = "https://x19mclobt.nie.netease.com"
	x19UserAgent   = "WPFLauncher/0.0.0.0"
	requestTimeout = 30 * time.Second
)

var httpIV = []byte("szkgpbyimxavqjcn")
var httpKeys = strings.Split("MK6mipwmOUedplb6,OtEylfId6dyhrfdn,VNbhn5mvUaQaeOo9,bIEoQGQYjKd02U0J,fuaJrPwaH2cfXXLP,LEkdyiroouKQ4XN1,jM1h27H4UROu427W,DhReQada7gZybTDk,ZGXfpSTYUvcdKqdY,AZwKf7MWZrJpGR5W,amuvbcHw38TcSyPU,SI4QotspbjhyFdT0,VP4dhjKnDGlSJtbB,UXDZx4KhZywQ2tcn,NIK73ZNvNqzva4kd,WeiW7qU766Q1YQZI", ",")

type ProtocolError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	VerifyURL string `json:"verify_url,omitempty"`
}

func (e *ProtocolError) Error() string {
	if e.VerifyURL != "" {
		return e.Message + ": " + e.VerifyURL
	}
	return e.Message
}

type DeviceState struct {
	UniqueID string `json:"unique_id"`
	ID       string `json:"id"`
	Key      string `json:"key"`
}

type AccountState struct {
	Email      string      `json:"email"`
	Password   string      `json:"password"`
	Device     DeviceState `json:"device"`
	UserID     string      `json:"user_id,omitempty"`
	UserToken  string      `json:"user_token,omitempty"`
	VerifiedAt *time.Time  `json:"verified_at,omitempty"`
}

type Client struct {
	http            *http.Client
	email           string
	password        string
	device          DeviceState
	launcherVersion string
	userID          string
	userToken       string
}

type MinecraftClientLibs struct {
	CoreLibMD5  string `json:"core_lib_md5"`
	CoreLibName string `json:"core_lib_name"`
	CoreLibSize int    `json:"core_lib_size"`
	CoreLibURL  string `json:"core_lib_url"`
	MCVersion   int    `json:"mc_version"`
	MD5         string `json:"md5"`
	Name        string `json:"name"`
	RefreshTime int    `json:"refresh_time"`
	Size        int    `json:"size"`
	URL         string `json:"url"`
	Version     string `json:"version"`
}

func NewClient(account AccountState) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &Client{
		http:  &http.Client{Jar: jar, Timeout: requestTimeout},
		email: strings.TrimSpace(account.Email), password: strings.TrimSpace(account.Password),
		device: account.Device, userID: account.UserID, userToken: account.UserToken,
	}, nil
}

func (c *Client) Login(ctx context.Context) (AccountState, error) {
	version, err := c.latestLauncherVersion(ctx)
	if err != nil {
		return AccountState{}, err
	}
	c.launcherVersion = version
	device, err := c.loadOrCreateDevice(ctx)
	if err != nil {
		return AccountState{}, err
	}
	user, err := c.loginMPay(ctx, device)
	if err != nil {
		return AccountState{}, err
	}
	sauth := c.buildSAuthJSON(user, device)
	otp, err := c.loginOTP(ctx, sauth)
	if err != nil {
		return AccountState{}, err
	}
	authenticated, err := c.authenticateOTP(ctx, sauth, otp)
	if err != nil {
		return AccountState{}, err
	}
	c.userID = stringValue(authenticated["entity_id"])
	c.userToken = stringValue(authenticated["token"])
	if c.userID == "" || c.userToken == "" {
		return AccountState{}, protocolError("AUTHENTICATION_FAILED", "X19 authentication response is missing user id or token")
	}
	c.device = device
	now := time.Now().UTC()
	return AccountState{
		Email: c.email, Password: c.password, Device: device,
		UserID: c.userID, UserToken: c.userToken, VerifiedAt: &now,
	}, nil
}

func (c *Client) LauncherVersion() string { return c.launcherVersion }

func (c *Client) FetchMinecraftClientLibs(ctx context.Context, version Version) (MinecraftClientLibs, error) {
	if err := version.Validate(); err != nil {
		return MinecraftClientLibs{}, err
	}
	entity, err := c.postSignedEntity(ctx, clientBaseURL, "/game-patch-info", map[string]any{
		"mc_version": uint32(version),
	})
	if err != nil {
		return MinecraftClientLibs{}, err
	}
	encoded, err := json.Marshal(entity)
	if err != nil {
		return MinecraftClientLibs{}, err
	}
	var libs MinecraftClientLibs
	if err := json.Unmarshal(encoded, &libs); err != nil {
		return MinecraftClientLibs{}, err
	}
	if libs.URL == "" || libs.MD5 == "" {
		return MinecraftClientLibs{}, protocolError("VERSION_PACKAGE_UNAVAILABLE", "game-patch-info response is missing package URL or md5")
	}
	return libs, nil
}

func (c *Client) DownloadVersionPackages(ctx context.Context, version Version, paths CachePaths) ([]PackageDownload, error) {
	return c.DownloadVersionPackagesWithProgress(ctx, version, paths, nil)
}

func (c *Client) DownloadVersionPackagesWithProgress(
	ctx context.Context,
	version Version,
	paths CachePaths,
	progress func(label, phase string, itemIndex, itemCount int, current, total int64),
) ([]PackageDownload, error) {
	base, err := c.FetchMinecraftClientLibs(ctx, VersionBase)
	if err != nil {
		return nil, err
	}
	target, err := c.FetchMinecraftClientLibs(ctx, version)
	if err != nil {
		return nil, err
	}
	downloads := VersionDownloads(paths, base, target)
	for index, item := range downloads {
		report := func(phase string, current, total int64) {
			if progress != nil {
				progress(item.Label, phase, index, len(downloads)+1, current, total)
			}
		}
		if _, err := DownloadIfNeededWithProgress(ctx, item, report); err != nil {
			return nil, err
		}
	}
	if len(downloads) > 0 {
		libItem := downloads[len(downloads)-1]
		report := func(phase string, current, total int64) {
			if progress != nil {
				progress(libItem.Label, phase, len(downloads), len(downloads)+1, current, total)
			}
		}
		label, err := VersionLabel(version)
		if err != nil {
			return nil, err
		}
		if err := InstallCoreLibraries(ctx, paths, label, libItem, report); err != nil {
			return nil, err
		}
	}
	return downloads, nil
}
func (c *Client) latestLauncherVersion(ctx context.Context) (string, error) {
	body, err := c.do(ctx, http.MethodGet, updateBaseURL+"/pl/x19_java_patchlist", nil, map[string]string{"User-Agent": x19UserAgent})
	if err != nil {
		return "", err
	}
	text := string(body)
	marker := `":{"size":`
	if pos := strings.LastIndex(text, marker); pos >= 0 {
		if quote := strings.LastIndex(text[:pos], `"`); quote >= 0 {
			return text[quote+1 : pos], nil
		}
	}
	var patchList map[string]map[string]any
	if err := decodeJSON(body, &patchList); err == nil {
		for key, value := range patchList {
			if _, ok := value["size"]; ok {
				return key, nil
			}
		}
	}
	return "", protocolError("LAUNCHER_VERSION_UNAVAILABLE", "cannot read X19 launcher version")
}

func (c *Client) baseParameters() map[string]string {
	return map[string]string{
		"app_channel": "netease", "app_mode": "2", "app_type": "games", "arch": "win_x64",
		"cv": "c4.2.0", "mcount_app_key": "EEkEEXLymcNjM42yLY3Bn6AO15aGy4yq",
		"mcount_transaction_id": "0", "process_id": strconv.Itoa(os.Getpid()),
		"sv": "10.0.22621", "updater_cv": "c1.0.0", "game_id": mpayGameID, "gv": c.launcherVersion,
	}
}

func (c *Client) loadOrCreateDevice(ctx context.Context) (DeviceState, error) {
	if c.device.UniqueID != "" && c.device.ID != "" && c.device.Key != "" {
		if _, err := hex.DecodeString(c.device.Key); err == nil {
			return c.device, nil
		}
	}
	uniqueID, err := randomHex(16)
	if err != nil {
		return DeviceState{}, err
	}
	name, err := randomString(12)
	if err != nil {
		return DeviceState{}, err
	}
	params := c.baseParameters()
	params["brand"] = "Microsoft"
	params["device_model"] = "pc_mode"
	params["device_name"] = "PC-" + name
	params["device_type"] = "Computer"
	params["init_urs_device"] = "0"
	params["mac"] = randomMAC(":")
	params["resolution"] = "1920x1080"
	params["system_name"] = "windows"
	params["system_version"] = "10.0.22621"
	params["unique_id"] = uniqueID
	result, err := c.postForm(ctx, fmt.Sprintf("%s/mpay/games/%s/devices", mpayBaseURL, mpayGameID), params)
	if err != nil {
		return DeviceState{}, err
	}
	deviceRaw, ok := result["device"].(map[string]any)
	if !ok {
		return DeviceState{}, protocolError("DEVICE_REGISTER_FAILED", "MPay device registration response is invalid")
	}
	device := DeviceState{UniqueID: uniqueID, ID: stringValue(deviceRaw["id"]), Key: stringValue(deviceRaw["key"])}
	if device.ID == "" || device.Key == "" {
		return DeviceState{}, protocolError("DEVICE_REGISTER_FAILED", "MPay device registration response is missing id or key")
	}
	return device, nil
}

func (c *Client) loginMPay(ctx context.Context, device DeviceState) (map[string]any, error) {
	passwordDigest := md5.Sum([]byte(c.password))
	paramsCipher, err := encryptMPayParameters(map[string]any{
		"password": hex.EncodeToString(passwordDigest[:]), "unique_id": device.UniqueID, "username": c.email,
	}, device.Key)
	if err != nil {
		return nil, err
	}
	params := c.baseParameters()
	params["opt_fields"] = "nickname,avatar,realname_status,mobile_bind_status,mask_related_mobile,related_login_status"
	params["params"] = paramsCipher
	params["un"] = base64.StdEncoding.EncodeToString([]byte(c.email))
	result, err := c.postForm(ctx, fmt.Sprintf("%s/mpay/games/%s/devices/%s/users", mpayBaseURL, mpayGameID, device.ID), params)
	if err != nil {
		return nil, err
	}
	user, ok := result["user"].(map[string]any)
	if !ok || stringValue(user["id"]) == "" || stringValue(user["token"]) == "" {
		return nil, protocolError("MPAY_LOGIN_FAILED", "163 login response is missing sdkuid or sessionid")
	}
	return user, nil
}

func (c *Client) buildSAuthJSON(user map[string]any, device DeviceState) string {
	udid, _ := randomHex(16)
	return compactJSONString(map[string]any{
		"gameid": "x19", "login_channel": "netease", "app_channel": "netease", "platform": "pc",
		"sdkuid": stringValue(user["id"]), "sessionid": stringValue(user["token"]), "sdk_version": "4.2.0",
		"udid": strings.ToUpper(udid), "deviceid": device.ID,
		"aim_info": compactJSONString(map[string]any{"aim": "127.0.0.1", "country": "CN", "tz": "+0800", "tzid": ""}),
	})
}

func (c *Client) loginOTP(ctx context.Context, sauthJSON string) (map[string]any, error) {
	result, err := c.postJSON(ctx, coreBaseURL+"/login-otp", map[string]any{"sauth_json": sauthJSON}, c.x19Headers("application/json; charset=utf-8"))
	if err != nil {
		return nil, err
	}
	entity, err := requireEntity(result, "login OTP")
	if err != nil {
		return nil, err
	}
	if stringValue(entity["otp_token"]) == "" || entity["aid"] == nil {
		return nil, protocolError("LOGIN_OTP_FAILED", "login-otp response is missing aid or otp token")
	}
	return entity, nil
}

func (c *Client) authenticateOTP(ctx context.Context, sauthJSON string, otp map[string]any) (map[string]any, error) {
	identity, err := randomHex(4)
	if err != nil {
		return nil, err
	}
	detail := map[string]any{
		"os_name": "windows", "os_ver": "Microsoft Windows 11 Professional", "mac_addr": randomMAC(""),
		"udid": "0000000000000000" + strings.ToUpper(identity), "app_ver": c.launcherVersion, "sdk_ver": "",
		"network": "", "disk": strings.ToUpper(identity), "is64bit": "1", "video_card1": "Microsoft Hyper-V Video",
		"video_card2": "Microsoft Remote Display Adapter", "video_card3": "", "video_card4": "",
		"launcher_type": "PC_java", "pay_channel": "netease", "dotnet_ver": "4.8.0",
		"cpu_type": "Intel(R) Core(TM) i7-12700", "ram_size": strconv.FormatInt(16*1024*1024*1024, 10),
		"device_width": "1920", "device_height": "1080", "os_detail": "10.0.26100",
	}
	body := compactJSONString(map[string]any{
		"sa_data": compactJSONString(detail), "sauth_json": sauthJSON,
		"version": map[string]any{"version": c.launcherVersion, "launcher_md5": "", "updater_md5": ""},
		"sdkuid":  nil, "aid": stringValue(otp["aid"]), "hasMessage": false, "hasGmail": false,
		"otp_token": stringValue(otp["otp_token"]), "otp_pwd": nil, "lock_time": 0, "env": nil,
		"min_engine_version": nil, "min_patch_version": nil, "verify_status": 0, "unisdk_login_json": nil,
		"token": nil, "is_register": true, "entity_id": nil,
	})
	encrypted, err := encryptX19HTTP([]byte(body))
	if err != nil {
		return nil, err
	}
	response, err := c.do(ctx, http.MethodPost, coreBaseURL+"/authentication-otp", encrypted, c.x19Headers("application/octet-stream"))
	if err != nil {
		return nil, err
	}
	plain, err := decryptX19HTTP(response)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := decodeJSON(plain, &decoded); err != nil {
		return nil, protocolError("AUTHENTICATION_DECODE_FAILED", "authentication-otp response cannot be decoded")
	}
	return requireEntity(decoded, "authentication OTP")
}

func (c *Client) postSignedEntity(ctx context.Context, baseURL, path string, bodyValue map[string]any) (map[string]any, error) {
	response, err := c.postSigned(ctx, baseURL, path, bodyValue)
	if err != nil {
		return nil, err
	}
	return requireEntity(response, strings.Trim(path, "/"))
}

func (c *Client) postSigned(ctx context.Context, baseURL, path string, bodyValue map[string]any) (map[string]any, error) {
	if c.userID == "" || c.userToken == "" {
		return nil, protocolError("NOT_LOGGED_IN", "X19 login has not completed")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	body := compactJSONString(bodyValue)
	headers := c.x19Headers("application/json; charset=utf-8")
	for key, value := range computeRequestToken(path, body, c.userID, c.userToken) {
		headers[key] = value
	}
	return c.postJSONBytes(ctx, strings.TrimRight(baseURL, "/")+path, []byte(body), headers)
}

func (c *Client) postForm(ctx context.Context, target string, params map[string]string) (map[string]any, error) {
	values := url.Values{}
	for key, value := range params {
		values.Set(key, value)
	}
	return c.postJSONBytes(ctx, target, []byte(values.Encode()), map[string]string{"Content-Type": "application/x-www-form-urlencoded; charset=utf-8"})
}

func (c *Client) postJSON(ctx context.Context, target string, value any, headers map[string]string) (map[string]any, error) {
	return c.postJSONBytes(ctx, target, []byte(compactJSONString(value)), headers)
}

func (c *Client) postJSONBytes(ctx context.Context, target string, body []byte, headers map[string]string) (map[string]any, error) {
	response, err := c.do(ctx, http.MethodPost, target, body, headers)
	if err != nil {
		return nil, err
	}
	var decoded map[string]any
	if err := decodeJSON(response, &decoded); err != nil {
		return nil, protocolError("INVALID_JSON", "server response is not valid JSON")
	}
	return decoded, nil
}

func (c *Client) do(ctx context.Context, method, target string, body []byte, headers map[string]string) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, protocolError("NETWORK_ERROR", err.Error())
	}
	defer resp.Body.Close()
	contents, err := io.ReadAll(io.LimitReader(resp.Body, 8*1024*1024))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var decoded map[string]any
		verifyURL := ""
		message := resp.Status
		if decodeJSON(contents, &decoded) == nil {
			if value := stringValue(decoded["reason"]); value != "" {
				message = value
			} else if value := stringValue(decoded["message"]); value != "" {
				message = value
			}
			verifyURL = stringValue(decoded["verify_url"])
		}
		code := "HTTP_ERROR"
		if verifyURL != "" {
			code = "NEEDS_VERIFICATION"
		}
		return nil, &ProtocolError{Code: code, Message: message, VerifyURL: verifyURL}
	}
	return contents, nil
}

func (c *Client) x19Headers(contentType string) map[string]string {
	return map[string]string{"User-Agent": x19UserAgent, "Content-Type": contentType}
}

func requireEntity(response map[string]any, operation string) (map[string]any, error) {
	if code := intValue(response["code"]); code != 0 {
		message := stringValue(response["message"])
		if message == "" {
			message = "unknown error"
		}
		return nil, protocolError(strings.ToUpper(strings.ReplaceAll(operation, " ", "_"))+"_FAILED", fmt.Sprintf("%s failed: code=%d %s", operation, code, message))
	}
	entity, ok := response["entity"].(map[string]any)
	if !ok {
		return nil, protocolError("MISSING_ENTITY", operation+" response is missing entity")
	}
	return entity, nil
}

func protocolError(code, message string) *ProtocolError {
	return &ProtocolError{Code: code, Message: message}
}

func compactJSONString(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func decodeJSON(data []byte, value any) error {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(value)
}

func encryptMPayParameters(data map[string]any, deviceKey string) (string, error) {
	key, err := hex.DecodeString(deviceKey)
	if err != nil {
		return "", protocolError("INVALID_DEVICE_KEY", "MPay device key is invalid")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	plain := pkcs7Pad([]byte(compactJSONString(data)), block.BlockSize())
	out := make([]byte, len(plain))
	for offset := 0; offset < len(plain); offset += block.BlockSize() {
		block.Encrypt(out[offset:offset+block.BlockSize()], plain[offset:offset+block.BlockSize()])
	}
	return hex.EncodeToString(out), nil
}

func encryptX19HTTP(body []byte) ([]byte, error) {
	paddedLength := ((len(body) + len(httpIV) + aes.BlockSize - 1) / aes.BlockSize) * aes.BlockSize
	plain := make([]byte, paddedLength)
	copy(plain, body)
	copy(plain[len(body):], httpIV)
	keyIndex, err := randomInt(len(httpKeys) - 1)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher([]byte(httpKeys[keyIndex]))
	if err != nil {
		return nil, err
	}
	encrypted := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, httpIV).CryptBlocks(encrypted, plain)
	result := make([]byte, 0, len(httpIV)+len(encrypted)+1)
	result = append(result, httpIV...)
	result = append(result, encrypted...)
	result = append(result, byte((keyIndex<<4)|2))
	return result, nil
}

func decryptX19HTTP(body []byte) ([]byte, error) {
	if len(body) < 18 || (len(body)-17)%aes.BlockSize != 0 {
		return nil, protocolError("INVALID_AUTH_RESPONSE", "authentication-otp returned invalid encrypted data")
	}
	keyIndex := int((body[len(body)-1] >> 4) & 0x0F)
	if keyIndex >= len(httpKeys) {
		return nil, protocolError("INVALID_AUTH_RESPONSE", "authentication-otp returned invalid key index")
	}
	block, err := aes.NewCipher([]byte(httpKeys[keyIndex]))
	if err != nil {
		return nil, err
	}
	plain := make([]byte, len(body)-17)
	cipher.NewCBCDecrypter(block, body[:16]).CryptBlocks(plain, body[16:len(body)-1])
	remaining := len(httpIV)
	position := len(plain) - 1
	for remaining > 0 && position >= 0 {
		if plain[position] != 0 {
			remaining--
		}
		position--
	}
	if remaining != 0 {
		return nil, protocolError("INVALID_AUTH_RESPONSE", "authentication-otp response is missing integrity marker")
	}
	return plain[:position+1], nil
}

func computeRequestToken(path, body, userID, userToken string) map[string]string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	tokenDigest := md5.Sum([]byte(userToken))
	tokenHex := hex.EncodeToString(tokenDigest[:])
	source := []byte(tokenHex + body + "0eGsBkhl" + path)
	digest := md5.Sum(source)
	digestHex := hex.EncodeToString(digest[:])
	bitString := strings.Builder{}
	for _, char := range []byte(digestHex) {
		bitString.WriteString(fmt.Sprintf("%08b", char))
	}
	bits := bitString.String()
	rotated := bits[6:] + bits[:6]
	tokenBytes := []byte(digestHex)
	for index := 0; index < len(rotated); index += 8 {
		value, _ := strconv.ParseUint(rotated[index:index+8], 2, 8)
		tokenBytes[index/8] ^= byte(value)
	}
	encoded := base64.StdEncoding.EncodeToString(tokenBytes[:12]) + "1"
	encoded = strings.ReplaceAll(encoded, "+", "m")
	encoded = strings.ReplaceAll(encoded, "/", "o")
	return map[string]string{"user-id": userID, "user-token": encoded}
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+padding)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(padding)
	}
	return out
}

func randomHex(size int) (string, error) {
	data := make([]byte, size)
	if _, err := crand.Read(data); err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func randomString(length int) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	out := make([]byte, length)
	for i := range out {
		index, err := randomInt(len(alphabet))
		if err != nil {
			return "", err
		}
		out[i] = alphabet[index]
	}
	return string(out), nil
}

func randomMAC(separator string) string {
	data := make([]byte, 6)
	_, _ = crand.Read(data)
	data[0] = (data[0] & 0xFE) | 0x02
	parts := make([]string, len(data))
	for index, item := range data {
		parts[index] = fmt.Sprintf("%02X", item)
	}
	return strings.Join(parts, separator)
}

func randomInt(max int) (int, error) {
	if max <= 0 {
		return 0, errors.New("max must be positive")
	}
	value, err := crand.Int(crand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0, err
	}
	return int(value.Int64()), nil
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		if typed == float64(int64(typed)) {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case float64:
		return int(typed)
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}
