//go:build linux

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/directrun"
)

func TestRunDirectRunAdmissionPublicLifecycle(t *testing.T) {
	repo := directRunRepo(t)
	handoff := directRunHandoff(t, repo, "run-admission")
	record := directRunIssue(t, repo, handoff)
	record = directRunRegister(t, repo, record)

	request := directRunRequest(t, record, "child-admission", "direct_read", `{"path":"note.txt","offset":0,"limit":64}`)
	response := directRunExecute(t, repo, request)
	if response.Status != "ok" || string(response.Result) != `{"data_sha256":"9160d4be34c8695bd172a76c7c7966587ea5a4d991ad22c87b2b91af54aa9ebb","content_b64":"YmVmb3JlCg==","offset":0,"total_size":7,"truncated":false}` {
		t.Fatalf("first read response = %s", mustJSON(t, response))
	}
	consumed := directRunInspect(t, repo, record.Handoff.Identity)
	if consumed.State != directrun.RecordConsumed || consumed.SessionID != "child-admission" {
		t.Fatalf("first read did not atomically bind and consume: %#v", consumed)
	}

	for _, request := range []directrun.Request{
		directRunRequest(t, consumed, "child-admission", "direct_read", `{"path":"note.txt","offset":0,"limit":3}`),
		directRunRequest(t, consumed, "child-admission", "direct_edit", `{"path":"note.txt","base_sha256":"9160d4be34c8695bd172a76c7c7966587ea5a4d991ad22c87b2b91af54aa9ebb","replacements":[{"start":0,"end":6,"text":"after"}]}`),
		directRunRequest(t, consumed, "child-admission", "direct_inspect", `{"query":"tree"}`),
	} {
		response := directRunExecute(t, repo, request)
		if response.Status != "ok" {
			t.Fatalf("%s response = %s", request.Operation, mustJSON(t, response))
		}
	}
	if got, err := os.ReadFile(filepath.Join(repo, "note.txt")); err != nil || string(got) != "after\n" {
		t.Fatalf("edit side effect = %q, %v", got, err)
	}

	output := directRunOutput(t, handoff)
	finished := directRunFinish(t, repo, consumed, "child-admission", directrun.OutcomeSucceeded, output)
	if finished.State != directrun.RecordFinished || finished.Outcome != directrun.OutcomeSucceeded || finished.Output == nil || mustJSON(t, *finished.Output) != mustJSON(t, output) {
		t.Fatalf("finished record = %#v", finished)
	}
	assertDirectFailure(t, repo, "finish", directRunFinishInput(t, finished, "child-admission", directrun.OutcomeSucceeded, output))
}

func TestRunDirectRunAdmissionRaceAndWireRefusals(t *testing.T) {
	repo := directRunRepo(t)
	record := directRunRegister(t, repo, directRunIssue(t, repo, directRunHandoff(t, repo, "run-race")))
	request := func(child, operation string) directrun.Request {
		return directRunRequest(t, record, child, operation, `{"path":"note.txt","offset":0,"limit":64}`)
	}
	start := make(chan struct{})
	responses := make(chan directrun.Response, 2)
	var group sync.WaitGroup
	for _, child := range []string{"child-one", "child-two"} {
		group.Add(1)
		go func(child string) {
			defer group.Done()
			<-start
			responses <- directRunExecute(t, repo, request(child, "direct_read"))
		}(child)
	}
	close(start)
	group.Wait()
	close(responses)
	ok, denied := 0, 0
	for response := range responses {
		if response.Status == "ok" {
			ok++
		} else if response.Error.Code == "unauthorized" {
			denied++
		}
	}
	if ok != 1 || denied != 1 {
		t.Fatalf("race outcomes ok=%d denied=%d", ok, denied)
	}
	consumed := directRunInspect(t, repo, record.Handoff.Identity)
	if consumed.State != directrun.RecordConsumed {
		t.Fatalf("race record = %#v", consumed)
	}

	response := directRunExecute(t, repo, directRunRequest(t, consumed, consumed.SessionID, "direct_exec", `{"command_index":0}`))
	if response.Status != "error" || response.Error.Code != "unsupported_operation" || response.Error.Message != "operation unsupported" {
		t.Fatalf("direct_exec = %#v", response)
	}
	for _, child := range []string{"wrong-child"} {
		response := directRunExecute(t, repo, directRunRequest(t, consumed, child, "direct_read", `{"path":"note.txt","offset":0,"limit":1}`))
		if response.Status != "error" || response.Error.Code != "unauthorized" || response.Error.Message != "request denied" {
			t.Fatalf("wrong child response = %#v", response)
		}
	}
}

func TestRunDirectRunStrictInputsNeverWriteSuccess(t *testing.T) {
	repo := directRunRepo(t)
	handoff := directRunHandoff(t, repo, "run-strict")
	record := directRunIssue(t, repo, handoff)
	register := directRunRegisterInput(record)
	execute := directRunRequest(t, record, "child-strict", "direct_read", `{"path":"note.txt","offset":0,"limit":1}`)
	finish := directRunFinishInput(t, record, "child-strict", directrun.OutcomeSucceeded, directRunOutput(t, handoff))
	abort := directrun.AbortRequest{Schema: directrun.AbortRequestSchema, Identity: record.Handoff.Identity, Revision: record.Revision, HandoffRevision: handoff.Revision, ParentSessionID: "parent-3026", ParentCallID: "call-3026", Agent: directrun.WorkerRole, RepositoryIdentity: "repository", Reason: directrun.AbortCancelled}
	abortJSON, err := abort.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		command string
		valid   []byte
	}{
		{"issue", mustHandoffJSON(t, handoff)}, {"register", register}, {"inspect", []byte(`{"identity":"run-strict"}`)}, {"execute", mustRequestJSON(t, execute)}, {"finish", finish}, {"abort", abortJSON},
	}
	for _, test := range cases {
		t.Run(test.command, func(t *testing.T) {
			for _, input := range [][]byte{nil, []byte(`{}`), []byte(`null`), append(test.valid, []byte(` {}`)...), bytes.Repeat([]byte("x"), 1<<20+1), nonCanonical(test.valid), unknownField(test.valid), duplicateFirstField(test.valid)} {
				assertDirectFailure(t, repo, test.command, input)
			}
		})
	}
	for _, args := range [][]string{{}, {"unknown", "--cwd", repo}, {"inspect", "--cwd", repo, "--cwd", repo}, {"inspect", "--bad", repo}} {
		var stdout bytes.Buffer
		if err := RunDirectRun(args, bytes.NewReader([]byte(`{"identity":"run-strict"}`)), &stdout); !errors.Is(err, directrun.ErrInvalidTransition) || stdout.Len() != 0 {
			t.Fatalf("args %v: %v stdout=%q", args, err, stdout.String())
		}
	}
}

func TestRunDirectRunFinishAndAbortRefuseMutation(t *testing.T) {
	for _, outcome := range []directrun.RecordOutcome{directrun.OutcomeSucceeded, directrun.OutcomeFailed} {
		t.Run(string(outcome), func(t *testing.T) {
			repo := directRunRepo(t)
			handoff := directRunHandoff(t, repo, "run-finish-"+string(outcome))
			record := directRunRegister(t, repo, directRunIssue(t, repo, handoff))
			response := directRunExecute(t, repo, directRunRequest(t, record, "child-finish", "direct_read", `{"path":"note.txt","offset":0,"limit":1}`))
			if response.Status != "ok" {
				t.Fatalf("consume = %#v", response)
			}
			consumed := directRunInspect(t, repo, handoff.Identity)
			output := directRunOutput(t, handoff)
			for _, input := range [][]byte{
				directRunFinishInput(t, consumed, "wrong-child", outcome, output),
				directRunFinishInput(t, consumed, "child-finish", outcome, directrun.WorkerOutput{}),
			} {
				assertDirectFailure(t, repo, "finish", input)
			}
			finished := directRunFinish(t, repo, consumed, "child-finish", outcome, output)
			if finished.State != directrun.RecordFinished || finished.Outcome != outcome {
				t.Fatalf("terminal = %#v", finished)
			}
			assertDirectFailure(t, repo, "finish", directRunFinishInput(t, finished, "child-finish", outcome, output))
		})
	}

	repo := directRunRepo(t)
	record := directRunRegister(t, repo, directRunIssue(t, repo, directRunHandoff(t, repo, "run-abort-race")))
	abort := directrun.AbortRequest{Schema: directrun.AbortRequestSchema, Identity: record.Handoff.Identity, Revision: record.Revision, HandoffRevision: record.Handoff.Revision, ParentSessionID: "parent-3026", ParentCallID: "call-3026", Agent: directrun.WorkerRole, RepositoryIdentity: record.RepositoryIdentity, Reason: directrun.AbortCancelled}
	payload, err := abort.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			var stdout bytes.Buffer
			results <- RunDirectRun([]string{"abort", "--cwd", repo}, bytes.NewReader(payload), &stdout)
		}()
	}
	close(start)
	winners := 0
	for range 2 {
		if err := <-results; err == nil {
			winners++
		} else if !errors.Is(err, directrun.ErrInvalidTransition) {
			t.Fatal(err)
		}
	}
	if winners != 1 {
		t.Fatalf("abort winners = %d", winners)
	}
}

func directRunRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, key := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "TMPDIR", "TMP", "TEMP"} {
		t.Setenv(key, filepath.Join(root, "env", key))
	}
	repo := filepath.Join(root, "repo")
	if output, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(repo, "note.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}
func directRunHandoff(t *testing.T, repo, identity string) directrun.Handoff {
	t.Helper()
	h, err := directrun.NewHandoff(identity, "worker-3026", []string{repo}, "update note", []string{"note updated"}, []directrun.Command{{Argv: []string{"go", "version"}, CWD: repo}})
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func directRunIssue(t *testing.T, repo string, h directrun.Handoff) directrun.Record {
	t.Helper()
	var r directrun.Record
	runDirectJSON(t, repo, "issue", mustHandoffJSON(t, h), &r)
	return r
}
func directRunRegister(t *testing.T, repo string, r directrun.Record) directrun.Record {
	t.Helper()
	var next directrun.Record
	runDirectJSON(t, repo, "register", directRunRegisterInput(r), &next)
	return next
}
func directRunRegisterInput(r directrun.Record) []byte {
	b, _ := json.Marshal(struct {
		Identity        string           `json:"identity"`
		Revision        directrun.Digest `json:"revision"`
		ParentSessionID string           `json:"parent_session_id"`
		ParentCallID    string           `json:"parent_call_id"`
		Agent           string           `json:"agent"`
	}{r.Handoff.Identity, r.Revision, "parent-3026", "call-3026", directrun.WorkerRole})
	return b
}
func directRunInspect(t *testing.T, repo, identity string) directrun.Record {
	t.Helper()
	var r directrun.Record
	runDirectJSON(t, repo, "inspect", []byte(`{"identity":"`+identity+`"}`), &r)
	return r
}
func directRunRequest(t *testing.T, r directrun.Record, child, operation, payload string) directrun.Request {
	t.Helper()
	request := directrun.Request{Schema: directrun.OperationSchema, Identity: r.Handoff.Identity, Operation: operation, RequestID: "request-" + child + "-" + operation, SessionID: child, HandoffRevision: string(r.Handoff.Revision), BindingRevision: string(r.Revision), ParentSessionID: "parent-3026", ParentCallID: "call-3026", Agent: directrun.WorkerRole, Payload: json.RawMessage(payload)}
	if err := request.Validate(); err != nil {
		t.Fatal(err)
	}
	return request
}
func directRunExecute(t *testing.T, repo string, request directrun.Request) directrun.Response {
	t.Helper()
	var response directrun.Response
	runDirectJSON(t, repo, "execute", mustRequestJSON(t, request), &response)
	return response
}
func directRunOutput(t *testing.T, h directrun.Handoff) directrun.WorkerOutput {
	t.Helper()
	output := directrun.WorkerOutput{Schema: directrun.WorkerOutputSchema, Binding: h.OutputBinding(), ChangedPaths: []string{"note.txt"}, Verification: []directrun.VerificationResult{{CommandIndex: 0, ExitCode: 0, OutputDigest: directrun.Digest("sha256:" + strings.Repeat("0", 64))}}, Summary: "note updated"}
	if err := output.Validate(); err != nil {
		t.Fatal(err)
	}
	return output
}
func directRunFinishInput(t *testing.T, r directrun.Record, child string, outcome directrun.RecordOutcome, output directrun.WorkerOutput) []byte {
	t.Helper()
	b, err := json.Marshal(struct {
		Identity  string                  `json:"identity"`
		Revision  directrun.Digest        `json:"revision"`
		SessionID string                  `json:"session_id"`
		Outcome   directrun.RecordOutcome `json:"outcome"`
		Output    directrun.WorkerOutput  `json:"output"`
	}{r.Handoff.Identity, r.Revision, child, outcome, output})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func directRunFinish(t *testing.T, repo string, r directrun.Record, child string, outcome directrun.RecordOutcome, output directrun.WorkerOutput) directrun.Record {
	t.Helper()
	var next directrun.Record
	runDirectJSON(t, repo, "finish", directRunFinishInput(t, r, child, outcome, output), &next)
	return next
}
func mustHandoffJSON(t *testing.T, h directrun.Handoff) []byte {
	t.Helper()
	b, err := h.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func mustRequestJSON(t *testing.T, r directrun.Request) []byte {
	t.Helper()
	b, err := r.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func mustJSON(t *testing.T, value any) string {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func assertDirectFailure(t *testing.T, repo, command string, input []byte) {
	t.Helper()
	var stdout bytes.Buffer
	if err := RunDirectRun([]string{command, "--cwd", repo}, bytes.NewReader(input), &stdout); !errors.Is(err, directrun.ErrInvalidTransition) || stdout.Len() != 0 {
		t.Fatalf("%s accepted invalid input: %v stdout=%q", command, err, stdout.String())
	}
}
func nonCanonical(value []byte) []byte { return append([]byte(" "), value...) }
func unknownField(value []byte) []byte {
	return append(append([]byte(nil), value[:len(value)-1]...), []byte(`,"unknown":true}`)...)
}
func duplicateFirstField(value []byte) []byte { return append([]byte(`{"schema":"x",`), value[1:]...) }
