package opencode

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gentleman-programming/gentle-ai/v2/internal/components/mutationjournal"
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

func WriteManagedDirectRunArtifact(pluginsDir, pluginPath string, content []byte) (bool, error) {
	journal := mutationjournal.New(pluginsDir)
	changed, err := WriteManagedDirectRunArtifactWithJournal(journal, pluginsDir, pluginPath, content, nil)
	if err != nil {
		return false, fmt.Errorf("write managed direct-run artifact: %w (rollback: %v)", err, journal.Restore())
	}
	return changed, nil
}

// WriteManagedDirectRunArtifactWithJournal couples launcher and ownership
// sidecar writes to the caller's wider direct-role transaction.
func WriteManagedDirectRunArtifactWithJournal(journal *mutationjournal.Journal, pluginsDir, pluginPath string, content []byte, afterLauncher func() error) (bool, error) {
	beforePlugin, pluginErr := os.ReadFile(pluginPath)
	beforeRecord, recordErr := os.ReadFile(DirectRoleArtifactRecordPath(pluginsDir))
	if _, err := journal.WriteWithMode(pluginPath, content, 0o644); err != nil {
		return false, err
	}
	if afterLauncher != nil {
		if err := afterLauncher(); err != nil {
			return false, err
		}
	}
	record := DirectRoleArtifactRecord{Schema: "gentle-ai.opencode-direct-role-artifact/v1", Owner: ManagedOwner, Kind: "managed-direct-run-plugin", Path: pluginPath, Mode: 0o644, Fingerprint: artifactFingerprint(content)}
	data, err := json.Marshal(record)
	if err != nil {
		return false, err
	}
	recordData := append(data, '\n')
	if _, err := journal.WriteWithMode(DirectRoleArtifactRecordPath(pluginsDir), recordData, 0o600); err != nil {
		return false, fmt.Errorf("write direct-role artifact record: %w", err)
	}
	return pluginErr != nil || recordErr != nil || !bytes.Equal(beforePlugin, content) || !bytes.Equal(beforeRecord, recordData), nil
}

// RemoveManagedDirectRunArtifactWithJournalAfterLauncher permits a caller's
// transaction-local checkpoint between launcher and sidecar removal.
func RemoveManagedDirectRunArtifactWithJournalAfterLauncher(journal *mutationjournal.Journal, pluginsDir, pluginPath string, afterLauncher func() error) error {
	if _, err := journal.Remove(pluginPath); err != nil {
		return err
	}
	if afterLauncher != nil {
		if err := afterLauncher(); err != nil {
			return err
		}
	}
	if _, err := journal.Remove(DirectRoleArtifactRecordPath(pluginsDir)); err != nil {
		return fmt.Errorf("remove direct-role artifact record: %w", err)
	}
	return nil
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
