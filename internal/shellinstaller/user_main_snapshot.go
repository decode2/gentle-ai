package shellinstaller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type userSnapshotArtifact struct {
	URL, SHA, Archive, Prefix string
	Bound, Size               int64
}

// Descriptor construction alone does not authenticate or resolve Main source.
func userMainDescriptor(commit, archiveSHA string) (userSnapshotArtifact, error) {
	if !userSnapshotLowerHex(commit, 40) || !userSnapshotLowerHex(archiveSHA, 64) {
		return userSnapshotArtifact{}, errors.New("invalid frozen Gentle Shell Main commit/archive digest")
	}
	return userSnapshotArtifact{URL: "https://codeload.github.com/Gentleman-Programming/gentle-shell/zip/" + commit, SHA: archiveSHA, Archive: "main.zip", Prefix: "gentle-shell-" + commit, Bound: 32 << 20}, nil
}

func userSnapshotLowerHex(value string, length int) bool {
	_, err := hex.DecodeString(value)
	return len(value) == length && err == nil && value == strings.ToLower(value)
}

// One fixed-publisher ref lookup; no redirects, proxy, credentials or retries.
func userMainSnapshot(ctx context.Context) (userSnapshotArtifact, []byte, error) {
	if ctx == nil || ctx.Err() != nil {
		return userSnapshotArtifact{}, nil, errors.New("Main acquisition canceled before requests")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy, transport.DisableCompression = nil, true
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 90 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("Main publisher redirect refused")
	}}
	return userMainSnapshotUsing(ctx, client)
}

// Client seam keeps the same fixed URLs; offline response DATA do not prove TLS.
func userMainSnapshotUsing(ctx context.Context, client *http.Client) (userSnapshotArtifact, []byte, error) {
	if ctx == nil || ctx.Err() != nil || client == nil {
		return userSnapshotArtifact{}, nil, errors.New("Main acquisition requires a live context and client")
	}
	get := func(url string, bound int64) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept-Encoding", "identity")
		req.Header.Set("User-Agent", "gentle-shell-windows-installer")
		response, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK || response.ContentLength > bound || (response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != "identity") {
			return nil, errors.New("Main publisher status/encoding/bound refused")
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, bound+1))
		if err != nil || int64(len(data)) > bound {
			return nil, errors.Join(err, errors.New("Main publisher response bound"))
		}
		return data, nil
	}
	metadata, err := get("https://api.github.com/repos/Gentleman-Programming/gentle-shell/commits/main", 256<<10)
	var record struct {
		SHA string `json:"sha"`
	}
	if err != nil {
		return userSnapshotArtifact{}, nil, err
	}
	if err := json.Unmarshal(metadata, &record); err != nil || !userSnapshotLowerHex(record.SHA, 40) {
		return userSnapshotArtifact{}, nil, errors.Join(err, errors.New("canonical Main ref identity refused"))
	}
	data, err := get("https://codeload.github.com/Gentleman-Programming/gentle-shell/zip/"+record.SHA, 32<<20)
	if err != nil {
		return userSnapshotArtifact{}, nil, err
	}
	// Body.Close has settled before get returns; cancellation there must not
	// publish a usable snapshot to the confirmed installer.
	if err := ctx.Err(); err != nil {
		return userSnapshotArtifact{}, nil, err
	}
	artifact, err := userMainDescriptor(record.SHA, fmt.Sprintf("%x", sha256.Sum256(data)))
	return artifact, data, err
}
