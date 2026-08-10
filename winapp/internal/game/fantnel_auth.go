package game

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

const fantnelAuthenticatedEndpoint = "http://110.42.70.32:13423"

type joinServerProfile struct {
	GameID       string         `json:"gameId"`
	GameVersion  string         `json:"gameVersion"`
	BootstrapMD5 string         `json:"bootstrapMd5"`
	DatFileMD5   string         `json:"datFileMd5"`
	Mods         NetGameModList `json:"mods"`
	Profile      struct {
		User struct {
			UserID string `json:"userId"`
			Token  string `json:"token"`
		} `json:"user"`
	} `json:"profile"`
}

type FantnelAuthenticator struct {
	Endpoint   string
	HTTPClient *http.Client
}

func NewFantnelAuthenticator() *FantnelAuthenticator {
	return &FantnelAuthenticator{
		Endpoint:   fantnelAuthenticatedEndpoint,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *FantnelAuthenticator) Authenticate(ctx context.Context, gameID, userID, serverID string, profile LaunchProfile, account AccountState, baseMC string) (bool, error) {
	if a == nil {
		return false, errors.New("Fantnel authenticator is not configured")
	}
	if strings.TrimSpace(gameID) == "" || strings.TrimSpace(serverID) == "" {
		return false, errors.New("join-server game id and server id are required")
	}
	versionLabel := strings.TrimSpace(profile.VersionLabel)
	if versionLabel == "" {
		return false, errors.New("join-server game version is required")
	}
	md5Pair, err := md5PairForVersion(versionLabel, baseMC)
	if err != nil {
		return false, err
	}
	var mods NetGameModList
	if strings.TrimSpace(profile.ModInfo) != "" {
		if err := json.Unmarshal([]byte(profile.ModInfo), &mods); err != nil {
			return false, fmt.Errorf("decode join-server mod info: %w", err)
		}
	}
	payload := joinServerProfile{
		GameID: gameID, GameVersion: versionLabel, BootstrapMD5: md5Pair.BootstrapMD5,
		DatFileMD5: md5Pair.DatFileMD5, Mods: mods,
	}
	payload.Profile.User.UserID = account.UserID
	if payload.Profile.User.UserID == "" {
		payload.Profile.User.UserID = userID
	}
	payload.Profile.User.Token = account.UserToken
	if payload.Profile.User.UserID == "" || payload.Profile.User.Token == "" {
		return false, errors.New("NetEase account credentials are missing")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	endpoint := strings.TrimRight(strings.TrimSpace(a.Endpoint), "/")
	if endpoint == "" {
		return false, errors.New("Fantnel authenticated endpoint is empty")
	}
	target, err := url.Parse(endpoint)
	if err != nil {
		return false, err
	}
	target.Path = path.Join(target.Path, "/api/fantnel/authenticated")
	query := target.Query()
	query.Set("id", serverID)
	target.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	request.Header.Set("User-Agent", x19UserAgent)
	request.Header.Set("Content-Type", "application/json")
	client := a.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024))
	if err != nil {
		return false, fmt.Errorf("read Fantnel authenticated response: %w", err)
	}
	var result struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return false, fmt.Errorf("decode Fantnel authenticated response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if strings.TrimSpace(result.Msg) != "" {
			return false, fmt.Errorf("Fantnel authenticated returned %s: %s", response.Status, result.Msg)
		}
		return false, fmt.Errorf("Fantnel authenticated returned %s", response.Status)
	}
	if result.Code != 1 {
		if result.Msg == "" {
			result.Msg = "Fantnel authenticated rejected the server"
		}
		return false, errors.New(result.Msg)
	}
	return true, nil
}

type md5Pair struct {
	BootstrapMD5 string
	DatFileMD5   string
}

var netEaseMD5Mapping = map[string]md5Pair{
	"1.7.10": {BootstrapMD5: "A895FE657915D58F55919CEACD30209D", DatFileMD5: "538D33D5F35EF01736EDA30F94C61DF6"},
	"1.8.9":  {BootstrapMD5: "A895FE657915D58F55919CEACD30209D", DatFileMD5: "0CF2074AA7D4B543E35A3D6BB57AF861"},
	"1.12.2": {BootstrapMD5: "A895FE657915D58F55919CEACD30209D", DatFileMD5: "51581ADD89B8AC5A0D8CCDD0E33EE1DE"},
	"1.16":   {BootstrapMD5: "7B101583C3965371B89A3C9115B27526", DatFileMD5: "B0712F34B0A584D05D9D29FA68759E29"},
	"1.18":   {BootstrapMD5: "C3BD2115F23F6FE4B2ADCC7FC4DEFFEA", DatFileMD5: "56677A2BB31E18246FA241FB02E16D0E"},
	"1.20":   {BootstrapMD5: "2A7A476411A1687A56DC6848829C1AE4", DatFileMD5: "D285CBF97D9BA30D3C445DBF1C342634"},
	"1.21":   {BootstrapMD5: "684528BF492A84489F825F5599B3E1C6", DatFileMD5: "574033E7E4841D8AC4C14D7FA5E05337"},
	"1.21.8": {BootstrapMD5: "5BF6153C69DD28951A699F7F834EFE1A", DatFileMD5: "C9906B5809A92C73299279E562A78D81"},
}

func md5PairForVersion(versionLabel, baseMC string) (md5Pair, error) {
	pair := netEaseMD5Mapping[versionLabel]
	if value := bootstrapMD5ForVersion(versionLabel, baseMC); value != "" {
		pair.BootstrapMD5 = value
	}
	datPath := filepath.Join(baseMC, "versions", versionLabel, versionLabel+".dat")
	if value := fileMD5(datPath); value != "" {
		pair.DatFileMD5 = value
	}
	if pair.BootstrapMD5 == "" || pair.DatFileMD5 == "" {
		return md5Pair{}, fmt.Errorf("NetEase join-server MD5 values are unavailable for %s", versionLabel)
	}
	return pair, nil
}

func bootstrapMD5ForVersion(versionLabel, baseMC string) string {
	metadataPath := filepath.Join(baseMC, "versions", versionLabel, versionLabel+".json")
	contents, err := os.ReadFile(metadataPath)
	if err != nil {
		return ""
	}
	var metadata struct {
		Libraries []struct {
			Name      string `json:"name"`
			Downloads struct {
				Artifact struct {
					Path string `json:"path"`
				} `json:"artifact"`
			} `json:"downloads"`
		} `json:"libraries"`
	}
	if json.Unmarshal(contents, &metadata) != nil {
		return ""
	}
	for _, library := range metadata.Libraries {
		artifactPath := strings.TrimSpace(library.Downloads.Artifact.Path)
		if !strings.Contains(strings.ToLower(library.Name+" "+artifactPath), "bootstraplauncher") {
			continue
		}
		cleanPath, err := cleanArchivePath(artifactPath)
		if err != nil {
			return ""
		}
		return fileMD5(filepath.Join(baseMC, "libraries", filepath.FromSlash(cleanPath)))
	}
	return ""
}
