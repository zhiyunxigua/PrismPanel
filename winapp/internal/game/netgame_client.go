package game

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const maxNetGameCharacters = 3

var netGameIDPattern = regexp.MustCompile("^46[0-9]+$")

type NetGameDetail struct {
	GameID       string   `json:"game_id"`
	Name         string   `json:"name"`
	Version      Version  `json:"version"`
	VersionLabel string   `json:"version_label"`
	Versions     []string `json:"versions"`
}

type NetGameAddress struct {
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

type GameCharacter struct {
	GameID     string `json:"game_id"`
	GameType   int    `json:"game_type"`
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	CreateTime int    `json:"create_time"`
	ExpireTime int    `json:"expire_time"`
}

type NetGameLaunchOptions struct {
	Detail     NetGameDetail   `json:"detail"`
	Address    NetGameAddress  `json:"address"`
	Characters []GameCharacter `json:"characters"`
}

func ValidateNetGameID(gameID string) error {
	gameID = strings.TrimSpace(gameID)
	if !netGameIDPattern.MatchString(gameID) {
		return errors.New("network game id must be a numeric id starting with 46")
	}
	return nil
}

func (c *Client) FetchNetGameDetail(ctx context.Context, gameID string) (NetGameDetail, error) {
	gameID = strings.TrimSpace(gameID)
	if err := ValidateNetGameID(gameID); err != nil {
		return NetGameDetail{}, err
	}
	entity, err := c.postSignedEntity(ctx, gatewayBaseURL, "/item-details/get_v2", map[string]any{"item_id": gameID})
	if err != nil {
		return NetGameDetail{}, err
	}
	var response struct {
		Name          string `json:"name"`
		EntityID      string `json:"entity_id"`
		MCVersionList []struct {
			Name string `json:"name"`
		} `json:"mc_version_list"`
	}
	if err := decodeMap(entity, &response); err != nil {
		return NetGameDetail{}, fmt.Errorf("decode network game details: %w", err)
	}
	if len(response.MCVersionList) == 0 || strings.TrimSpace(response.MCVersionList[0].Name) == "" {
		return NetGameDetail{}, errors.New("network game details do not contain a game version")
	}
	versionLabel := strings.TrimSpace(response.MCVersionList[0].Name)
	version, err := VersionFromLabel(versionLabel)
	if err != nil {
		return NetGameDetail{}, err
	}
	versions := make([]string, 0, len(response.MCVersionList))
	for _, item := range response.MCVersionList {
		if value := strings.TrimSpace(item.Name); value != "" {
			versions = append(versions, value)
		}
	}
	if strings.TrimSpace(response.EntityID) != "" {
		gameID = strings.TrimSpace(response.EntityID)
	}
	return NetGameDetail{
		GameID: gameID, Name: strings.TrimSpace(response.Name), Version: version,
		VersionLabel: versionLabel, Versions: versions,
	}, nil
}

func (c *Client) FetchNetGameAddress(ctx context.Context, gameID string) (NetGameAddress, error) {
	gameID = strings.TrimSpace(gameID)
	if err := ValidateNetGameID(gameID); err != nil {
		return NetGameAddress{}, err
	}
	entity, err := c.postSignedEntity(ctx, gatewayBaseURL, "/item-address/get", map[string]any{"item_id": gameID})
	if err != nil {
		return NetGameAddress{}, err
	}
	address := NetGameAddress{IP: strings.TrimSpace(stringValue(entity["ip"])), Port: intValue(entity["port"])}
	if address.Port <= 0 {
		address.Port = 25565
	}
	if address.IP == "" {
		return NetGameAddress{}, errors.New("network game address is empty")
	}
	return address, nil
}

func (c *Client) FetchGameCharacters(ctx context.Context, gameID string) ([]GameCharacter, error) {
	gameID = strings.TrimSpace(gameID)
	if err := ValidateNetGameID(gameID); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.userID) == "" {
		return nil, errors.New("NetEase account user id is missing")
	}
	response, err := c.postSigned(ctx, gatewayBaseURL, "/game-character/query/user-game-characters", map[string]any{
		"offset":    0,
		"length":    10,
		"user_id":   c.userID,
		"game_id":   gameID,
		"game_type": "2",
	})
	if err != nil {
		return nil, err
	}
	entities, err := requireEntities(response, "game characters")
	if err != nil {
		return nil, err
	}
	var characters []GameCharacter
	if err := decodeValue(entities, &characters); err != nil {
		return nil, fmt.Errorf("decode game characters: %w", err)
	}
	return characters, nil
}

func (c *Client) EnsureGameCharacter(ctx context.Context, gameID, roleName string) (GameCharacter, error) {
	roleName = strings.TrimSpace(roleName)
	if roleName == "" {
		return GameCharacter{}, errors.New("role username is required")
	}
	characters, err := c.FetchGameCharacters(ctx, gameID)
	if err != nil {
		return GameCharacter{}, err
	}
	if character, ok := findGameCharacter(characters, roleName); ok {
		return character, nil
	}
	if len(characters) >= maxNetGameCharacters {
		return GameCharacter{}, fmt.Errorf("NetEase game character limit reached (%d); delete a character before creating %q", maxNetGameCharacters, roleName)
	}
	response, err := c.postSigned(ctx, gatewayBaseURL, "/game-character", map[string]any{
		"game_id": strings.TrimSpace(gameID), "game_type": netEaseNetGameType,
		"user_id": c.userID, "name": roleName, "create_time": 555555,
	})
	if err != nil {
		return GameCharacter{}, err
	}
	if err := requireSuccess(response, "create game character"); err != nil {
		return GameCharacter{}, err
	}
	characters, err = c.FetchGameCharacters(ctx, gameID)
	if err != nil {
		return GameCharacter{}, err
	}
	if character, ok := findGameCharacter(characters, roleName); ok {
		return character, nil
	}
	return GameCharacter{}, fmt.Errorf("NetEase did not return the newly created role %q", roleName)
}

func (c *Client) DeleteGameCharacter(ctx context.Context, gameID, roleName string) ([]GameCharacter, error) {
	gameID = strings.TrimSpace(gameID)
	if err := ValidateNetGameID(gameID); err != nil {
		return nil, err
	}
	roleName = strings.TrimSpace(roleName)
	if roleName == "" {
		return nil, errors.New("role username is required")
	}
	characters, err := c.FetchGameCharacters(ctx, gameID)
	if err != nil {
		return nil, err
	}
	character, ok := findGameCharacter(characters, roleName)
	if !ok {
		return nil, fmt.Errorf("NetEase game character not found: %s", roleName)
	}
	gameType := character.GameType
	if gameType == 0 {
		gameType = netEaseNetGameType
	}
	response, err := c.deleteSigned(ctx, gatewayBaseURL, "/game-character", map[string]any{
		"game_id":     gameID,
		"game_type":   gameType,
		"user_id":     c.userID,
		"name":        character.Name,
		"create_time": character.CreateTime,
		"expire_time": character.ExpireTime,
	})
	if err != nil {
		return nil, err
	}
	if err := requireSuccess(response, "delete game character"); err != nil {
		return nil, err
	}
	return c.FetchGameCharacters(ctx, gameID)
}

func (c *Client) FetchNetGameLaunchOptions(ctx context.Context, gameID string) (NetGameLaunchOptions, error) {
	detail, err := c.FetchNetGameDetail(ctx, gameID)
	if err != nil {
		return NetGameLaunchOptions{}, err
	}
	characters, err := c.FetchGameCharacters(ctx, detail.GameID)
	if err != nil {
		return NetGameLaunchOptions{}, err
	}
	address, _ := c.FetchNetGameAddress(ctx, detail.GameID)
	return NetGameLaunchOptions{Detail: detail, Address: address, Characters: characters}, nil
}

func findGameCharacter(characters []GameCharacter, roleName string) (GameCharacter, bool) {
	for _, character := range characters {
		if character.Name == roleName {
			return character, true
		}
	}
	return GameCharacter{}, false
}

func requireEntities(response map[string]any, operation string) (any, error) {
	if err := requireSuccess(response, operation); err != nil {
		return nil, err
	}
	entities, ok := response["entities"]
	if !ok {
		return nil, protocolError("MISSING_ENTITIES", operation+" response is missing entities")
	}
	return entities, nil
}

func requireSuccess(response map[string]any, operation string) error {
	if code := intValue(response["code"]); code != 0 {
		message := stringValue(response["message"])
		if message == "" {
			message = stringValue(response["msg"])
		}
		if message == "" {
			message = "unknown error"
		}
		return protocolError(strings.ToUpper(strings.ReplaceAll(operation, " ", "_"))+"_FAILED", fmt.Sprintf("%s failed: code=%d %s", operation, code, message))
	}
	return nil
}

func decodeMap(source map[string]any, target any) error {
	return decodeValue(source, target)
}

func decodeValue(source, target any) error {
	encoded, err := json.Marshal(source)
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}
