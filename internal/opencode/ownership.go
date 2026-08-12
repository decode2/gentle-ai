package opencode

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
)

const (
	ManagedMetadataKey     = "x-gentle-ai"
	ManagedMetadataSchema  = "gentle-ai.opencode-agent"
	ManagedMetadataVersion = 1
	ManagedOwner           = "gentle-ai"
	ManagedComponent       = "opencode"
)

// ManagedAgentIdentity names the package-owned contract expected for one
// agent entry. The agent map key is deliberately not part of the identity.
type ManagedAgentIdentity struct {
	Owner     string
	Component string
	Role      string
}

// ManagedAgentMetadata is stored under ManagedMetadataKey in an agent entry.
// Fingerprint covers the agent definition without this metadata object.
type ManagedAgentMetadata struct {
	Schema      string `json:"schema"`
	Version     int    `json:"version"`
	Owner       string `json:"owner"`
	Component   string `json:"component"`
	Role        string `json:"role"`
	Fingerprint string `json:"fingerprint"`
}

type OwnershipClassification string

const (
	OwnershipManaged           OwnershipClassification = "managed"
	OwnershipMissingMetadata   OwnershipClassification = "missing_metadata"
	OwnershipWrongOwner        OwnershipClassification = "wrong_owner"
	OwnershipWrongComponent    OwnershipClassification = "wrong_component"
	OwnershipWrongRole         OwnershipClassification = "wrong_role"
	OwnershipMalformedMetadata OwnershipClassification = "malformed_metadata"
	OwnershipFingerprintDrift  OwnershipClassification = "fingerprint_drift"
)

var managedFingerprintRE = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// CanonicalJSON returns deterministic compact JSON. encoding/json sorts string
// map keys, including keys in nested JSON objects.
func CanonicalJSON(value any) ([]byte, error) {
	return json.Marshal(value)
}

// CanonicalAgentJSON returns the canonical bytes used to fingerprint an agent.
// The ownership metadata field is excluded without changing the caller's map.
func CanonicalAgentJSON(agent map[string]any) ([]byte, error) {
	if agent == nil {
		return nil, fmt.Errorf("agent definition is nil")
	}
	definition := make(map[string]any, len(agent))
	for key, value := range agent {
		if key != ManagedMetadataKey {
			definition[key] = value
		}
	}
	return CanonicalJSON(definition)
}

// Fingerprint returns the SHA-256 fingerprint of an agent definition. The
// reserved ownership metadata is excluded so writers can refresh it safely.
func Fingerprint(agent map[string]any) (string, error) {
	canonical, err := CanonicalAgentJSON(agent)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ManagedMetadataFor creates metadata for the exact agent definition supplied.
func ManagedMetadataFor(agent map[string]any, identity ManagedAgentIdentity) (ManagedAgentMetadata, error) {
	if identity.Owner == "" || identity.Component == "" || identity.Role == "" {
		return ManagedAgentMetadata{}, fmt.Errorf("managed agent identity must include owner, component, and role")
	}
	fingerprint, err := Fingerprint(agent)
	if err != nil {
		return ManagedAgentMetadata{}, err
	}
	return ManagedAgentMetadata{
		Schema:      ManagedMetadataSchema,
		Version:     ManagedMetadataVersion,
		Owner:       identity.Owner,
		Component:   identity.Component,
		Role:        identity.Role,
		Fingerprint: fingerprint,
	}, nil
}

// WithManagedMetadata returns a shallow copy of agent with fresh ownership
// metadata attached. Neither the input map nor its nested values are changed.
func WithManagedMetadata(agent map[string]any, identity ManagedAgentIdentity) (map[string]any, error) {
	metadata, err := ManagedMetadataFor(agent, identity)
	if err != nil {
		return nil, err
	}
	managed := make(map[string]any, len(agent)+1)
	for key, value := range agent {
		managed[key] = value
	}
	managed[ManagedMetadataKey] = metadata
	return managed, nil
}

// ClassifyOwnership verifies metadata and the definition fingerprint. The
// caller supplies the expected identity; an agent name alone never proves
// package ownership.
func ClassifyOwnership(agent map[string]any, expected ManagedAgentIdentity) OwnershipClassification {
	if agent == nil {
		return OwnershipMissingMetadata
	}
	raw, ok := agent[ManagedMetadataKey]
	if !ok {
		return OwnershipMissingMetadata
	}
	metadata, err := decodeManagedMetadata(raw)
	if err != nil {
		return OwnershipMalformedMetadata
	}
	if metadata.Owner != expected.Owner {
		return OwnershipWrongOwner
	}
	if metadata.Component != expected.Component {
		return OwnershipWrongComponent
	}
	if metadata.Role != expected.Role {
		return OwnershipWrongRole
	}
	fingerprint, err := Fingerprint(agent)
	if err != nil || metadata.Fingerprint != fingerprint {
		return OwnershipFingerprintDrift
	}
	return OwnershipManaged
}

func decodeManagedMetadata(value any) (ManagedAgentMetadata, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return ManagedAgentMetadata{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var metadata ManagedAgentMetadata
	if err := decoder.Decode(&metadata); err != nil {
		return ManagedAgentMetadata{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return ManagedAgentMetadata{}, fmt.Errorf("metadata has trailing data")
	}
	if metadata.Schema != ManagedMetadataSchema ||
		metadata.Version != ManagedMetadataVersion ||
		metadata.Owner == "" || metadata.Component == "" || metadata.Role == "" ||
		!managedFingerprintRE.MatchString(metadata.Fingerprint) {
		return ManagedAgentMetadata{}, fmt.Errorf("metadata does not match the managed schema")
	}
	return metadata, nil
}
