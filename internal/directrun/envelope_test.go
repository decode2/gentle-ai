package directrun

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestRequestWire(t *testing.T) {
	valid := map[string]string{
		"read":    `{"schema":"gentle-ai.direct-operation/v1","identity":"i","operation":"direct_read","request_id":"r","session_id":"s","handoff_revision":"h","binding_revision":"b","parent_session_id":"p","parent_call_id":"c","agent":"gentle-worker","payload":{"path":"a/b","offset":0,"limit":1}}`,
		"edit":    `{"schema":"gentle-ai.direct-operation/v1","identity":"i","operation":"direct_edit","request_id":"r","session_id":"s","handoff_revision":"h","binding_revision":"b","parent_session_id":"p","parent_call_id":"c","agent":"gentle-worker","payload":{"path":"a","base_sha256":"0000000000000000000000000000000000000000000000000000000000000000","replacements":[]}}`,
		"exec":    `{"schema":"gentle-ai.direct-operation/v1","identity":"i","operation":"direct_exec","request_id":"r","session_id":"s","handoff_revision":"h","binding_revision":"b","parent_session_id":"p","parent_call_id":"c","agent":"gentle-worker","payload":{"command_index":0,"timeout_ms":1}}`,
		"inspect": `{"schema":"gentle-ai.direct-operation/v1","identity":"i","operation":"direct_inspect","request_id":"r","session_id":"s","handoff_revision":"h","binding_revision":"b","parent_session_id":"p","parent_call_id":"c","agent":"gentle-worker","payload":{"query":"tree","path":"a"}}`,
	}
	for n, in := range valid {
		t.Run(n, func(t *testing.T) {
			r, err := DecodeRequest([]byte(in))
			if err != nil {
				t.Fatal(err)
			}
			out, err := r.CanonicalJSON()
			if err != nil || string(out) != in {
				t.Fatalf("roundtrip %s %v", out, err)
			}
		})
	}
	bad := []string{
		`{"schema":"gentle-ai.direct-operation/v1","schema":"gentle-ai.direct-operation/v1"}`, `{"schema":"gentle-ai.direct-operation/v1"} {}`, ` {"schema":"gentle-ai.direct-operation/v1"}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":null,"session_id":"s","handoff_revision":"h","payload":{}}`,
		` {"schema":"gentle-ai.direct-operation/v1"}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"path":"a","offset":0,"limit":1,"cwd":"x"}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"path":"a","path":"b","offset":0,"limit":1}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_exec","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"command_index":1.2}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_exec","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"command_index":-1}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_exec","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"command_index":0,"timeout_ms":120001}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_inspect","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"query":"status","path":"a"}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_inspect","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"query":"diff"}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"path":"../a","offset":0,"limit":1}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_edit","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"path":"a","base_sha256":"0000000000000000000000000000000000000000000000000000000000000000","replacements":[{"start":2,"end":3,"text":"x"},{"start":1,"end":2,"text":"x"}]}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_edit","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"path":"a","base_sha256":"0000000000000000000000000000000000000000000000000000000000000000","replacements":[{"start":0,"end":1,"text":"x","patch":"x"}]}}`,
	}
	for _, in := range bad {
		if _, err := DecodeRequest([]byte(in)); err == nil {
			t.Errorf("accepted %s", in)
		}
	}
}
func TestLogicalPathDevices(t *testing.T) {
	for _, tt := range []struct {
		path string
		bad  bool
	}{
		{"CON", true}, {"nUl", true}, {"con.txt", true}, {"LPT9. ", true}, {"CON .txt", true}, {"a/COM1/x", true}, {"console", false}, {"com10", false}, {"a/clockwork", false},
	} {
		err := path([]byte(`"` + tt.path + `"`))
		if (err != nil) != tt.bad {
			t.Errorf("%q: %v", tt.path, err)
		}
	}
}
func TestResponseValidation(t *testing.T) {
	valid := map[string]string{
		"direct_read":    `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"ok","result":{"data_sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","content_b64":"","offset":0,"total_size":0,"truncated":false}}`,
		"direct_edit":    `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_edit","request_id":"r","status":"ok","result":{"result_sha256":"0000000000000000000000000000000000000000000000000000000000000000","changed":false,"publication":"unchanged"}}`,
		"direct_exec":    `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_exec","request_id":"r","status":"ok","result":{"exit_code":0,"output_sha256":"0000000000000000000000000000000000000000000000000000000000000000"}}`,
		"direct_inspect": `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_inspect","request_id":"r","status":"ok","result":{"evidence_sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","content_b64":"","encoding":"utf-8","truncated":false}}`,
	}
	for op, in := range valid {
		if err := validateResponse([]byte(in)); err != nil {
			t.Errorf("%s: %v", op, err)
		}
	}
	for _, in := range []string{
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"ok"}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"error","result":{"data_sha256":"0000000000000000000000000000000000000000000000000000000000000000"},"error":{"code":"timeout","message":"x"}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"error","error":{"code":"oops","message":"x"}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"error","error":{"code":"timeout","message":"/native/path"}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"ok","result":{"data_sha256":"0000000000000000000000000000000000000000000000000000000000000000","content_b64":"%%%","offset":0,"total_size":0,"truncated":false}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"ok","result":{"data_sha256":"0000000000000000000000000000000000000000000000000000000000000000","content_b64":"","offset":1,"total_size":0,"truncated":false}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_edit","request_id":"r","status":"ok","result":{"result_sha256":"0000000000000000000000000000000000000000000000000000000000000000","changed":true,"publication":"unknown"}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_edit","request_id":"r","status":"ok","result":{"result_sha256":"0000000000000000000000000000000000000000000000000000000000000000","changed":false,"publication":"published"}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_inspect","request_id":"r","status":"ok","result":{"evidence_sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","content_b64":"","encoding":"utf-8","truncated":true}}`,
	} {
		if err := validateResponse([]byte(in)); err == nil {
			t.Errorf("accepted %s", in)
		}
	}
}

func TestOperationResultBuilders(t *testing.T) {
	read, err := NewReadResult([]byte{0, 1, 2}, []byte{1, 2}, 1, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	if read.DataSHA256 != DigestSHA256([]byte{0, 1, 2}) || read.ContentB64 != base64.StdEncoding.EncodeToString([]byte{1, 2}) {
		t.Fatalf("read result = %#v", read)
	}
	if _, err := read.CanonicalJSON(); err != nil {
		t.Fatal(err)
	}
	inspect, err := NewInspectResult([]byte("a\nb\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspect.CanonicalJSON(); err != nil {
		t.Fatal(err)
	}
	edit, err := NewEditResult([]byte("x"), false, "unchanged")
	if err != nil {
		t.Fatal(err)
	}
	if got := edit.Publication; got != "unchanged" {
		t.Fatalf("publication = %q", got)
	}
}

func TestOperationResultBuildersRejectMalformedInputs(t *testing.T) {
	for _, tt := range []struct {
		name string
		call func() error
	}{
		{"negative offset", func() error { _, err := NewReadResult([]byte("a"), nil, -1, 1, true); return err }},
		{"total mismatch", func() error { _, err := NewReadResult([]byte("a"), []byte("a"), 0, 2, true); return err }},
		{"offset past total", func() error { _, err := NewReadResult([]byte("a"), nil, 2, 1, true); return err }},
		{"content mismatch", func() error { _, err := NewReadResult([]byte("ab"), []byte("b"), 0, 2, true); return err }},
		{"truncation mismatch", func() error { _, err := NewReadResult([]byte("a"), []byte("a"), 0, 1, true); return err }},
		{"invalid inspect utf8", func() error { _, err := NewInspectResult([]byte{0xff}); return err }},
		{"oversize inspect", func() error { _, err := NewInspectResult(make([]byte, maxContent+1)); return err }},
		{"invalid edit combination", func() error { _, err := NewEditResult([]byte("x"), false, "published"); return err }},
		{"unknown publication", func() error { _, err := NewEditResult([]byte("x"), true, "unknown"); return err }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.call() == nil {
				t.Fatal("accepted malformed result")
			}
		})
	}
}

func TestOperationResultValidationRejectsMaliciousRows(t *testing.T) {
	response := func(operation, result string) []byte {
		return []byte(fmt.Sprintf(`{"schema":"%s","operation":"%s","request_id":"r","status":"ok","result":%s}`, OperationSchema, operation, result))
	}
	read := func(digest, content string, offset, total int64, truncated bool) []byte {
		return response("direct_read", fmt.Sprintf(`{"data_sha256":"%s","content_b64":"%s","offset":%d,"total_size":%d,"truncated":%t}`, digest, content, offset, total, truncated))
	}
	empty := DigestSHA256(nil)
	full := []byte("abc")
	fullDigest := DigestSHA256(full)
	for _, in := range [][]byte{
		read(empty, "", 0, 0, false),
		read(fullDigest, "YWJj", 0, 3, false),
		read(fullDigest, "YQ==", 0, 3, true),
		read(fullDigest, "YmM=", 1, 3, true),
		read(fullDigest, "", 3, 3, true),
	} {
		if err := validateResponse(in); err != nil {
			t.Fatalf("rejected canonical result %s: %v", in, err)
		}
	}
	bad := [][]byte{
		read(empty, "", 0, 0, false), // replaced below to retain the valid empty control.
		read(empty, "YWJj", 0, 3, false),
		read(fullDigest, "%", 0, 3, false),
		read(fullDigest, "", -1, 3, true),
		response("direct_read", fmt.Sprintf(`{"data_sha256":"%s","content_b64":"","offset":9223372036854775808,"total_size":3,"truncated":true}`, fullDigest)),
		read(fullDigest, "", 4, 3, true),
		read(fullDigest, "YWJj", 1, 3, true),
		read(fullDigest, "YWJj", 0, 3, true),
		read(fullDigest, strings.Repeat("A", ((maxContent+3)/3)*4), 0, maxContent, true),
		response("direct_inspect", fmt.Sprintf(`{"evidence_sha256":"%s","content_b64":"/w==","encoding":"utf-8","truncated":false}`, DigestSHA256([]byte{0xff}))),
		response("direct_inspect", `{"evidence_sha256":"0000000000000000000000000000000000000000000000000000000000000000","content_b64":"YQ==","encoding":"utf-8","truncated":false}`),
		response("direct_inspect", fmt.Sprintf(`{"evidence_sha256":"%s","content_b64":"YQ==","encoding":"utf-8","truncated":true}`, DigestSHA256([]byte("a")))),
		response("direct_inspect", fmt.Sprintf(`{"evidence_sha256":"%s","content_b64":"YQ==","encoding":"binary","truncated":false}`, DigestSHA256([]byte("a")))),
		response("direct_edit", `{"result_sha256":"0000000000000000000000000000000000000000000000000000000000000000","changed":false,"publication":"published"}`),
		response("direct_edit", `{"result_sha256":"0000000000000000000000000000000000000000000000000000000000000000","changed":true,"publication":"unchanged"}`),
		response("direct_edit", `{"result_sha256":"0000000000000000000000000000000000000000000000000000000000000000","changed":true,"publication":"unknown"}`),
	}
	bad = bad[1:]
	for _, in := range bad {
		if err := validateResponse(in); err == nil {
			t.Errorf("accepted malicious result %s", in)
		}
	}
}

func validateResponse(payload []byte) error {
	var response Response
	if err := json.Unmarshal(payload, &response); err != nil {
		return err
	}
	return response.Validate()
}

func TestEnvelopeRejectsStructuralMalice(t *testing.T) {
	valid := `{"schema":"gentle-ai.direct-operation/v1","identity":"i","operation":"direct_read","request_id":"r","session_id":"s","handoff_revision":"h","binding_revision":"b","parent_session_id":"p","parent_call_id":"c","agent":"gentle-worker","payload":{"path":"a","offset":0,"limit":1}}`
	for _, in := range [][]byte{
		[]byte(strings.Replace(valid, `"request_id":"r"`, `"request_id":null`, 1)),
		[]byte(strings.Replace(valid, `"payload":{`, `"unknown":1,"payload":{`, 1)),
		[]byte(strings.Replace(valid, `"path":"a"`, `"path":"a","path":"b"`, 1)),
		append([]byte(valid), []byte(` {}`)...),
		[]byte(strings.Replace(valid, `"schema"`, ` "schema"`, 1)),
		[]byte(strings.Repeat(" ", maxEnvelope+1)),
	} {
		if _, err := DecodeRequest(in); err == nil {
			t.Errorf("accepted malformed envelope %q", in[:min(len(in), 128)])
		}
	}
}
