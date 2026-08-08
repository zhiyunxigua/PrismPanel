package game

import "strings"

type LaunchKind string

const (
	LaunchKindLocal   LaunchKind = "local"
	LaunchKindNetGame LaunchKind = "net_game"
)

const (
	localLauncherGameIDValue = "0"
	netEaseNetGameType       = 2
)

type LaunchProfile struct {
	Kind                 LaunchKind `json:"kind"`
	InstanceID           string     `json:"instance_id"`
	GameName             string     `json:"game_name"`
	GameID               string     `json:"game_id"`
	GameType             int        `json:"game_type"`
	RoleName             string     `json:"role_name"`
	ServerIP             string     `json:"server_ip"`
	ServerPort           int        `json:"server_port"`
	Version              Version    `json:"version"`
	VersionLabel         string     `json:"version_label"`
	ProtocolVersion      string     `json:"protocol_version"`
	UseCustomResourceDir bool       `json:"use_custom_resource_dir"`
	ModInfo              string     `json:"mod_info,omitempty"`
	CRCSalt              string     `json:"crc_salt,omitempty"`
}

func NewLocalLaunchProfile(server ServerConfig, protocolVersion string) LaunchProfile {
	return newLaunchProfile(LaunchKindLocal, server, protocolVersion, localLauncherGameIDValue, true)
}

func NewNetGameLaunchProfile(server ServerConfig, protocolVersion string) LaunchProfile {
	return newLaunchProfile(
		LaunchKindNetGame,
		server,
		protocolVersion,
		strings.TrimSpace(server.GameID),
		strings.TrimSpace(server.ModDir) != "",
	)
}

func newLaunchProfile(kind LaunchKind, server ServerConfig, protocolVersion, gameID string, useCustomResourceDir bool) LaunchProfile {
	if strings.TrimSpace(gameID) == "" {
		gameID = localLauncherGameIDValue
	}
	port := server.Port
	if port <= 0 {
		port = 25565
	}
	ip := strings.TrimSpace(server.IP)
	if ip == "" {
		ip = "127.0.0.1"
	}
	gameType := netEaseNetGameType
	return LaunchProfile{
		Kind: kind, InstanceID: server.ID, GameName: server.Name, GameID: gameID, GameType: gameType,
		RoleName: server.Username, ServerIP: ip, ServerPort: port, Version: server.Version,
		VersionLabel: server.VersionLabel, ProtocolVersion: strings.TrimSpace(protocolVersion),
		UseCustomResourceDir: useCustomResourceDir,
	}
}

func (p LaunchProfile) LauncherGameID() string {
	if value := strings.TrimSpace(p.GameID); value != "" {
		return value
	}
	return localLauncherGameIDValue
}

func (p LaunchProfile) normalized(server ServerConfig, protocolVersion string) LaunchProfile {
	if p.Kind == "" {
		return NewLocalLaunchProfile(server, protocolVersion)
	}
	if p.InstanceID == "" {
		p.InstanceID = server.ID
	}
	if strings.TrimSpace(p.GameID) == "" {
		p.GameID = localLauncherGameIDValue
	}
	if p.GameType == 0 {
		p.GameType = netEaseNetGameType
	}
	if strings.TrimSpace(p.RoleName) == "" {
		p.RoleName = server.Username
	}
	if strings.TrimSpace(p.ServerIP) == "" {
		p.ServerIP = server.IP
	}
	if p.ServerPort <= 0 {
		p.ServerPort = server.Port
	}
	if p.ServerPort <= 0 {
		p.ServerPort = 25565
	}
	if p.Version == VersionBase {
		p.Version = server.Version
	}
	if strings.TrimSpace(p.VersionLabel) == "" {
		p.VersionLabel = server.VersionLabel
	}
	if strings.TrimSpace(p.ProtocolVersion) == "" {
		p.ProtocolVersion = strings.TrimSpace(protocolVersion)
	}
	return p
}
