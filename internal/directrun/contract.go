// Package directrun validates contract bytes only. Filesystem containment,
// symlinks, repository binding, CAS/single-use replay, changed-path containment,
// fresh reviewer context, one-pass enforcement, and runtime mutation enforcement
// are deferred to later layers.
package directrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	HandoffSchema      = "gentle-ai.direct-run/v1"
	WorkerOutputSchema = "gentle-ai.direct-run-output/v1"
	WorkerRole         = "gentle-worker"
	revisionDomain     = "gentle-ai.direct-run-revision/v1"
)

type Digest string
type WorkerIdentity struct {
	Role string `json:"role"`
	ID   string `json:"id"`
}
type Command struct {
	Argv []string `json:"argv"`
	CWD  string   `json:"cwd"`
}
type Handoff struct {
	Schema             string         `json:"schema"`
	Identity           string         `json:"identity"`
	Revision           Digest         `json:"revision,omitempty"`
	Worker             WorkerIdentity `json:"worker"`
	AllowedEditRoots   []string       `json:"allowed_edit_roots"`
	TargetBehavior     string         `json:"target_behavior"`
	AcceptanceCriteria []string       `json:"acceptance_criteria"`
	Verification       []Command      `json:"verification_commands"`
}
type OutputBinding struct {
	HandoffIdentity string         `json:"handoff_identity"`
	HandoffRevision Digest         `json:"handoff_revision"`
	Worker          WorkerIdentity `json:"worker"`
}
type VerificationResult struct {
	CommandIndex int    `json:"command_index"`
	ExitCode     int    `json:"exit_code"`
	OutputDigest Digest `json:"output_digest"`
}
type WorkerOutput struct {
	Schema       string               `json:"schema"`
	Binding      OutputBinding        `json:"binding"`
	ChangedPaths []string             `json:"changed_paths"`
	Verification []VerificationResult `json:"verification"`
	Summary      string               `json:"summary"`
}

var idPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var safePathPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)*$`)
var unsafeArgPattern = regexp.MustCompile(`(?:^|\x01)[A-Za-z_][A-Za-z0-9_]*=|[\x00&|;< >\r\n\x60]|\$\(`)
var goFlags = map[string]bool{"-race": true, "-short": true, "-v": true, "-json": true, "-count=1": true}

// Unknown executables, subcommands, wrappers, aliases, and path lookalikes deny.
var exactCommands = map[string]bool{"git\x00status": true, "git\x00status\x00--short": true, "git\x00status\x00--short\x00--branch": true, "git\x00diff\x00--check": true, "git\x00diff\x00--stat": true, "git\x00diff\x00--name-only": true, "git\x00rev-parse\x00--show-toplevel": true, "git\x00rev-parse\x00--is-inside-work-tree": true, "git\x00ls-files": true, "git\x00show\x00--stat\x00--oneline\x00HEAD": true, "npm\x00test": true, "npm\x00run\x00test": true, "pytest\x00-q": true, "pytest\x00-x": true, "pytest\x00--maxfail=1": true}

func (h Handoff) Validate() error {
	if err := h.validate(); err != nil {
		return err
	}
	want, err := DeriveRevision(h)
	return errors.Join(err, ok(h.Revision == want, fmt.Sprintf("revision does not match handoff content: got %q want %q", h.Revision, want)))
}
func (h Handoff) validate() error {
	return errors.Join(ok(h.Schema == HandoffSchema, "invalid handoff schema"), worker("worker", h.Worker), id("identity", h.Identity), ok(len(h.AllowedEditRoots) > 0, "allowed_edit_roots must not be empty"), paths(h.AllowedEditRoots, true), text("target_behavior", h.TargetBehavior), unique("acceptance_criteria", h.AcceptanceCriteria, stringKey, textAt), unique("verification_commands", h.Verification, commandKey, commandAt))
}
func unique[T any](label string, values []T, key func(T) string, valid func(int, T) error) error {
	if len(values) == 0 {
		return errors.New(label + " must not be empty")
	}
	seen := map[string]bool{}
	for i, value := range values {
		if err := valid(i, value); err != nil {
			return err
		}
		if seen[key(value)] {
			return errors.New(label + " must be unique")
		}
		seen[key(value)] = true
	}
	return nil
}
func stringKey(value string) string { return value }
func textAt(index int, value string) error {
	return text(fmt.Sprintf("acceptance_criteria[%d]", index), value)
}
func commandKey(value Command) string          { return strings.Join(value.Argv, "\x00") + "\x00" + value.CWD }
func commandAt(index int, value Command) error { return value.validate(index) }
func NewHandoff(identity, workerID string, roots []string, target string, criteria []string, commands []Command) (Handoff, error) {
	return (Handoff{HandoffSchema, identity, "", WorkerIdentity{WorkerRole, workerID}, roots, target, criteria, commands}).Seal()
}
func (h Handoff) Seal() (Handoff, error) {
	if h.Revision != "" {
		return Handoff{}, errors.New("handoff is already sealed")
	}
	revision, err := DeriveRevision(h)
	if err != nil {
		return Handoff{}, err
	}
	h.Revision = revision
	return h, nil
}
func DeriveRevision(h Handoff) (Digest, error) {
	if err := h.validate(); err != nil {
		return "", err
	}
	h.Revision = ""
	payload, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	return digest(revisionDomain, payload), nil
}
func (c Command) validate(index int) error {
	return errors.Join(ok(len(c.Argv) > 0 && safeArgs(c.Argv), fmt.Sprintf("verification_commands[%d].argv is empty or unsafe", index)), ok(canonicalPath(c.CWD, true), fmt.Sprintf("verification_commands[%d].cwd must be canonical", index)), ok(allowedCommand(c.Argv), fmt.Sprintf("verification_commands[%d] is not an allowed command", index)))
}
func safeArgs(args []string) bool { return !unsafeArgPattern.MatchString(strings.Join(args, "\x01")) }
func (o WorkerOutput) Validate() error {
	return errors.Join(ok(o.Schema == WorkerOutputSchema, "invalid worker output schema"), worker("binding.worker", o.Binding.Worker), id("binding.handoff_identity", o.Binding.HandoffIdentity), digestValue("binding.handoff_revision", o.Binding.HandoffRevision), text("summary", o.Summary), ok(o.ChangedPaths != nil, "changed_paths must be present"), paths(o.ChangedPaths, false), results(o.Verification))
}
func results(values []VerificationResult) error {
	if len(values) == 0 {
		return errors.New("verification must not be empty")
	}
	for i, value := range values {
		if err := errors.Join(ok(value.CommandIndex == i && value.ExitCode >= 0, fmt.Sprintf("verification[%d] has an invalid index or exit code", i)), digestValue(fmt.Sprintf("verification[%d].output_digest", i), value.OutputDigest)); err != nil {
			return err
		}
	}
	return nil
}
func worker(label string, value WorkerIdentity) error {
	return errors.Join(ok(value.Role == WorkerRole, fmt.Sprintf("%s.role must be %q", label, WorkerRole)), id(label+".id", value.ID))
}
func (h Handoff) OutputBinding() OutputBinding {
	return OutputBinding{h.Identity, h.Revision, h.Worker}
}
func (o WorkerOutput) ValidateAgainst(h Handoff) error {
	return errors.Join(h.Validate(), o.Validate(), ok(o.Binding == h.OutputBinding(), "worker output is bound to a different handoff"), ok(len(o.Verification) == len(h.Verification), "worker output verification does not cover every command"))
}
func ok(condition bool, message string) error {
	if !condition {
		return errors.New(message)
	}
	return nil
}
func text(label, value string) error {
	return ok(value != "" && strings.TrimSpace(value) == value, label+" must not be empty or padded")
}
func id(label, value string) error {
	return ok(len(value) > 0 && len(value) <= 128 && idPattern.MatchString(value), label+" is not a canonical identifier")
}
func digestValue(label string, value Digest) error {
	return ok(digestPattern.MatchString(string(value)), label+" is not a canonical SHA-256 digest")
}
func paths(values []string, absolute bool) error {
	for i, value := range values {
		if err := errors.Join(ok(canonicalPath(value, absolute), fmt.Sprintf("path[%d] must be canonical", i)), ok(i == 0 || values[i-1] < value, "paths must be sorted and unique")); err != nil {
			return err
		}
	}
	return nil
}
func canonicalPath(value string, absolute bool) bool {
	return value != "" && !strings.ContainsRune(value, 0) && strings.TrimSpace(value) == value && filepath.Clean(value) == value && (!absolute || filepath.IsAbs(value))
}
func allowedCommand(argv []string) bool {
	if len(argv) == 0 {
		return false
	}
	if argv[0] == "go" {
		return allowedGo(argv)
	}
	if exactCommands[strings.Join(argv, "\x00")] {
		return true
	}
	return len(argv) == 3 && argv[0] == "pytest" && (argv[1] == "-q" || argv[1] == "-x") && testPath(argv[2])
}
func allowedGo(argv []string) bool {
	if len(argv) == 2 && argv[1] == "version" {
		return true
	}
	if len(argv) < 3 || (argv[1] != "test" && argv[1] != "vet") {
		return false
	}
	hasPackage := false
	for _, arg := range argv[2:] {
		if goFlags[arg] {
			continue
		}
		if !goPackage(arg) {
			return false
		}
		hasPackage = true
	}
	return hasPackage
}
func goPackage(arg string) bool {
	return arg == "./..." || strings.HasPrefix(arg, "./") && !strings.Contains(arg[2:], "..") && safePathPattern.MatchString(arg[2:]) && filepath.Clean(arg[2:]) == arg[2:]
}
func testPath(value string) bool {
	return safePathPattern.MatchString(value) && !strings.Contains(value, "..") && filepath.Clean(value) == value && (strings.HasPrefix(value, "test") || strings.HasPrefix(value, "tests/"))
}
func (h Handoff) CanonicalJSON() ([]byte, error)      { return encode(h, h.Validate) }
func (o WorkerOutput) CanonicalJSON() ([]byte, error) { return encode(o, o.Validate) }
func (h Handoff) Digest() (Digest, error)             { return contractDigest(HandoffSchema, h, h.Validate) }
func (o WorkerOutput) Digest() (Digest, error) {
	return contractDigest(WorkerOutputSchema, o, o.Validate)
}
func encode(value any, validate func() error) ([]byte, error) {
	if err := validate(); err != nil {
		return nil, err
	}
	return json.Marshal(value)
}
func contractDigest(domain string, value any, validate func() error) (Digest, error) {
	payload, err := encode(value, validate)
	if err != nil {
		return "", err
	}
	return digest(domain, payload), nil
}
func DecodeHandoff(payload []byte) (Handoff, error) { return decode[Handoff](payload, false) }
func DecodeWorkerOutput(payload []byte) (WorkerOutput, error) {
	return decode[WorkerOutput](payload, true)
}
func decode[T interface{ Validate() error }](payload []byte, results bool) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode contract: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return value, errors.New("decode contract: multiple JSON values")
		}
		return value, fmt.Errorf("decode contract: trailing JSON: %w", err)
	}
	if results {
		if err := requiredResults(payload); err != nil {
			return value, err
		}
	}
	if err := value.Validate(); err != nil {
		return value, err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return value, err
	}
	return value, ok(bytes.Equal(encoded, payload), "contract JSON is not canonical")
}
func requiredResults(payload []byte) error {
	var value struct {
		Verification []map[string]json.RawMessage `json:"verification"`
	}
	if err := json.Unmarshal(payload, &value); err != nil {
		return err
	}
	for i, result := range value.Verification {
		for _, field := range []string{"command_index", "exit_code", "output_digest"} {
			raw, present := result[field]
			if !present || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return fmt.Errorf("verification[%d] requires non-null %s", i, field)
			}
		}
	}
	return nil
}
func digest(domain string, payload []byte) Digest {
	sum := sha256.New()
	_, _ = sum.Write([]byte(domain))
	_, _ = sum.Write([]byte{0})
	_, _ = sum.Write(payload)
	return Digest(fmt.Sprintf("sha256:%x", sum.Sum(nil)))
}
