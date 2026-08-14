package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/gentleman-programming/gentle-ai/v2/internal/directrun"
)

// RunDirectRun is the JSON-only internal boundary for the future OpenCode adapter.
func RunDirectRun(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return directrun.ErrInvalidTransition
	}
	cwd, err := directRunCWD(args[1:])
	if err != nil {
		return err
	}
	runtime, err := directrun.OpenRuntime(context.Background(), cwd)
	if err != nil {
		return directrun.ErrOperationUnavailable
	}
	defer runtime.Close()
	switch args[0] {
	case "issue":
		payload, err := readDirectRunJSON(stdin)
		if err != nil {
			return err
		}
		handoff, err := directrun.DecodeHandoff(payload)
		if err != nil {
			return directrun.ErrInvalidTransition
		}
		record, err := runtime.Issue(context.Background(), handoff)
		return writeDirectRun(stdout, record, err)
	case "register":
		var input struct {
			Identity        string           `json:"identity"`
			Revision        directrun.Digest `json:"revision"`
			ParentSessionID string           `json:"parent_session_id"`
			ParentCallID    string           `json:"parent_call_id"`
			Agent           string           `json:"agent"`
		}
		if err := decodeDirectRun(stdin, &input); err != nil {
			return err
		}
		record, err := runtime.RegisterTask(context.Background(), input.Identity, input.Revision, input.ParentSessionID, input.ParentCallID, input.Agent)
		return writeDirectRun(stdout, record, err)
	case "inspect":
		var input struct {
			Identity string `json:"identity"`
		}
		if err := decodeDirectRun(stdin, &input); err != nil {
			return err
		}
		record, err := runtime.Read(context.Background(), input.Identity)
		return writeDirectRun(stdout, record, err)
	case "execute":
		payload, err := readDirectRunJSON(stdin)
		if err != nil {
			return err
		}
		request, err := directrun.DecodeRequest(payload)
		if err != nil {
			return directrun.ErrInvalidTransition
		}
		response, err := runtime.Execute(context.Background(), request)
		return writeDirectRun(stdout, response, err)
	case "finish":
		var input struct {
			Identity  string                  `json:"identity"`
			Revision  directrun.Digest        `json:"revision"`
			SessionID string                  `json:"session_id"`
			Outcome   directrun.RecordOutcome `json:"outcome"`
			Output    directrun.WorkerOutput  `json:"output"`
		}
		if err := decodeDirectRun(stdin, &input); err != nil {
			return err
		}
		record, err := runtime.Finish(context.Background(), input.Identity, input.Revision, input.SessionID, input.Outcome, input.Output)
		return writeDirectRun(stdout, record, err)
	case "abort":
		var input struct {
			Identity string                `json:"identity"`
			Revision directrun.Digest      `json:"revision"`
			Reason   directrun.AbortReason `json:"reason"`
		}
		if err := decodeDirectRun(stdin, &input); err != nil {
			return err
		}
		record, err := runtime.Abort(context.Background(), input.Identity, input.Revision, input.Reason)
		return writeDirectRun(stdout, record, err)
	default:
		return directrun.ErrInvalidTransition
	}
}
func directRunCWD(args []string) (string, error) {
	cwd := ""
	for i := 0; i < len(args); i++ {
		if args[i] != "--cwd" || i+1 == len(args) || cwd != "" {
			return "", directrun.ErrInvalidTransition
		}
		cwd = args[i+1]
		i++
	}
	if cwd != "" {
		return cwd, nil
	}
	return os.Getwd()
}
func readDirectRunJSON(stdin io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(stdin, 1<<20+1))
	if err != nil || len(b) == 0 || len(b) > 1<<20 {
		return nil, directrun.ErrInvalidTransition
	}
	return b, nil
}
func decodeDirectRun(stdin io.Reader, value any) error {
	b, err := readDirectRunJSON(stdin)
	if err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(value) != nil {
		return directrun.ErrInvalidTransition
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return directrun.ErrInvalidTransition
	}
	canonical, _ := json.Marshal(value)
	if !bytes.Equal(canonical, b) {
		return directrun.ErrInvalidTransition
	}
	return nil
}
func writeDirectRun(stdout io.Writer, value any, err error) error {
	if err != nil {
		return directrun.ErrInvalidTransition
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return directrun.ErrInvalidTransition
	}
	_, err = fmt.Fprintln(stdout, string(payload))
	return err
}
