package directrun

import (
	"encoding/base64"
	"testing"
)

func TestRequestWire(t *testing.T) {
	valid := map[string]string{
		"read":    `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"path":"a/b","offset":0,"limit":1}}`,
		"edit":    `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_edit","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"path":"a","base_sha256":"0000000000000000000000000000000000000000000000000000000000000000","replacements":[]}}`,
		"exec":    `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_exec","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"command_index":0,"timeout_ms":1}}`,
		"inspect": `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_inspect","request_id":"r","session_id":"s","handoff_revision":"h","payload":{"query":"tree","path":"a"}}`,
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
func TestResponseWire(t *testing.T) {
	valid := map[string]string{
		"direct_read":    `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"ok","result":{"data_sha256":"0000000000000000000000000000000000000000000000000000000000000000","content_b64":"","offset":0,"total_size":0,"truncated":false}}`,
		"direct_edit":    `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_edit","request_id":"r","status":"ok","result":{"result_sha256":"0000000000000000000000000000000000000000000000000000000000000000","changed":false,"publication":"unchanged"}}`,
		"direct_exec":    `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_exec","request_id":"r","status":"ok","result":{"output_sha256":"0000000000000000000000000000000000000000000000000000000000000000"}}`,
		"direct_inspect": `{"schema":"gentle-ai.direct-operation/v1","operation":"direct_inspect","request_id":"r","status":"ok","result":{"evidence_sha256":"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","content_b64":"","encoding":"utf-8","truncated":false}}`,
	}
	for op, in := range valid {
		if _, err := DecodeResponse([]byte(in)); err != nil {
			t.Errorf("%s: %v", op, err)
		}
	}
	for _, in := range []string{
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"ok"}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"error","result":{"data_sha256":"0000000000000000000000000000000000000000000000000000000000000000"},"error":{"code":"timeout","message":"x"}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"error","error":{"code":"oops","message":"x"}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"error","error":{"code":"timeout","message":"/native/path"}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_read","request_id":"r","status":"ok","result":{"data_sha256":"0000000000000000000000000000000000000000000000000000000000000000","content_b64":"%%%","offset":0,"total_size":0,"truncated":false}}`,
		`{"schema":"gentle-ai.direct-operation/v1","operation":"direct_edit","request_id":"r","status":"ok","result":{"result_sha256":"0000000000000000000000000000000000000000000000000000000000000000","changed":true,"publication":"unknown"}}`,
	} {
		if _, err := DecodeResponse([]byte(in)); err == nil {
			t.Errorf("accepted %s", in)
		}
	}
}

func TestOperationResultBuilders(t *testing.T) {
	read := NewReadResult([]byte{0, 1, 2}, []byte{1, 2}, 1, 3, false)
	if read.DataSHA256 != DigestSHA256([]byte{0, 1, 2}) || read.ContentB64 != base64.StdEncoding.EncodeToString([]byte{1, 2}) {
		t.Fatalf("read result = %#v", read)
	}
	if _, err := read.CanonicalJSON(); err != nil {
		t.Fatal(err)
	}
	inspect := NewInspectResult([]byte("a\nb\n"))
	if _, err := inspect.CanonicalJSON(); err != nil {
		t.Fatal(err)
	}
	if got := NewEditResult([]byte("x"), false).Publication; got != "unchanged" {
		t.Fatalf("publication = %q", got)
	}
}
