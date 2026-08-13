package directrun

import (
	"bytes"
	"encoding/json"
	"testing"
)

func testHandoff(t *testing.T) Handoff {
	h, err := NewHandoff("run-3026-example", "worker-3026-example", []string{"/workspace/repo"}, "add the direct-run contract primitives", []string{"strict decoding rejects unknown fields", "canonical digests are stable"}, []Command{{[]string{"go", "test", "./internal/directrun"}, "/workspace/repo"}})
	check(t, err)
	return h
}

const testOutputDigest = Digest("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")

func testOutput(h Handoff) WorkerOutput {
	return WorkerOutput{WorkerOutputSchema, h.OutputBinding(), []string{"internal/directrun/contract.go"}, []VerificationResult{{0, 0, testOutputDigest}}, "worker completed the bounded run"}
}
func jsonOf(value any) []byte { payload, _ := json.Marshal(value); return payload }
func check(t *testing.T, err error) {
	if err != nil {
		t.Fatal(err)
	}
}
func invalid(t *testing.T, h Handoff, edit func(*Handoff)) {
	t.Helper()
	h.Revision = ""
	edit(&h)
	if _, err := h.Seal(); err == nil {
		t.Fatal("accepted invalid handoff")
	}
}
func TestContractBindingAndCanonicalBytes(t *testing.T) {
	h := testHandoff(t)
	payload, err := h.CanonicalJSON()
	check(t, err)
	decoded, err := DecodeHandoff(payload)
	check(t, err)
	canonical, err := decoded.CanonicalJSON()
	check(t, err)
	revision, err := DeriveRevision(h)
	check(t, err)
	if !bytes.Equal(payload, canonical) || revision != h.Revision {
		t.Fatal("handoff canonical bytes or revision changed")
	}
	h.TargetBehavior = "tampered"
	if h.Validate() == nil {
		t.Fatal("accepted stale revision")
	}
	h = testHandoff(t)
	output := testOutput(h)
	outputPayload, err := output.CanonicalJSON()
	check(t, err)
	decodedOutput, err := DecodeWorkerOutput(outputPayload)
	check(t, err)
	if err := decodedOutput.ValidateAgainst(h); err != nil {
		t.Fatal(err)
	}
	for _, edit := range []func(*WorkerOutput){func(o *WorkerOutput) { o.Binding.HandoffIdentity = "run-other" }, func(o *WorkerOutput) {
		o.Binding.HandoffRevision = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	}, func(o *WorkerOutput) { o.Verification[0].CommandIndex = 1 }} {
		candidate := testOutput(h)
		edit(&candidate)
		if candidate.ValidateAgainst(h) == nil {
			t.Fatal("accepted mismatched output")
		}
	}
}
func TestDecodeRejectsNoncanonicalMissingAndNull(t *testing.T) {
	h := testHandoff(t)
	payload := jsonOf(h)
	var value map[string]any
	_ = json.Unmarshal(payload, &value)
	value["extra"] = true
	cases := map[string][]byte{"whitespace": append([]byte("\n"), append(payload, '\n')...), "escaping": bytes.Replace(payload, []byte("add the"), []byte(`\u0061dd the`), 1), "duplicate": append(bytes.TrimSuffix(payload, []byte("}")), []byte(`,"schema":"gentle-ai.direct-run/v1"}`)...), "trailing": append(payload, []byte("{}")...), "unknown": jsonOf(value), "reordered": []byte(`{"verification_commands":[{"argv":["go","test","./internal/directrun"],"cwd":"/workspace/repo"}],"acceptance_criteria":["strict decoding rejects unknown fields","canonical digests are stable"],"allowed_edit_roots":["/workspace/repo"],"worker":{"role":"gentle-worker","id":"worker-3026-example"},"revision":"` + string(h.Revision) + `","identity":"run-3026-example","target_behavior":"add the direct-run contract primitives","schema":"gentle-ai.direct-run/v1"}`)}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeHandoff(input); err == nil {
				t.Fatal("accepted noncanonical handoff")
			}
		})
	}
	delete(value, "extra")
	delete(value, "identity")
	if _, err := DecodeHandoff(jsonOf(value)); err == nil {
		t.Fatal("accepted missing identity")
	}
	for _, field := range []string{"command_index", "exit_code", "output_digest"} {
		out := map[string]any{}
		_ = json.Unmarshal(jsonOf(testOutput(h)), &out)
		out["verification"].([]any)[0].(map[string]any)[field] = nil
		if _, err := DecodeWorkerOutput(jsonOf(out)); err == nil {
			t.Fatalf("accepted null %s", field)
		}
	}
}
func TestValidationRejectsDuplicatesRoleAndUnsafeCommands(t *testing.T) {
	h := testHandoff(t)
	cases := map[string]func(*Handoff){"criterion": func(h *Handoff) { h.AcceptanceCriteria = append(h.AcceptanceCriteria, h.AcceptanceCriteria[0]) }, "command": func(h *Handoff) { h.Verification = append(h.Verification, h.Verification[0]) }, "role": func(h *Handoff) { h.Worker.Role = "gentle-reviewer" }, "shell": func(h *Handoff) { h.Verification[0].Argv = []string{"sh", "-c", "go test ./..."} }, "git mutation": func(h *Handoff) { h.Verification[0].Argv = []string{"git", "commit"} }, "git alias": func(h *Handoff) { h.Verification[0].Argv = []string{"/usr/bin/git", "status"} }, "assignment": func(h *Handoff) { h.Verification[0].Argv = []string{"FOO=bar", "go", "test"} }, "interpreter": func(h *Handoff) { h.Verification[0].Argv = []string{"python", "-c", "print(1)"} }}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) { invalid(t, h, edit) })
	}
}
