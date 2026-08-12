package sdd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/gentleman-programming/gentle-ai/v2/internal/components/filemerge"
	"github.com/gentleman-programming/gentle-ai/v2/internal/opencode"
)

type ProfileOwnershipConflict struct {
	Key            string
	Classification opencode.OwnershipClassification
}
type ProfileOwnershipReport struct {
	Preserved []ProfileOwnershipConflict
}

func (r ProfileOwnershipReport) Warnings() []string {
	if len(r.Preserved) == 0 {
		return nil
	}
	warnings := make([]string, len(r.Preserved))
	for i, conflict := range r.Preserved {
		warnings[i] = fmt.Sprintf("preserved named profile agent %q: ownership %s", conflict.Key, conflict.Classification)
	}
	return warnings
}
func profileRoleIdentity(role, profileName string) opencode.ManagedAgentIdentity {
	return opencode.ManagedAgentIdentity{Owner: opencode.ManagedOwner, Component: opencode.ManagedComponent, Role: role + "@" + profileName}
}
func attachProfileRoleOwnership(agentMap map[string]any, profileName string) error {
	for _, role := range opencode.DirectRoles() {
		key := role + "-" + profileName
		entry, ok := agentMap[key].(map[string]any)
		if !ok {
			return fmt.Errorf("profile role %q is not an object", key)
		}
		managed, err := opencode.WithManagedMetadata(entry, profileRoleIdentity(role, profileName))
		if err != nil {
			return fmt.Errorf("attach ownership to profile role %q: %w", key, err)
		}
		agentMap[key] = managed
	}
	return nil
}
func reconcileProfileOverlayOwnership(baseJSON, overlayJSON []byte, profileName string) ([]byte, ProfileOwnershipReport, error) {
	if normalized, err := migrateLegacyOpenCodeAgentsKey(baseJSON); err == nil {
		baseJSON = normalized
	}
	base, err := filemerge.UnmarshalJSONObject(baseJSON)
	if err != nil {
		base = map[string]any{}
	}
	overlay, err := filemerge.UnmarshalJSONObject(overlayJSON)
	if err != nil {
		return nil, ProfileOwnershipReport{}, fmt.Errorf("parse profile overlay: %w", err)
	}
	overlayAgents, ok := overlay["agent"].(map[string]any)
	if !ok {
		return overlayJSON, ProfileOwnershipReport{}, nil
	}
	baseAgents, _ := base["agent"].(map[string]any)
	var report ProfileOwnershipReport

	for _, role := range opencode.DirectRoles() {
		key := role + "-" + profileName
		candidate, generated := overlayAgents[key]
		if !generated {
			continue
		}
		existing, exists := baseAgents[key]
		if exists {
			classification := profileRoleClassification(existing, role, profileName)
			if classification != opencode.OwnershipManaged {
				delete(overlayAgents, key)
				report.Preserved = append(report.Preserved, ProfileOwnershipConflict{Key: key, Classification: classification})
				continue
			}
		}

		overlayAgents[key] = map[string]any{"__replace__": candidate}
	}

	encoded, err := json.MarshalIndent(overlay, "", "  ")
	if err != nil {
		return nil, ProfileOwnershipReport{}, fmt.Errorf("marshal reconciled profile overlay: %w", err)
	}
	return append(encoded, '\n'), report, nil
}
func mergeProfileJSONFile(path string, overlay []byte, profileName string) (mergeJSONResult, ProfileOwnershipReport, error) {
	base, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return mergeJSONResult{}, ProfileOwnershipReport{}, fmt.Errorf("read json file %q: %w", path, err)
	}
	reconciled, report, err := reconcileProfileOverlayOwnership(base, overlay, profileName)
	if err != nil {
		return mergeJSONResult{}, ProfileOwnershipReport{}, err
	}
	merged, err := mergeJSONFile(path, reconciled)
	return merged, report, err
}
func removeProfileRoleOwnership(agentMap map[string]any, profileName string) (int, ProfileOwnershipReport) {
	var report ProfileOwnershipReport
	deleted := 0
	for _, role := range opencode.DirectRoles() {
		key := role + "-" + profileName
		raw, exists := agentMap[key]
		if !exists {
			continue
		}
		classification := profileRoleClassification(raw, role, profileName)
		if classification == opencode.OwnershipManaged {
			delete(agentMap, key)
			deleted++
			continue
		}
		report.Preserved = append(report.Preserved, ProfileOwnershipConflict{Key: key, Classification: classification})
	}
	return deleted, report
}
func profileRoleClassification(raw any, role, profileName string) opencode.OwnershipClassification {
	entry, ok := raw.(map[string]any)
	if !ok {
		return opencode.OwnershipMalformedMetadata
	}
	return opencode.ClassifyOwnership(entry, profileRoleIdentity(role, profileName))
}
