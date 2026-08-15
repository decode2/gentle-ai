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

// ManagedDirectRunArtifactRefreshable proves both files are the exact managed
// artifact pair before sync is allowed to rewrite either one.
func ManagedDirectRunArtifactRefreshable(pluginsDir, pluginPath string) (bool, string) {
	recordPath := DirectRoleArtifactRecordPath(pluginsDir)
	launcherInfo, launcherErr := os.Lstat(pluginPath)
	recordInfo, recordErr := os.Lstat(recordPath)
	if os.IsNotExist(launcherErr) && os.IsNotExist(recordErr) {
		return false, "both launcher and sidecar are absent"
	}
	if launcherErr == nil && os.IsNotExist(recordErr) {
		return false, "launcher exists without ownership sidecar"
	}
	if os.IsNotExist(launcherErr) && recordErr == nil {
		return false, "sidecar exists without launcher"
	}
	if launcherErr != nil {
		return false, "launcher state is unreadable"
	}
	if recordErr != nil {
		return false, "sidecar state is unreadable"
	}
	if !launcherInfo.Mode().IsRegular() {
		return false, "launcher is not a regular file"
	}
	if launcherInfo.Mode().Perm() != 0o644 {
		return false, "launcher mode drift"
	}
	if !recordInfo.Mode().IsRegular() {
		return false, "sidecar is not a regular file"
	}
	if recordInfo.Mode().Perm() != 0o600 {
		return false, "sidecar mode drift"
	}
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return false, "sidecar is unreadable"
	}
	var record DirectRoleArtifactRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return false, "sidecar is malformed"
	}
	if record.Schema != "gentle-ai.opencode-direct-role-artifact/v1" {
		return false, "sidecar schema is invalid"
	}
	if record.Owner != ManagedOwner {
		return false, "sidecar owner does not match"
	}
	if record.Kind != "managed-direct-run-plugin" {
		return false, "sidecar kind does not match"
	}
	if record.Mode != 0o644 {
		return false, "sidecar declares an unexpected launcher mode"
	}
	if record.Path != pluginPath {
		return false, "sidecar path does not match launcher"
	}
	if record.Fingerprint == "" {
		return false, "sidecar fingerprint is missing"
	}
	if !DirectRoleArtifactMatches(record, pluginPath) {
		return false, "launcher fingerprint drift"
	}
	return true, ""
}

func artifactFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
