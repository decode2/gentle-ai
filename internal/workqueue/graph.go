package workqueue

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gentleman-programming/gentle-ai/v2/internal/pathidentity"
)

const GraphSchemaVersion, CanonicalizationVersion, PathPolicyVersion, GitCommonDirBindingSchema, SourceArtifactBindingSchema, ConflictKeySchema = "gentle-ai.workqueue-graph/v2", "json-struct/v2", "repository-relative-existing-ancestor-identity/v1", "gentle-ai.workqueue-git-common-dir/v1", "gentle-ai.workqueue-source-artifact/v1", "gentle-ai.workqueue-conflict-key/v1"
const PathConflictDomain, SemanticConflictDomain = "path", "semantic"

var ErrInvalidGraph = errors.New("invalid workqueue graph")

type GitCommonDirBinding struct{ Schema, CommonDir, CommonDirIdentity string }
type ArtifactLocator struct{ Topic, Path string }
type SourceArtifactBinding struct {
	Schema, Revision string
	Locator          ArtifactLocator
}
type ChangeIdentity struct{ ID, Revision string }
type ConflictKey struct{ Schema, Domain, Producer, Name, Version string }
type EvidenceRequirements struct{ Required, Validation []string }
type DeliveryBoundary struct{ RollbackBoundary, IntegrationBoundary string }
type QueueItem struct {
	ID, Payload       string
	DependsOn, Scopes []string
	Conflicts         []ConflictKey
	Evidence          EvidenceRequirements
	Delivery          DeliveryBoundary
}
type GraphInput struct {
	Repository GitCommonDirBinding
	Change     ChangeIdentity
	Source     SourceArtifactBinding
	Items      []QueueItem
}
type GraphSnapshot struct {
	input    GraphInput
	revision string
}

func NewSnapshot(repositoryRoot string, input GraphInput) (GraphSnapshot, error) {
	root, err := canonicalDirectory(repositoryRoot)
	if err != nil {
		return GraphSnapshot{}, fmt.Errorf("%w: repository root", ErrInvalidGraph)
	}
	if err = validateHeader(root, &input); err != nil {
		return GraphSnapshot{}, err
	}
	items, seen := make([]QueueItem, len(input.Items)), map[string]bool{}
	for i, raw := range input.Items {
		item, itemErr := normalizeItem(root, raw)
		if itemErr != nil {
			return GraphSnapshot{}, fmt.Errorf("item %d: %w", i, itemErr)
		}
		if seen[item.ID] {
			return GraphSnapshot{}, fmt.Errorf("%w: duplicate item ID", ErrInvalidGraph)
		}
		seen[item.ID], items[i] = true, item
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	if err = validateDependencies(items); err != nil {
		return GraphSnapshot{}, err
	}
	input.Items, input.Repository.CommonDir = items, ""
	payload, _ := json.Marshal(struct {
		Schema, Canonicalization, PathPolicy string
		Input                                GraphInput
	}{GraphSchemaVersion, CanonicalizationVersion, PathPolicyVersion, input})
	sum := sha256.Sum256(payload)
	return GraphSnapshot{input: input, revision: fmt.Sprintf("sha256:%x", sum)}, nil
}
func (s GraphSnapshot) GraphRevision() string { return s.revision }
func validateHeader(root string, input *GraphInput) error {
	if len(input.Items) == 0 {
		return fmt.Errorf("%w: no queue items", ErrInvalidGraph)
	}
	common, err := canonicalDirectory(input.Repository.CommonDir)
	if input.Repository.Schema != GitCommonDirBindingSchema || err != nil || common != input.Repository.CommonDir || !pathidentity.SameDirectory(root, common) || !validDigest(input.Repository.CommonDirIdentity) {
		return fmt.Errorf("%w: Git-common-dir binding is not canonical", ErrInvalidGraph)
	}
	if !identifier(input.Change.ID, "-_") || clean(&input.Change.Revision) != nil {
		return fmt.Errorf("%w: change identity is not canonical", ErrInvalidGraph)
	}
	source, pathErr := input.Source, error(nil)
	source.Locator.Path, pathErr = canonicalRelative(source.Locator.Path)
	if source.Schema != SourceArtifactBindingSchema || !validDigest(source.Revision) || !identifier(source.Locator.Topic, "-_") || pathErr != nil || !(strings.HasPrefix(source.Locator.Path, "openspec/changes/"+source.Locator.Topic+"/") || strings.HasPrefix(source.Locator.Path, "sdd/"+source.Locator.Topic+"/")) || source.Locator.Topic != input.Change.ID {
		return fmt.Errorf("%w: source artifact binding is not canonical", ErrInvalidGraph)
	}
	return nil
}
func normalizeItem(root string, raw QueueItem) (QueueItem, error) {
	item := raw
	if !identifier(item.ID, "._-") {
		return item, fmt.Errorf("%w: item ID is not canonical", ErrInvalidGraph)
	}
	for _, value := range []*string{&item.Payload, &item.Delivery.RollbackBoundary, &item.Delivery.IntegrationBoundary} {
		if err := clean(value); err != nil {
			return item, err
		}
	}
	var err error
	if item.DependsOn, err = list("", item.DependsOn, false); err != nil {
		return item, err
	}
	if item.Scopes, err = list(root, item.Scopes, true); err != nil {
		return item, err
	}
	if item.Conflicts, err = conflictList(root, item.Conflicts); err != nil {
		return item, err
	}
	if item.Evidence.Required, err = list("", item.Evidence.Required, true); err != nil {
		return item, err
	}
	item.Evidence.Validation, err = list("", item.Evidence.Validation, true)
	return item, err
}
func clean(value *string) error {
	*value = strings.TrimSpace(*value)
	if *value == "" || !utf8.ValidString(*value) || strings.IndexFunc(*value, unicode.IsControl) >= 0 {
		return ErrInvalidGraph
	}
	return nil
}
func list(root string, values []string, required bool) ([]string, error) {
	if required && len(values) == 0 {
		return nil, ErrInvalidGraph
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = value
		var err error
		if root == "" {
			err = clean(&out[i])
		} else {
			out[i], err = normalizePath(root, out[i])
		}
		if err != nil {
			return nil, err
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
func conflictList(root string, values []ConflictKey) ([]ConflictKey, error) {
	out, seen := make([]ConflictKey, len(values)), map[ConflictKey]bool{}
	for i, key := range values {
		if key.Schema != ConflictKeySchema || (key.Domain != PathConflictDomain && key.Domain != SemanticConflictDomain) || !identifier(key.Producer, "._-") || !identifier(key.Version, "._-") {
			return nil, fmt.Errorf("%w: conflict key %d", ErrInvalidGraph, i)
		}
		var err error
		if key.Domain == PathConflictDomain {
			key.Name, err = normalizePath(root, key.Name)
		} else {
			err = clean(&key.Name)
			key.Name = strings.Join(strings.Fields(key.Name), " ")
		}
		if err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, fmt.Errorf("%w: duplicate conflict key", ErrInvalidGraph)
		}
		seen[key], out[i] = true, key
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.Join([]string{out[i].Schema, out[i].Domain, out[i].Producer, out[i].Name, out[i].Version}, "\x00") < strings.Join([]string{out[j].Schema, out[j].Domain, out[j].Producer, out[j].Name, out[j].Version}, "\x00")
	})
	return out, nil
}
func validateDependencies(items []QueueItem) error {
	deps, state := map[string][]string{}, map[string]uint8{}
	for _, item := range items {
		deps[item.ID] = item.DependsOn
	}
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("%w: dependency cycle", ErrInvalidGraph)
		case 2:
			return nil
		}
		state[id] = 1
		for _, dependency := range deps[id] {
			if _, ok := deps[dependency]; !ok {
				return fmt.Errorf("%w: missing dependency", ErrInvalidGraph)
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for id := range deps {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}
func canonicalDirectory(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || !filepath.IsAbs(value) || value != filepath.Clean(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("noncanonical directory")
	}
	resolved, err := filepath.EvalSymlinks(value)
	if err != nil || resolved != value {
		return "", errors.New("directory is not pre-resolved")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return "", errors.New("directory does not exist")
	}
	return resolved, nil
}
func normalizePath(root, value string) (string, error) {
	path, err := canonicalRelative(value)
	if err != nil {
		return "", err
	}
	parts, current := strings.Split(path, "/"), root
	for i, part := range parts {
		next := filepath.Join(current, part)
		info, statErr := os.Lstat(next)
		if os.IsNotExist(statErr) {
			return relativePath(root, current, parts[i:])
		}
		if statErr != nil {
			return "", fmt.Errorf("%w: inspect path", ErrInvalidGraph)
		}
		entries, readErr := os.ReadDir(current)
		if readErr != nil {
			return "", fmt.Errorf("%w: inspect path", ErrInvalidGraph)
		}
		for _, entry := range entries {
			actual := filepath.Join(current, entry.Name())
			if other, entryErr := os.Lstat(actual); entryErr == nil && os.SameFile(info, other) {
				next = actual
				break
			}
		}
		current = next
		if i+1 < len(parts) {
			nextInfo, nextErr := os.Stat(current)
			if nextErr != nil || !nextInfo.IsDir() {
				return "", fmt.Errorf("%w: path continues through a non-directory", ErrInvalidGraph)
			}
		}
	}
	return relativePath(root, current, nil)
}
func relativePath(root, current string, tail []string) (string, error) {
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil || !pathidentity.Contains(root, resolved) {
		return "", fmt.Errorf("%w: path escapes through a symlink", ErrInvalidGraph)
	}
	relative, err := filepath.Rel(root, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("%w: path is not contained", ErrInvalidGraph)
	}
	return filepath.ToSlash(filepath.Clean(filepath.Join(relative, filepath.Join(tail...)))), nil
}
func canonicalRelative(value string) (string, error) {
	if value == "" || value != strings.TrimSpace(value) || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 || strings.ContainsRune(value, '\\') || filepath.IsAbs(value) || strings.HasPrefix(value, "/") || (len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':') {
		return "", ErrInvalidGraph
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return "", ErrInvalidGraph
		}
	}
	return value, nil
}
func identifier(value, separators string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > 96 {
		return false
	}
	previous := false
	for i, c := range value {
		alpha := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if !alpha && (!strings.ContainsRune(separators, c) || i == 0 || previous) {
			return false
		}
		previous = !alpha
	}
	return !previous
}
func validDigest(value string) bool {
	return len(value) == 71 && strings.HasPrefix(value, "sha256:") && value == strings.ToLower(value) && strings.Trim(value[7:], "0123456789abcdef") == ""
}
