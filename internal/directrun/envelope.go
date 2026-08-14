package directrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const OperationSchema = "gentle-ai.direct-operation/v1"

const (
	maxEnvelope = 1 << 20
	maxID       = 128
	maxPath     = 1024
	maxText     = 256 << 10
	maxMessage  = 512
	maxContent  = 256 << 10
)

type Request struct {
	Schema          string          `json:"schema"`
	Operation       string          `json:"operation"`
	RequestID       string          `json:"request_id"`
	SessionID       string          `json:"session_id"`
	HandoffRevision string          `json:"handoff_revision"`
	Payload         json.RawMessage `json:"payload"`
}
type Response struct {
	Schema    string          `json:"schema"`
	Operation string          `json:"operation"`
	RequestID string          `json:"request_id"`
	Status    string          `json:"status"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *WireError      `json:"error,omitempty"`
}
type WireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var operations = set("direct_read", "direct_edit", "direct_exec", "direct_inspect")
var codes = set("malformed_request", "unsupported_operation", "unauthenticated", "unauthorized", "repository_unavailable", "invalid_path", "not_found", "conflict", "limit_exceeded", "timeout", "cancelled", "backend_failure")

func (r Request) Validate() error {
	if r.Schema != OperationSchema || !operations[r.Operation] || !opaque(r.RequestID) || !opaque(r.SessionID) || !opaque(r.HandoffRevision) {
		return errors.New("invalid direct operation request")
	}
	return requestPayload(r.Operation, r.Payload)
}
func (r Request) CanonicalJSON() ([]byte, error) { return marshal(r, r.Validate) }
func DecodeRequest(b []byte) (Request, error) {
	var r Request
	if err := decodeEnvelope(b, &r, set("schema", "operation", "request_id", "session_id", "handoff_revision", "payload")); err != nil {
		return r, err
	}
	return r, r.Validate()
}
func (r Response) Validate() error {
	if r.Schema != OperationSchema || !operations[r.Operation] || !opaque(r.RequestID) || (r.Status != "ok" && r.Status != "error") {
		return errors.New("invalid direct operation response")
	}
	if r.Status == "ok" && (r.Result == nil || r.Error != nil) || r.Status == "error" && (r.Result != nil || r.Error == nil) {
		return errors.New("response must contain exactly its status payload")
	}
	if r.Status == "ok" {
		return result(r.Operation, r.Result)
	}
	if r.Error == nil || !codes[r.Error.Code] || len(r.Error.Message) == 0 || len(r.Error.Message) > maxMessage || strings.ContainsAny(r.Error.Message, "\r\n/\\") {
		return errors.New("invalid wire error")
	}
	return nil
}
func (r Response) CanonicalJSON() ([]byte, error) { return marshal(r, r.Validate) }
func DecodeResponse(b []byte) (Response, error) {
	var r Response
	if err := decodeEnvelope(b, &r, set("schema", "operation", "request_id", "status", "result", "error")); err != nil {
		return r, err
	}
	return r, r.Validate()
}

func requestPayload(op string, b json.RawMessage) error {
	m, err := object(b, map[string]map[string]bool{
		"direct_read": {"path": true, "offset": true, "limit": true}, "direct_edit": {"path": true, "base_sha256": true, "replacements": true},
		"direct_exec": {"command_index": true, "timeout_ms": true}, "direct_inspect": {"query": true, "path": true},
	}[op])
	if err != nil {
		return err
	}
	switch op {
	case "direct_read":
		return all(path(m["path"]), integer(m["offset"], 0, -1), integer(m["limit"], 1, maxContent))
	case "direct_edit":
		if err := all(path(m["path"]), sha(m["base_sha256"])); err != nil {
			return err
		}
		var x []json.RawMessage
		if err := json.Unmarshal(m["replacements"], &x); err != nil || len(x) > 64 {
			return errors.New("invalid replacements")
		}
		n, end := 0, int64(-1)
		for _, raw := range x {
			v, err := object(raw, set("start", "end", "text"))
			if err != nil {
				return errors.New("invalid replacements")
			}
			var start, finish int64
			var text string
			if json.Unmarshal(v["start"], &start) != nil || json.Unmarshal(v["end"], &finish) != nil || json.Unmarshal(v["text"], &text) != nil {
				return errors.New("invalid replacements")
			}
			n += len(text)
			if start < 0 || finish < start || start < end || n > maxText {
				return errors.New("invalid replacements")
			}
			end = finish
		}
		return nil
	case "direct_exec":
		if err := integer(m["command_index"], 0, -1); err != nil {
			return err
		}
		if v, ok := m["timeout_ms"]; ok {
			return integer(v, 1, 120000)
		}
		return nil
	default:
		q := ""
		if err := json.Unmarshal(m["query"], &q); err != nil || q != "tree" {
			return errors.New("invalid inspect query")
		}
		if v, ok := m["path"]; ok {
			return path(v)
		}
		return nil
	}
}
func result(op string, b json.RawMessage) error {
	switch op {
	case "direct_read":
		m, err := object(b, set("data_sha256", "content_b64", "offset", "total_size", "truncated"))
		if err != nil || sha(m["data_sha256"]) != nil || integer(m["offset"], 0, -1) != nil || integer(m["total_size"], 0, -1) != nil || boolean(m["truncated"]) != nil {
			return errors.New("invalid read result")
		}
		content, err := b64(m["content_b64"])
		if err != nil || int64(len(content)) > maxContent {
			return errors.New("invalid read result")
		}
		var offset, total int64
		_ = json.Unmarshal(m["offset"], &offset)
		_ = json.Unmarshal(m["total_size"], &total)
		if offset > total || int64(len(content)) > total-offset {
			return errors.New("invalid read result")
		}
		var truncated bool
		_ = json.Unmarshal(m["truncated"], &truncated)
		wantTruncated := offset != 0 || int64(len(content)) != total
		if truncated != wantTruncated || (!truncated && DigestSHA256(content) != string(mustDigest(m["data_sha256"]))) {
			return errors.New("invalid read result")
		}
		return nil
	case "direct_edit":
		m, err := object(b, set("result_sha256", "changed", "publication"))
		if err != nil || sha(m["result_sha256"]) != nil || boolean(m["changed"]) != nil {
			return errors.New("invalid edit result")
		}
		var publication string
		if json.Unmarshal(m["publication"], &publication) != nil || (publication != "published" && publication != "unchanged") {
			return errors.New("invalid edit result")
		}
		var changed bool
		_ = json.Unmarshal(m["changed"], &changed)
		if changed != (publication == "published") {
			return errors.New("invalid edit result")
		}
		return nil
	case "direct_inspect":
		m, err := object(b, set("evidence_sha256", "content_b64", "encoding", "truncated"))
		if err != nil || sha(m["evidence_sha256"]) != nil || boolean(m["truncated"]) != nil {
			return errors.New("invalid inspect result")
		}
		var encoding string
		if json.Unmarshal(m["encoding"], &encoding) != nil || encoding != "utf-8" {
			return errors.New("invalid inspect result")
		}
		content, err := b64(m["content_b64"])
		var truncated bool
		_ = json.Unmarshal(m["truncated"], &truncated)
		if err != nil || len(content) > maxContent || !utf8.Valid(content) || truncated || !bytes.Equal([]byte(DigestSHA256(content)), mustDigest(m["evidence_sha256"])) {
			return errors.New("invalid inspect result")
		}
		return nil
	default:
		m, err := object(b, set("output_sha256"))
		if err != nil {
			return err
		}
		return sha(m["output_sha256"])
	}
}

// DigestSHA256 returns the lower-case SHA-256 representation used by operation envelopes.
func DigestSHA256(value []byte) string { return fmt.Sprintf("%x", sha256.Sum256(value)) }

func mustDigest(raw []byte) []byte {
	var value string
	_ = json.Unmarshal(raw, &value)
	return []byte(value)
}

func b64(raw []byte) ([]byte, error) {
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("invalid base64")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func boolean(raw []byte) error {
	var value bool
	if json.Unmarshal(raw, &value) != nil {
		return errors.New("invalid boolean")
	}
	return nil
}
func object(b []byte, allowed map[string]bool) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if len(b) == 0 || json.Unmarshal(b, &m) != nil || m == nil {
		return nil, errors.New("expected object")
	}
	for k, v := range m {
		if !allowed[k] || bytes.Equal(v, []byte("null")) {
			return nil, errors.New("unknown or null field")
		}
	}
	for k := range allowed {
		if _, ok := m[k]; !ok && !(k == "timeout_ms" || k == "path") {
			return nil, errors.New("missing field")
		}
	}
	return m, nil
}
func path(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) != nil || len(s) == 0 || len(s) > maxPath || strings.ContainsAny(s, "\\\x00:") || strings.HasPrefix(s, "/") {
		return errors.New("invalid logical path")
	}
	for _, p := range strings.Split(s, "/") {
		if p == "" || p == "." || p == ".." || reservedDevice(p) {
			return errors.New("invalid logical path")
		}
	}
	return nil
}
func reservedDevice(segment string) bool {
	base := strings.ToUpper(strings.TrimRight(strings.Split(segment, ".")[0], " ."))
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CLOCK$" {
		return true
	}
	return len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9'
}
func sha(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) != nil || len(s) != 64 {
		return errors.New("invalid sha256")
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return errors.New("invalid sha256")
		}
	}
	return nil
}
func integer(b []byte, min, max int64) error {
	var n int64
	if json.Unmarshal(b, &n) != nil || n < min || max >= 0 && n > max {
		return errors.New("invalid integer")
	}
	return nil
}
func opaque(s string) bool { return len(s) > 0 && len(s) <= maxID && strings.TrimSpace(s) == s }
func set(s ...string) map[string]bool {
	m := map[string]bool{}
	for _, v := range s {
		m[v] = true
	}
	return m
}
func all(es ...error) error {
	for _, e := range es {
		if e != nil {
			return e
		}
	}
	return nil
}
func marshal(v any, valid func() error) ([]byte, error) {
	if err := valid(); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
func decodeEnvelope(b []byte, dst any, fields map[string]bool) error {
	if len(b) == 0 || len(b) > maxEnvelope || duplicate(b) {
		return errors.New("malformed direct operation envelope")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(dst); err != nil {
		return fmt.Errorf("decode envelope: %w", err)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return errors.New("trailing or multiple JSON values")
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return errors.New("expected object")
	}
	for k, v := range m {
		if !fields[k] || bytes.Equal(v, []byte("null")) {
			return errors.New("unknown or null field")
		}
	}
	if out, err := json.Marshal(dst); err != nil || !bytes.Equal(out, b) {
		return errors.New("noncanonical envelope")
	}
	return nil
}
func duplicate(b []byte) bool {
	d := json.NewDecoder(bytes.NewReader(b))
	var walk func() bool
	walk = func() bool {
		t, e := d.Token()
		if e != nil {
			return true
		}
		if x, ok := t.(json.Delim); ok && x == '{' {
			seen := set()
			for d.More() {
				k, e := d.Token()
				if e != nil || seen[k.(string)] {
					return true
				}
				seen[k.(string)] = true
				if walk() {
					return true
				}
			}
			_, e = d.Token()
			return e != nil
		}
		if x, ok := t.(json.Delim); ok && x == '[' {
			for d.More() {
				if walk() {
					return true
				}
			}
			_, e = d.Token()
			return e != nil
		}
		return false
	}
	return walk()
}
