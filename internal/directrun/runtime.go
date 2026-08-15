package directrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

const registrationTTL = 5 * time.Minute

const AbortRequestSchema = "gentle-ai.direct-run-abort/v1"

// AbortRequest carries the same persisted principal that admitted the child.
// RepositoryIdentity is the opaque active lease storage key, never a path.
type AbortRequest struct {
	Schema             string      `json:"schema"`
	Identity           string      `json:"identity"`
	Revision           Digest      `json:"revision"`
	HandoffRevision    Digest      `json:"handoff_revision"`
	ParentSessionID    string      `json:"parent_session_id"`
	ParentCallID       string      `json:"parent_call_id"`
	Agent              string      `json:"agent"`
	RepositoryIdentity string      `json:"repository_identity"`
	ChildSessionID     string      `json:"child_session_id"`
	Reason             AbortReason `json:"reason"`
}

func (r AbortRequest) Validate() error {
	if r.Schema != AbortRequestSchema || !recordIdentifier(r.Identity) || !digestPattern.MatchString(string(r.Revision)) || !digestPattern.MatchString(string(r.HandoffRevision)) || !recordIdentifier(r.ParentSessionID) || !recordIdentifier(r.ParentCallID) || !recordAgent(r.Agent) || !recordIdentifier(r.RepositoryIdentity) || (r.ChildSessionID != "" && !recordIdentifier(r.ChildSessionID)) || (r.Reason != AbortCancelled && r.Reason != AbortRevoked && r.Reason != AbortExpired) {
		return ErrInvalidTransition
	}
	return nil
}
func (r AbortRequest) CanonicalJSON() ([]byte, error) { return marshal(r, r.Validate) }
func DecodeAbortRequest(payload []byte) (AbortRequest, error) {
	var request AbortRequest
	if err := decodeEnvelope(payload, &request, set("schema", "identity", "revision", "handoff_revision", "parent_session_id", "parent_call_id", "agent", "repository_identity", "child_session_id", "reason")); err != nil {
		return request, err
	}
	return request, request.Validate()
}

// Runtime owns the repository lease, record backend, and file authority for one repository.
type Runtime struct {
	lease   *reviewtransaction.RepositoryIdentityLease
	git     *retainedGitInspector
	store   *Store
	backend interface{ Close() error }
	now     func() time.Time
	mu      sync.Mutex
	closed  bool
}

func OpenRuntime(ctx context.Context, cwd string) (*Runtime, error) {
	lease, err := reviewtransaction.OpenRepositoryIdentityLease(ctx, cwd)
	if err != nil {
		return nil, err
	}
	backend, err := newRecordBackend(ctx, lease)
	if err != nil {
		return nil, err
	}
	store, err := NewStore(backend, lease)
	if err != nil {
		_ = backend.Close()
		return nil, err
	}
	git, err := newRetainedGitInspector(lease)
	if err != nil {
		_ = backend.Close()
		return nil, err
	}
	return &Runtime{lease: lease, git: git, store: store, backend: backend, now: time.Now}, nil
}

func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.git != nil {
		r.git.Close()
	}
	return r.backend.Close()
}
func (r *Runtime) Issue(ctx context.Context, handoff Handoff) (Record, error) {
	if err := r.available(ctx); err != nil {
		return Record{}, err
	}
	files, err := newOperationFiles(ctx, r.lease, handoff)
	if err != nil {
		return Record{}, err
	}
	if err := files.Close(); err != nil {
		return Record{}, ErrOperationUnavailable
	}
	return r.store.Issue(ctx, handoff)
}
func (r *Runtime) Read(ctx context.Context, identity string) (Record, error) {
	if err := r.available(ctx); err != nil {
		return Record{}, err
	}
	return r.store.Read(ctx, identity)
}
func (r *Runtime) RegisterTask(ctx context.Context, identity string, expected Digest, parentSession, parentCall, agent string) (Record, error) {
	if err := r.available(ctx); err != nil {
		return Record{}, err
	}
	now := r.now().Unix()
	return r.store.Register(ctx, identity, expected, parentSession, parentCall, agent, r.lease.StorageKey(), now+int64(registrationTTL/time.Second), now)
}
func (r *Runtime) Finish(ctx context.Context, identity string, expected Digest, sessionID string, outcome RecordOutcome, output WorkerOutput) (Record, error) {
	if err := r.available(ctx); err != nil {
		return Record{}, err
	}
	record, err := r.store.Read(ctx, identity)
	if err != nil {
		return Record{}, err
	}
	if record.State != RecordConsumed || record.Revision != expected || record.SessionID != sessionID || record.RepositoryIdentity != r.lease.StorageKey() {
		return Record{}, ErrInvalidTransition
	}
	return r.store.Finish(ctx, identity, expected, outcome, output)
}
func (r *Runtime) Abort(ctx context.Context, request AbortRequest) (Record, error) {
	if err := request.Validate(); err != nil {
		return Record{}, err
	}
	if err := r.available(ctx); err != nil {
		return Record{}, err
	}
	record, err := r.store.Read(ctx, request.Identity)
	if err != nil {
		return Record{}, err
	}
	if record.Revision != request.Revision || record.Handoff.Revision != request.HandoffRevision || record.RepositoryIdentity != r.lease.StorageKey() || request.RepositoryIdentity != r.lease.StorageKey() || record.ParentSessionID != request.ParentSessionID || record.ParentCallID != request.ParentCallID || record.Agent != request.Agent {
		return Record{}, ErrInvalidTransition
	}
	switch record.State {
	case RecordRegistered:
		if request.ChildSessionID != "" {
			return Record{}, ErrInvalidTransition
		}
	case RecordBound, RecordConsumed:
		if request.ChildSessionID == "" || request.ChildSessionID != record.SessionID {
			return Record{}, ErrInvalidTransition
		}
	default:
		// Issued authority has no persisted principal and terminals cannot replay.
		return Record{}, transitionError(record.State)
	}
	return r.store.Abort(ctx, request.Identity, request.Revision, request.Reason)
}
func (r *Runtime) Execute(ctx context.Context, request Request) (Response, error) {
	if err := request.Validate(); err != nil {
		return Response{}, err
	}
	if err := r.available(ctx); err != nil {
		return wire(request, wireCode(ctx, err), "operation unavailable"), nil
	}
	var record Record
	for attempts := 0; attempts < 4; attempts++ {
		current, err := r.store.Read(ctx, request.Identity)
		if err != nil {
			return wire(request, admissionWireCode(ctx, err), "request denied"), nil
		}
		if current.Handoff.Revision != Digest(request.HandoffRevision) || current.RepositoryIdentity != "" && current.RepositoryIdentity != r.lease.StorageKey() {
			return wire(request, "unauthorized", "request denied"), nil
		}
		switch current.State {
		case RecordRegistered:
			record, err = r.store.Bind(ctx, request.Identity, Digest(request.BindingRevision), request.ParentSessionID, request.ParentCallID, request.Agent, request.SessionID, r.lease.StorageKey(), r.now().Unix())
			if errors.Is(err, ErrCASConflict) || errors.Is(err, ErrRevisionMismatch) {
				continue
			}
			if err != nil {
				return wire(request, admissionWireCode(ctx, err), "request denied"), nil
			}
		case RecordBound:
			record = current
			if record.SessionID != request.SessionID || record.RepositoryIdentity != r.lease.StorageKey() || record.ParentSessionID != request.ParentSessionID || record.ParentCallID != request.ParentCallID || record.Agent != request.Agent {
				return wire(request, "unauthorized", "request denied"), nil
			}
		case RecordConsumed:
			record = current
			if record.SessionID != request.SessionID || record.RepositoryIdentity != r.lease.StorageKey() || record.ParentSessionID != request.ParentSessionID || record.ParentCallID != request.ParentCallID || record.Agent != request.Agent {
				return wire(request, "unauthorized", "request denied"), nil
			}
			return r.executeOperation(ctx, record, request)
		default:
			return wire(request, "unauthorized", "request denied"), nil
		}
		if record.State == RecordBound {
			record, err = r.store.Consume(ctx, request.Identity, record.Revision, request.SessionID, r.lease.StorageKey())
			if errors.Is(err, ErrCASConflict) {
				continue
			}
			if err != nil {
				return wire(request, admissionWireCode(ctx, err), "request denied"), nil
			}
			return r.executeOperation(ctx, record, request)
		}
	}
	return wire(request, "unauthorized", "request denied"), nil
}
func (r *Runtime) executeOperation(ctx context.Context, record Record, request Request) (Response, error) {
	if err := r.lease.Validate(ctx); err != nil {
		return wire(request, wireCode(ctx, ErrOperationUnavailable), "operation unavailable"), nil
	}
	if strings.HasPrefix(record.Agent, "gentle-reviewer") && request.Operation == "direct_edit" {
		return wire(request, "unauthorized", "request denied"), nil
	}
	files, err := newOperationFiles(ctx, r.lease, record.Handoff)
	if err != nil {
		return Response{}, err
	}
	if err := r.lease.Validate(ctx); err != nil {
		return wire(request, wireCode(ctx, ErrOperationUnavailable), "operation unavailable"), nil
	}
	defer files.Close()
	var result any
	switch request.Operation {
	case "direct_exec":
		index, timeout, e := decodeExecPayload(request.Payload)
		if e != nil || index >= len(record.Handoff.Verification) {
			err = ErrOperationUnsupported
		} else {
			result, err = runRetainedCommand(ctx, r.lease.Identity().RepositoryRoot, record.Handoff.Verification[index], timeout)
		}
	case "direct_read":
		p, o, l, e := decodeReadPayload(request.Payload)
		if e != nil {
			err = e
		} else {
			result, err = files.Read(ctx, p, o, l)
		}
	case "direct_edit":
		p, sha, repl, e := decodeEditPayload(request.Payload)
		if e != nil {
			err = e
		} else {
			result, err = files.Edit(ctx, p, sha, repl)
		}
	case "direct_inspect":
		query, path, e := decodeInspectPayload(request.Payload)
		if e != nil {
			err = e
			break
		}
		switch query {
		case "tree":
			result, err = files.Tree(ctx, path)
		case "git_status":
			var inspection gitInspection
			inspection, err = r.git.inspect(ctx)
			if err == nil {
				result, err = inspection.statusResult()
			}
		case "git_diff":
			var inspection gitInspection
			inspection, err = r.git.inspect(ctx)
			if err == nil {
				result, err = inspection.diffResult()
			}
		default:
			err = ErrOperationUnsupported
		}
	default:
		return wire(request, "unsupported_operation", "operation unsupported"), nil
	}
	if err != nil {
		return wire(request, wireCode(ctx, err), "operation failed"), nil
	}
	payload, err := json.Marshal(result)
	if err != nil {
		return Response{}, err
	}
	response := Response{Schema: OperationSchema, Operation: request.Operation, RequestID: request.RequestID, Status: "ok", Result: payload}
	return response, response.Validate()
}

func decodeExecPayload(raw json.RawMessage) (int, time.Duration, error) {
	var value struct {
		CommandIndex int    `json:"command_index"`
		TimeoutMS    *int64 `json:"timeout_ms"`
	}
	if err := json.Unmarshal(raw, &value); err != nil || value.CommandIndex < 0 {
		return 0, 0, ErrOperationUnsupported
	}
	timeout := 120 * time.Second
	if value.TimeoutMS != nil {
		if *value.TimeoutMS < 1 || *value.TimeoutMS > 120000 {
			return 0, 0, ErrOperationUnsupported
		}
		timeout = time.Duration(*value.TimeoutMS) * time.Millisecond
	}
	return value.CommandIndex, timeout, nil
}
func (r *Runtime) available(ctx context.Context) error {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return ErrBackendUnavailable
	}
	return ctx.Err()
}
func wire(request Request, code, message string) Response {
	return Response{Schema: OperationSchema, Operation: request.Operation, RequestID: request.RequestID, Status: "error", Error: &WireError{Code: code, Message: message}}
}
func wireCode(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, ErrOperationInvalidPath) {
		return "invalid_path"
	}
	if errors.Is(err, ErrOperationNotFound) {
		return "not_found"
	}
	if errors.Is(err, ErrOperationConflict) {
		return "conflict"
	}
	if errors.Is(err, ErrOperationLimit) {
		return "limit_exceeded"
	}
	if errors.Is(err, ErrOperationUnsupported) {
		return "unsupported_operation"
	}
	if errors.Is(err, ErrCommandTargetUnsupported) {
		return "unsupported_command_target"
	}
	return "backend_failure"
}
func admissionWireCode(ctx context.Context, err error) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, ErrIdentityChanged) {
		return "repository_unavailable"
	}
	if errors.Is(err, ErrBackendUnavailable) || errors.Is(err, ErrOperationUnavailable) {
		return "backend_failure"
	}
	if errors.Is(err, ErrNotFound) {
		return "unauthenticated"
	}
	return "unauthorized"
}
func decodeInspectPayload(raw json.RawMessage) (query, path string, err error) {
	var value struct {
		Query string  `json:"query"`
		Path  *string `json:"path"`
	}
	if json.Unmarshal(raw, &value) != nil || (value.Query != "tree" && value.Query != "git_status" && value.Query != "git_diff") || (value.Query != "tree" && value.Path != nil) {
		return "", "", ErrOperationUnsupported
	}
	if value.Path != nil {
		path = *value.Path
	}
	return value.Query, path, nil
}
