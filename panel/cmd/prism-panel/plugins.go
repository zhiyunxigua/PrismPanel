package main

import (
	"context"
	"encoding/json"
	"fmt"

	panelplugins "PrismPanel/internal/plugins"
	"PrismPanel/internal/store"
)

func syncPluginCatalog(ctx context.Context, repository *store.Store, catalog []panelplugins.Plugin) error {
	artifacts := make([]store.PluginArtifactIndex, 0)
	for _, plugin := range catalog {
		for _, artifact := range plugin.Artifacts {
			manifest, err := json.Marshal(artifact)
			if err != nil {
				return fmt.Errorf("encode plugin %s artifact %d: %w", plugin.PluginID, artifact.ArtifactID, err)
			}
			artifacts = append(artifacts, store.PluginArtifactIndex{
				PluginType: plugin.PluginType, PluginID: plugin.PluginID, ArtifactID: artifact.ArtifactID,
				PluginName: artifact.Name, Version: artifact.Version, MainClass: artifact.Main,
				JARSHA256: artifact.Artifact.SHA256, ConfigSHA256: artifact.Config.SHA256,
				Current:      artifact.ArtifactID == plugin.CurrentArtifactID,
				ManifestJSON: manifest, UploadedAt: artifact.UploadedAt,
			})
		}
	}
	if err := repository.ReplacePluginCatalog(ctx, artifacts); err != nil {
		return fmt.Errorf("rebuild plugin catalog index: %w", err)
	}
	return nil
}
