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
	"os"
	"path/filepath"
	"strings"
)

type NetGameModList struct {
	Mods []NetGameMod `json:"mods"`
}

type NetGameMod struct {
	ModPath string `json:"modPath"`
	Name    string `json:"name"`
	ID      string `json:"id"`
	IID     string `json:"iid"`
	MD5     string `json:"md5"`
	Version string `json:"version"`
}

type netGameCoreModResponse struct {
	ItemID      string                    `json:"item_id"`
	EntityID    string                    `json:"entity_id"`
	MTypeID     int                       `json:"mtypeid"`
	SubEntities []netGameCoreModSubEntity `json:"sub_entities"`
}

type netGameCoreModSubEntity struct {
	MCVersionName string `json:"mc_version_name"`
	ResURL        string `json:"res_url"`
	ResMD5        string `json:"res_md5"`
	ResName       string `json:"res_name"`
	JarMD5        string `json:"jar_md5"`
}

type netGameComponentResponse struct {
	ItemID      string                    `json:"item_id"`
	SubEntities []netGameCoreModSubEntity `json:"sub_entities"`
}

type ResourceProgress func(stage, message string, percent float64)

func (c *Client) InstallNetGameResources(ctx context.Context, gameID string, version Version, paths CachePaths, report ResourceProgress) (NetGameModList, error) {
	if err := ValidateNetGameID(gameID); err != nil {
		return NetGameModList{}, err
	}
	versionLabel, err := VersionLabel(version)
	if err != nil {
		return NetGameModList{}, err
	}
	modList := NetGameModList{Mods: []NetGameMod{}}
	coreMods, err := c.installCoreNetGameMods(ctx, gameID, version, paths, report)
	if err != nil {
		return NetGameModList{}, err
	}
	modList.Mods = append(modList.Mods, coreMods...)
	componentMods, err := c.installGameComponent(ctx, gameID, versionLabel, paths, report)
	if err != nil {
		return NetGameModList{}, err
	}
	modList.Mods = append(modList.Mods, componentMods...)
	return modList, nil
}

func (c *Client) installCoreNetGameMods(ctx context.Context, gameID string, version Version, paths CachePaths, report ResourceProgress) ([]NetGameMod, error) {
	entity, err := c.postSignedEntity(ctx, gatewayBaseURL, "/game-auth-item-list/query/search-by-game", map[string]any{
		"mc_version_id": uint32(version),
		"game_type":     netEaseNetGameType,
	})
	if err != nil {
		return nil, err
	}
	var search struct {
		IIDList []uint64 `json:"iid_list"`
	}
	if err := decodeMap(entity, &search); err != nil {
		return nil, fmt.Errorf("decode core mod list: %w", err)
	}
	if len(search.IIDList) == 0 {
		return []NetGameMod{}, nil
	}
	response, err := c.postSigned(ctx, gatewayBaseURL, "/user-item-download-v2/get-list", map[string]any{
		"item_id_list": search.IIDList,
	})
	if err != nil {
		return nil, err
	}
	entities, err := requireEntities(response, "core mod details")
	if err != nil {
		return nil, err
	}
	var details []netGameCoreModResponse
	if err := decodeValue(entities, &details); err != nil {
		return nil, fmt.Errorf("decode core mod details: %w", err)
	}
	coreRoot := filepath.Join(paths.GameMods, safePathSegment(gameID))
	if err := os.MkdirAll(coreRoot, 0o755); err != nil {
		return nil, err
	}
	var mods []NetGameMod
	total := 0
	for _, detail := range details {
		total += len(detail.SubEntities)
	}
	index := 0
	for _, detail := range details {
		for _, item := range detail.SubEntities {
			index++
			modPath := fmt.Sprintf("%s@%d@0.jar", detail.ItemID, detail.MTypeID)
			mods = append(mods, NetGameMod{
				ModPath: modPath, ID: modPath, IID: detail.ItemID,
				MD5: strings.ToUpper(strings.TrimSpace(item.JarMD5)),
			})
			if report != nil {
				report("download", fmt.Sprintf("下载网易核心模组 %d/%d", index, total), 10+float64(index)*25/float64(maxInt(total, 1)))
			}
			if strings.TrimSpace(item.ResURL) == "" || strings.TrimSpace(item.ResName) == "" {
				return nil, errors.New("core mod download metadata is incomplete")
			}
			jarName := strings.TrimSuffix(filepath.Base(item.ResName), filepath.Ext(item.ResName))
			target := filepath.Join(coreRoot, fmt.Sprintf("%s@%d@%s.jar", jarName, detail.MTypeID, detail.EntityID))
			if fileMatchesMD5(target, item.JarMD5) {
				continue
			}
			archive := filepath.Join(coreRoot, filepath.Base(item.ResName))
			if err := c.downloadResource(ctx, item.ResURL, archive, item.ResMD5, nil); err != nil {
				return nil, fmt.Errorf("download core mod %s: %w", item.ResName, err)
			}
			extractDir := filepath.Join(coreRoot, ".extract-"+safePathSegment(detail.EntityID))
			if err := os.RemoveAll(extractDir); err != nil {
				return nil, err
			}
			if err := os.MkdirAll(extractDir, 0o755); err != nil {
				return nil, err
			}
			if err := extractResourceArchive(ctx, archive, extractDir); err != nil {
				return nil, fmt.Errorf("extract core mod %s: %w", item.ResName, err)
			}
			jars, err := filesWithExtension(extractDir, ".jar")
			if err != nil {
				return nil, err
			}
			if len(jars) == 0 {
				return nil, fmt.Errorf("core mod archive contains no jar: %s", item.ResName)
			}
			for _, jar := range jars {
				if err := copyFile(jar, target); err != nil {
					return nil, err
				}
			}
			_ = os.RemoveAll(extractDir)
		}
	}
	return mods, nil
}

func (c *Client) installGameComponent(ctx context.Context, gameID, versionLabel string, paths CachePaths, report ResourceProgress) ([]NetGameMod, error) {
	entity, err := c.postSignedEntity(ctx, clientBaseURL, "/user-item-download-v2", map[string]any{
		"item_id": gameID, "length": 0, "offset": 0,
	})
	if err != nil {
		return nil, err
	}
	var response netGameComponentResponse
	if err := decodeMap(entity, &response); err != nil {
		return nil, fmt.Errorf("decode game component: %w", err)
	}
	var component *netGameCoreModSubEntity
	for index := range response.SubEntities {
		item := &response.SubEntities[index]
		if item.ResName != "" && strings.TrimSpace(item.MCVersionName) == versionLabel {
			component = item
			break
		}
	}
	if component == nil {
		for index := range response.SubEntities {
			item := &response.SubEntities[index]
			if item.ResName != "" {
				component = item
				break
			}
		}
	}
	if component == nil {
		return []NetGameMod{}, nil
	}
	componentRoot := filepath.Join(paths.Game, safePathSegment(gameID))
	marker := filepath.Join(componentRoot, gameID+".MD5")
	modInfoPath := filepath.Join(componentRoot, gameID+".json")
	if markerMatches(marker, component.ResMD5) && fileExists(modInfoPath) {
		var cached NetGameModList
		if contents, readErr := os.ReadFile(modInfoPath); readErr == nil && json.Unmarshal(contents, &cached) == nil {
			return cached.Mods, nil
		}
	}
	if report != nil {
		report("download", "下载网络游戏专属资源", 40)
	}
	if err := os.MkdirAll(paths.Game, 0o755); err != nil {
		return nil, err
	}
	archive := filepath.Join(paths.Game, safePathSegment(gameID)+".7z")
	if err := c.downloadResource(ctx, component.ResURL, archive, component.ResMD5, nil); err != nil {
		return nil, fmt.Errorf("download game component: %w", err)
	}
	if report != nil {
		report("extract", "解压网络游戏专属资源", 55)
	}
	extractRoot := filepath.Join(paths.Game, ".component-extract-"+safePathSegment(gameID))
	if err := os.RemoveAll(extractRoot); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(extractRoot, 0o755); err != nil {
		return nil, err
	}
	if err := extractResourceArchive(ctx, archive, extractRoot); err != nil {
		return nil, fmt.Errorf("extract game component: %w", err)
	}
	// Some packages contain {gameID}/.minecraft, while others extract
	// directly from .minecraft. Normalize both layouts into Game/{gameID}.
	extractedRoot := filepath.Join(extractRoot, gameID)
	if !directoryExists(filepath.Join(extractedRoot, ".minecraft")) {
		extractedRoot = extractRoot
	}
	if !directoryExists(filepath.Join(extractedRoot, ".minecraft")) {
		return nil, fmt.Errorf("game component does not contain .minecraft: %s", component.ResName)
	}
	if err := os.RemoveAll(componentRoot); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(componentRoot, 0o755); err != nil {
		return nil, err
	}
	if err := copyDirectory(extractedRoot, componentRoot); err != nil {
		return nil, err
	}
	if err := os.RemoveAll(extractRoot); err != nil {
		return nil, err
	}
	modsRoot := filepath.Join(componentRoot, ".minecraft", "mods")
	pathsList, err := filesWithExtension(modsRoot, ".jar")
	if err != nil {
		return nil, err
	}
	mods := make([]NetGameMod, 0, len(pathsList))
	for _, path := range pathsList {
		name := filepath.Base(path)
		iid := strings.SplitN(name, "@", 2)[0]
		mods = append(mods, NetGameMod{ModPath: name, ID: name, IID: iid, MD5: fileMD5(path)})
	}
	encoded, err := json.Marshal(NetGameModList{Mods: mods})
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(marker, []byte(strings.TrimSpace(component.ResMD5)), 0o644); err != nil {
		return nil, err
	}
	if err := os.WriteFile(modInfoPath, encoded, 0o644); err != nil {
		return nil, err
	}
	return mods, nil
}

func (c *Client) downloadResource(ctx context.Context, url, destination, expectedMD5 string, progress func(current, total int64)) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", x19UserAgent)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("download returned %s", response.Status)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	tmp := destination + ".part"
	file, err := os.Create(tmp)
	if err != nil {
		return err
	}
	hash := md5.New()
	var reader io.Reader = response.Body
	if progress != nil {
		reader = &progressReader{reader: response.Body, total: response.ContentLength, progress: func(_ string, current, total int64) {
			progress(current, total)
		}}
	}
	_, copyErr := io.Copy(io.MultiWriter(file, hash), reader)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if expectedMD5 != "" && !strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), strings.TrimSpace(expectedMD5)) {
		_ = os.Remove(tmp)
		return fmt.Errorf("md5 mismatch for %s", filepath.Base(destination))
	}
	if err := os.Remove(destination); err != nil && !os.IsNotExist(err) {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, destination)
}

func extractResourceArchive(ctx context.Context, archive, target string) error {
	if strings.EqualFold(filepath.Ext(archive), ".zip") {
		return extractZip(archive, target)
	}
	return extractSevenZip(ctx, archive, target)
}

func filesWithExtension(root, extension string) ([]string, error) {
	if !directoryExists(root) {
		return []string{}, nil
	}
	var files []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type().IsRegular() && strings.EqualFold(filepath.Ext(entry.Name()), extension) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

func markerMatches(path, expected string) bool {
	if strings.TrimSpace(expected) == "" {
		return false
	}
	contents, err := os.ReadFile(path)
	return err == nil && strings.EqualFold(strings.TrimSpace(string(contents)), strings.TrimSpace(expected))
}

func fileMatchesMD5(path, expected string) bool {
	return strings.TrimSpace(expected) != "" && fileExists(path) && strings.EqualFold(fileMD5(path), strings.TrimSpace(expected))
}

func fileMD5(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return ""
	}
	return strings.ToUpper(hex.EncodeToString(hash.Sum(nil)))
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
