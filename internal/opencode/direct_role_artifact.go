package opencode

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const directRoleArtifactRecord = ".gentle-ai-managed-direct-run.json"

type DirectRoleArtifactRecord struct {
	Schema      string `json:"schema"`
	Owner       string `json:"owner"`
	Kind        string `json:"kind"`
	Path        string `json:"path"`
	Mode        uint32 `json:"mode"`
	Fingerprint string `json:"fingerprint"`
}

func DirectRoleArtifactRecordPath(pluginsDir string) string {
	return filepath.Join(pluginsDir, directRoleArtifactRecord)
}

func WriteDirectRoleArtifactRecord(pluginsDir, pluginPath string, content []byte) error {
	record := DirectRoleArtifactRecord{Schema: "gentle-ai.opencode-direct-role-artifact/v1", Owner: ManagedOwner, Kind: "managed-direct-run-plugin", Path: pluginPath, Mode: 0o644, Fingerprint: artifactFingerprint(content)}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return os.WriteFile(DirectRoleArtifactRecordPath(pluginsDir), append(data, '\n'), 0o600)
}

func ReadDirectRoleArtifactRecord(pluginsDir string) (DirectRoleArtifactRecord, error) {
	data, err := os.ReadFile(DirectRoleArtifactRecordPath(pluginsDir))
	if err != nil {
		return DirectRoleArtifactRecord{}, err
	}
	var record DirectRoleArtifactRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return DirectRoleArtifactRecord{}, err
	}
	if record.Schema != "gentle-ai.opencode-direct-role-artifact/v1" || record.Owner != ManagedOwner || record.Kind != "managed-direct-run-plugin" || record.Path == "" || record.Fingerprint == "" {
		return DirectRoleArtifactRecord{}, fmt.Errorf("invalid direct-role artifact record")
	}
	return record, nil
}

func DirectRoleArtifactMatches(record DirectRoleArtifactRecord, path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != record.Mode || record.Path != path {
		return false
	}
	data, err := os.ReadFile(path)
	return err == nil && artifactFingerprint(data) == record.Fingerprint
}

func artifactFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
