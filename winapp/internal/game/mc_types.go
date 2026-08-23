package game

import "time"

// MCAuthMode 国际版账号模式：离线 / 微软账号 / 第三方认证服务器。
type MCAuthMode string

const (
	MCAuthOffline    MCAuthMode = "offline"
	MCAuthMicrosoft  MCAuthMode = "microsoft"
	MCAuthThirdParty MCAuthMode = "third_party"
)

// MCAccount 国际版启动账号（本地保存，仅微软刷新令牌与离线名）。
type MCAccount struct {
	Mode         MCAuthMode `json:"mode"`
	Name         string     `json:"name"`
	UUID         string     `json:"uuid"`
	AccessToken  string     `json:"access_token"`
	RefreshToken string     `json:"refresh_token"`
	ClientToken  string     `json:"client_token,omitempty"` // 第三方认证服务器 clientToken
	AuthServer   string     `json:"auth_server,omitempty"`  // 第三方认证服务器地址（authlib-injector）
	ExpiresAt    time.Time  `json:"expires_at,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type MCAccountSummary struct {
	Mode      MCAuthMode `json:"mode"`
	Name      string     `json:"name"`
	UUID      string     `json:"uuid"`
	HasToken  bool       `json:"has_token"`
	UpdatedAt time.Time  `json:"updated_at"`
}

func (a MCAccount) Summary() MCAccountSummary {
	return MCAccountSummary{
		Mode: a.Mode, Name: a.Name, UUID: a.UUID, HasToken: a.AccessToken != "",
		UpdatedAt: a.UpdatedAt,
	}
}

// MCDeviceLogin 微软设备码登录的中间状态（用于前端展示用户码）。
type MCDeviceLogin struct {
	StateID         string `json:"state_id"`
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
	Message         string `json:"message"`
}

type MCVersionEntry struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	ReleaseTime string `json:"release_time"`
}

type MCFabricLoader struct {
	Loader struct {
		Version string `json:"version"`
	} `json:"loader"`
	Game string `json:"game"`
}

type MCInstalledVersion struct {
	ID        string `json:"id"`
	Fabric    bool   `json:"fabric"`
	Installed bool   `json:"installed"`
}

// MCLaunchRequest 国际版启动请求（前端 → WinApp）。
type MCLaunchRequest struct {
	ServerIP    string `json:"server_ip"`
	ServerPort  int    `json:"server_port"`
	InstanceDir string `json:"instance_dir"`
	VersionID   string `json:"version_id"`
	Fabric      bool   `json:"fabric"`
	MaxMemoryMB int    `json:"max_memory_mb"`
	JVMArgs     string `json:"jvm_args"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
}

func (r MCLaunchRequest) normalized() MCLaunchRequest {
	if r.ServerIP == "" {
		r.ServerIP = "127.0.0.1"
	}
	if r.ServerPort <= 0 {
		r.ServerPort = 25565
	}
	if r.MaxMemoryMB <= 0 {
		r.MaxMemoryMB = 2048
	}
	return r
}

// MCVersionSettings 版本特定设置（每版本独立，保存在 minecraft/<版本>/settings.json）。
type MCVersionSettings struct {
	ServerIP      string `json:"server_ip"`
	ServerPort    int    `json:"server_port"`
	MaxMemoryMB   int    `json:"max_memory_mb"`
	InstanceDir   string `json:"instance_dir"`
	JVMArgs       string `json:"jvm_args"`
	UseFabric     bool   `json:"use_fabric"`     // 基础版本自动使用其 Fabric 子版本启动
	LaunchVersion string `json:"launch_version"` // 显式指定启动的版本 id（如 fabric-loader-...），优先于 UseFabric
	JavaPath      string `json:"java_path"`      // 指定 Java 可执行文件路径（留空用默认/自动）
	Width         int    `json:"width"`          // 窗口宽度（0 = 不设置）
	Height        int    `json:"height"`         // 窗口高度（0 = 不设置）
}
