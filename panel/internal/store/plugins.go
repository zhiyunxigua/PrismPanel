package store

import (
	"context"
	"fmt"
	"time"
)

type PluginArtifactIndex struct {
	PluginType   string
	PluginID     string
	ArtifactID   int64
	PluginName   string
	Version      string
	MainClass    string
	JARSHA256    string
	ConfigSHA256 string
	Current      bool
	ManifestJSON []byte
	UploadedAt   time.Time
}

// ReplacePluginCatalog rebuilds the disposable database index from the local repository.
func (s *Store) ReplacePluginCatalog(ctx context.Context, artifacts []PluginArtifactIndex) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin plugin catalog rebuild: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM plugin_artifacts_v2"); err != nil {
		return fmt.Errorf("clear plugin catalog: %w", err)
	}
	statement := "INSERT INTO plugin_artifacts_v2 " +
		"(plugin_type, plugin_id, artifact_id, plugin_name, version, main_class, jar_sha256, " +
		"config_sha256, current_artifact, manifest, uploaded_at) " +
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	for _, artifact := range artifacts {
		if _, err := tx.ExecContext(ctx, statement,
			artifact.PluginType, artifact.PluginID, artifact.ArtifactID, artifact.PluginName, artifact.Version,
			artifact.MainClass, artifact.JARSHA256, artifact.ConfigSHA256, artifact.Current,
			artifact.ManifestJSON, artifact.UploadedAt); err != nil {
			return fmt.Errorf("index plugin %s artifact %d: %w", artifact.PluginID, artifact.ArtifactID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit plugin catalog rebuild: %w", err)
	}
	return nil
}
