package directrun

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"unicode/utf8"
)

// ReadResult is the complete digest plus the requested byte range. Offsets are bytes.
type ReadResult struct {
	DataSHA256 string `json:"data_sha256"`
	ContentB64 string `json:"content_b64"`
	Offset     int64  `json:"offset"`
	TotalSize  int64  `json:"total_size"`
	Truncated  bool   `json:"truncated"`
}

type EditResult struct {
	ResultSHA256 string `json:"result_sha256"`
	Changed      bool   `json:"changed"`
	Publication  string `json:"publication"`
}

type InspectResult struct {
	EvidenceSHA256 string `json:"evidence_sha256"`
	ContentB64     string `json:"content_b64"`
	Encoding       string `json:"encoding"`
	Truncated      bool   `json:"truncated"`
}

func NewReadResult(full, content []byte, offset, total int64, truncated bool) (ReadResult, error) {
	if offset < 0 || total < 0 || total != int64(len(full)) || offset > total || int64(len(content)) > total-offset {
		return ReadResult{}, errors.New("invalid read result metadata")
	}
	end := offset + int64(len(content))
	if !bytes.Equal(content, full[offset:end]) || truncated != (offset != 0 || int64(len(content)) != total) {
		return ReadResult{}, errors.New("invalid read result content")
	}
	return ReadResult{DataSHA256: DigestSHA256(full), ContentB64: base64.StdEncoding.EncodeToString(content), Offset: offset, TotalSize: total, Truncated: truncated}, nil
}

func NewEditResult(final []byte, changed bool, publication string) (EditResult, error) {
	if publication != "published" && publication != "unchanged" || changed != (publication == "published") {
		return EditResult{}, errors.New("invalid edit result publication")
	}
	return EditResult{ResultSHA256: DigestSHA256(final), Changed: changed, Publication: publication}, nil
}

func NewInspectResult(evidence []byte) (InspectResult, error) {
	if len(evidence) > maxContent || !utf8.Valid(evidence) {
		return InspectResult{}, errors.New("invalid inspect result evidence")
	}
	return InspectResult{EvidenceSHA256: DigestSHA256(evidence), ContentB64: base64.StdEncoding.EncodeToString(evidence), Encoding: "utf-8"}, nil
}

func (r ReadResult) CanonicalJSON() ([]byte, error) {
	return marshal(r, func() error { b, _ := json.Marshal(r); return result("direct_read", b) })
}
func (r EditResult) CanonicalJSON() ([]byte, error) {
	return marshal(r, func() error { b, _ := json.Marshal(r); return result("direct_edit", b) })
}
func (r InspectResult) CanonicalJSON() ([]byte, error) {
	return marshal(r, func() error { b, _ := json.Marshal(r); return result("direct_inspect", b) })
}
