//go:build linux

package directrun

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestRuntimeAbortRequiresPersistedPrincipal(t *testing.T) {
	repo := t.TempDir()
	if output, err := exec.Command("git", "init", "--quiet", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	runtime, err := OpenRuntime(t.Context(), repo)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	handoff, err := (Handoff{Schema: HandoffSchema, Identity: "run-3026", Worker: WorkerIdentity{Role: WorkerRole, ID: "worker-3026"}, AllowedEditRoots: []string{repo}, TargetBehavior: "change", AcceptanceCriteria: []string{"criterion"}, Verification: []Command{{Argv: []string{"go", "version"}, CWD: repo}}}).Seal()
	if err != nil {
		t.Fatal(err)
	}
	issued, err := runtime.Issue(t.Context(), handoff)
	if err != nil {
		t.Fatal(err)
	}
	request := func(record Record) AbortRequest {
		return AbortRequest{Schema: AbortRequestSchema, Identity: handoff.Identity, Revision: record.Revision, HandoffRevision: handoff.Revision, ParentSessionID: "parent-3026", ParentCallID: "call-3026", Agent: WorkerRole, RepositoryIdentity: runtime.lease.StorageKey(), Reason: AbortCancelled}
	}
	if _, err := runtime.Abort(t.Context(), request(issued)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("issued abort = %v", err)
	}
	registered, err := runtime.RegisterTask(t.Context(), handoff.Identity, issued.Revision, "parent-3026", "call-3026", WorkerRole)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*AbortRequest){
		func(r *AbortRequest) { r.ParentSessionID = "other" },
		func(r *AbortRequest) { r.ParentCallID = "other" },
		func(r *AbortRequest) { r.Agent = "gentle-worker-profile" },
		func(r *AbortRequest) { r.RepositoryIdentity = "other" },
		func(r *AbortRequest) {
			r.HandoffRevision = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
		},
	} {
		candidate := request(registered)
		mutate(&candidate)
		if _, err := runtime.Abort(context.Background(), candidate); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("unauthorized abort = %v", err)
		}
	}
	aborted, err := runtime.Abort(t.Context(), request(registered))
	if err != nil || aborted.State != RecordAborted {
		t.Fatalf("authorized abort = %#v, %v", aborted, err)
	}
	if _, err := runtime.Abort(t.Context(), request(aborted)); !errors.Is(err, ErrReplay) {
		t.Fatalf("terminal abort = %v", err)
	}
}

func TestAbortRequestCanonicalDecode(t *testing.T) {
	request := AbortRequest{Schema: AbortRequestSchema, Identity: "run-3026", Revision: Digest("sha256:0000000000000000000000000000000000000000000000000000000000000000"), HandoffRevision: Digest("sha256:1111111111111111111111111111111111111111111111111111111111111111"), ParentSessionID: "parent-3026", ParentCallID: "call-3026", Agent: WorkerRole, RepositoryIdentity: "repository-3026", ChildSessionID: "", Reason: AbortCancelled}
	payload, err := request.CanonicalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeAbortRequest(payload); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{append(payload, []byte("{}")...), []byte(`{"schema":"gentle-ai.direct-run-abort/v1","identity":"run-3026"}`), []byte(string(payload[:len(payload)-1]) + `,"unknown":true}`)} {
		if _, err := DecodeAbortRequest(bad); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}
