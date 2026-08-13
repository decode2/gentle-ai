package directrun

import (
	"bytes"
	"errors"
	"testing"
)

func recordFor(t *testing.T) Record {
	t.Helper()
	r, err := IssueRecord(testHandoff(t))
	check(t, err)
	return r
}
func bindFor(t *testing.T, r Record) Record {
	t.Helper()
	next, err := r.Bind(r.Revision, "session-3026", "repo-3026")
	check(t, err)
	return next
}
func consumeFor(t *testing.T, r Record) Record {
	t.Helper()
	next, err := r.Consume(r.Revision)
	check(t, err)
	return next
}
func unchanged(t *testing.T, r Record, before []byte) {
	t.Helper()
	after, err := r.CanonicalJSON()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("receiver mutated")
	}
}
func TestRecordLifecycle(t *testing.T) {
	for _, tt := range []struct {
		name    string
		outcome RecordOutcome
	}{
		{"succeeds", OutcomeSucceeded}, {"fails", OutcomeFailed},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := recordFor(t)
			before, _ := r.CanonicalJSON()
			b := bindFor(t, r)
			unchanged(t, r, before)
			before, _ = b.CanonicalJSON()
			r = consumeFor(t, b)
			unchanged(t, b, before)
			before, _ = r.CanonicalJSON()
			got, err := r.Finish(r.Revision, tt.outcome, testOutput(r.Handoff))
			if err != nil || got.State != RecordFinished || got.Outcome != tt.outcome || got.Output == nil {
				t.Fatalf("Finish = %#v, %v", got, err)
			}
			unchanged(t, r, before)
			if _, err := got.Consume(got.Revision); !errors.Is(err, ErrReplay) {
				t.Fatalf("replay = %v", err)
			}
		})
	}
}
func TestRecordAbortAndTransitions(t *testing.T) {
	for _, tt := range []struct {
		name   string
		record func(*testing.T) Record
	}{
		{"issued", recordFor}, {"bound", func(t *testing.T) Record { return bindFor(t, recordFor(t)) }}, {"consumed", func(t *testing.T) Record { return consumeFor(t, bindFor(t, recordFor(t))) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := tt.record(t)
			before, _ := r.CanonicalJSON()
			got, err := r.Abort(r.Revision, AbortCancelled)
			if err != nil || got.State != RecordAborted || got.Output != nil {
				t.Fatalf("Abort = %#v, %v", got, err)
			}
			unchanged(t, r, before)
			if _, err := got.Finish(got.Revision, OutcomeSucceeded, testOutput(got.Handoff)); !errors.Is(err, ErrReplay) {
				t.Fatalf("terminal finish = %v", err)
			}
		})
	}
	r := recordFor(t)
	if _, err := r.Bind(Digest("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"), "session-3026", "repo-3026"); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("stale bind = %v", err)
	}
	if _, err := bindFor(t, r).Consume(r.Revision); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("stale consume = %v", err)
	}
	if _, err := r.Abort(Digest("sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"), AbortCancelled); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("stale abort = %v", err)
	}
	if _, err := r.Abort(r.Revision, "other"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid abort = %v", err)
	}
	for _, err := range []error{func() error { _, err := r.Consume(r.Revision); return err }(), func() error { _, err := r.Finish(r.Revision, OutcomeSucceeded, testOutput(r.Handoff)); return err }(), func() error {
		b := bindFor(t, r)
		_, err := b.Bind(b.Revision, "session-3026", "repo-3026")
		return err
	}()} {
		if !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("illegal transition = %v", err)
		}
	}
	if _, err := consumeFor(t, bindFor(t, r)).Finish(r.Revision, OutcomeSucceeded, testOutput(r.Handoff)); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("stale finish = %v", err)
	}
}
func TestRecordRejectsMismatchedOutputAndIdentity(t *testing.T) {
	r := consumeFor(t, bindFor(t, recordFor(t)))
	output := testOutput(r.Handoff)
	output.Binding.HandoffIdentity = "other-run"
	if _, err := r.Finish(r.Revision, OutcomeSucceeded, output); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("foreign output = %v", err)
	}
	if _, err := recordFor(t).Bind(recordFor(t).Revision, "/session", "repo-3026"); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("path identity = %v", err)
	}
}

func TestRecordCanonicalDecodeAndTampering(t *testing.T) {
	r := consumeFor(t, bindFor(t, recordFor(t)))
	r, err := r.Finish(r.Revision, OutcomeSucceeded, testOutput(r.Handoff))
	check(t, err)
	payload, err := r.CanonicalJSON()
	check(t, err)
	decoded, err := DecodeRecord(payload)
	check(t, err)
	decodedPayload, decodedErr := decoded.CanonicalJSON()
	if decodedErr != nil || !bytes.Equal(decodedPayload, payload) {
		t.Fatalf("DecodeRecord = %#v, %v", decoded, decodedErr)
	}
	for _, bad := range [][]byte{
		append([]byte("\n"), payload...), append(payload, []byte("{}")...), bytes.Replace(payload, []byte(`,"revision":`), []byte(`,"unknown":true,"revision":`), 1), bytes.Replace(payload, []byte(`"state":"finished"`), []byte(`"state":null`), 1), bytes.Replace(payload, []byte(`"state":"finished"`), []byte(`"state":"bound"`), 1), bytes.Replace(payload, []byte(`"outcome":"succeeded"`), []byte(`"outcome":"failed"`), 1), bytes.Replace(payload, []byte(`"session_id":"session-3026"`), []byte(`"session_id":"other"`), 1), bytes.Replace(payload, []byte(`"role":"gentle-worker"`), []byte(`"role":"gentle-worker","role":"gentle-worker"`), 1), bytes.Replace(payload, []byte(`"output":{`), []byte(`"output":null,"extra":{`), 1), append(bytes.TrimSuffix(payload, []byte("}")), []byte(`,"schema":"x"}`)...),
	} {
		if _, err := DecodeRecord(bad); !errors.Is(err, ErrCorruptRecord) {
			t.Fatalf("accepted %s: %v", bad, err)
		}
	}
	if r.Revision != decoded.Revision {
		t.Fatal("revision was not deterministic")
	}
}
