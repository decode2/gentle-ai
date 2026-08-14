package directrun

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
)

const (
	maxOperationFileBytes = 1 << 20
	maxTreeEntries        = 4096
	maxTreeDepth          = 32
)

var (
	ErrOperationInvalidPath = errors.New("direct operation invalid path")
	ErrOperationNotFound    = errors.New("direct operation not found")
	ErrOperationConflict    = errors.New("direct operation conflict")
	ErrOperationLimit       = errors.New("direct operation limit exceeded")
	ErrOperationUnavailable = errors.New("direct operation authority unavailable")
	ErrOperationPublication = errors.New("direct operation publication unknown")
	ErrOperationUnsupported = errors.New("direct operation unsupported")
)

// operationFiles is deliberately private: callers retain no raw path or native authority.
type operationFiles interface {
	Read(context.Context, string, int64, int64) (ReadResult, error)
	Edit(context.Context, string, string, []Replacement) (EditResult, error)
	Tree(context.Context, string) (InspectResult, error)
	Close() error
}

type Replacement struct {
	Start int64
	End   int64
	Text  []byte
}

func decodeReadPayload(raw json.RawMessage) (string, int64, int64, error) {
	var value struct {
		Path   string `json:"path"`
		Offset int64  `json:"offset"`
		Limit  int64  `json:"limit"`
	}
	if err := json.Unmarshal(raw, &value); err != nil || path([]byte(`"`+value.Path+`"`)) != nil || value.Offset < 0 || value.Limit < 1 || value.Limit > maxOperationFileBytes {
		return "", 0, 0, ErrOperationInvalidPath
	}
	return value.Path, value.Offset, value.Limit, nil
}

func decodeEditPayload(raw json.RawMessage) (string, string, []Replacement, error) {
	var value struct {
		Path         string `json:"path"`
		BaseSHA256   string `json:"base_sha256"`
		Replacements []struct {
			Start int64  `json:"start"`
			End   int64  `json:"end"`
			Text  string `json:"text"`
		} `json:"replacements"`
	}
	if err := json.Unmarshal(raw, &value); err != nil || path([]byte(`"`+value.Path+`"`)) != nil || sha([]byte(`"`+value.BaseSHA256+`"`)) != nil {
		return "", "", nil, ErrOperationInvalidPath
	}
	result := make([]Replacement, len(value.Replacements))
	last, total := int64(-1), 0
	for i, replacement := range value.Replacements {
		if replacement.Start < 0 || replacement.End < replacement.Start || replacement.Start < last {
			return "", "", nil, ErrOperationConflict
		}
		total += len(replacement.Text)
		if total > maxText {
			return "", "", nil, ErrOperationLimit
		}
		result[i] = Replacement{Start: replacement.Start, End: replacement.End, Text: []byte(replacement.Text)}
		last = replacement.End
	}
	return value.Path, value.BaseSHA256, result, nil
}

func newOperationFiles(ctx context.Context, lease *reviewtransaction.RepositoryIdentityLease, handoff Handoff) (operationFiles, error) {
	return newPlatformOperationFiles(ctx, lease, handoff)
}
