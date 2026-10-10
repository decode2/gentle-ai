package shellinstaller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type userMainResponse func(*http.Request) (*http.Response, error)

func (f userMainResponse) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type userMainBody struct {
	io.Reader
	close func()
}

func (b userMainBody) Close() error { b.close(); return nil }

// Controlled HTTP response DATA only, not publisher/TLS/authenticated ZIP or
// installed/Main selection. Nothing from these archive bytes is executed.
func TestUserMainSnapshotFreezesOneCanonicalRef(t *testing.T) {
	commit := strings.Repeat("ab", 20)
	archive := "Main archive response DATA"
	for _, name := range []string{"success", "canceled before", "canceled on archive close", "malformed ref", "metadata overflow", "ref status", "archive status", "archive encoding"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if name == "canceled before" {
				cancel()
			}
			calls, closes := 0, 0
			client := &http.Client{Transport: userMainResponse(func(req *http.Request) (*http.Response, error) {
				calls++
				url := "https://api.github.com/repos/Gentleman-Programming/gentle-shell/commits/main"
				data := `{"sha":"` + commit + `"}`
				status, encoding := http.StatusOK, ""
				if calls == 1 {
					if name == "malformed ref" {
						data = `{"sha":"main"}`
					}
					if name == "metadata overflow" {
						data = strings.Repeat(" ", (256<<10)+1)
					}
					if name == "ref status" {
						status = http.StatusForbidden
					}
				} else {
					url, data = "https://codeload.github.com/Gentleman-Programming/gentle-shell/zip/"+commit, archive
					if name == "archive status" {
						status = http.StatusNotFound
					}
					if name == "archive encoding" {
						encoding = "gzip"
					}
				}
				if calls > 2 || req.Method != http.MethodGet || req.URL.String() != url || req.Header.Get("Accept-Encoding") != "identity" || req.Header.Get("Authorization") != "" || req.URL.User != nil {
					t.Fatal("fixed publisher request changed", req)
				}
				body := userMainBody{Reader: strings.NewReader(data), close: func() {
					closes++
					if calls == 2 && name == "canceled on archive close" {
						cancel()
					}
				}}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Encoding": []string{encoding}}, ContentLength: int64(len(data)), Body: body}, nil
			})}
			artifact, data, err := userMainSnapshotUsing(ctx, client)
			if name == "success" {
				want, descriptorErr := userMainDescriptor(commit, fmt.Sprintf("%x", sha256.Sum256([]byte(archive))))
				if descriptorErr != nil || err != nil || artifact != want || string(data) != archive || calls != 2 {
					t.Fatal("Main ref was not frozen exactly once", artifact, err, calls)
				}
			} else if err == nil || artifact != (userSnapshotArtifact{}) || data != nil {
				t.Fatal("failed/canceled producer returned usable snapshot", artifact, err)
			}
			if calls != closes || (name == "canceled before" && calls != 0) {
				t.Fatal("requests did not settle", calls, closes)
			}
		})
	}
}

func TestUserMainDescriptorPreservesSourceOnlyContract(t *testing.T) {
	commit, digest := strings.Repeat("a", 40), strings.Repeat("b", 64)
	artifact, err := userMainDescriptor(commit, digest)
	if err != nil || artifact != (userSnapshotArtifact{URL: "https://codeload.github.com/Gentleman-Programming/gentle-shell/zip/" + commit, SHA: digest, Archive: "main.zip", Prefix: "gentle-shell-" + commit, Bound: 32 << 20}) {
		t.Fatal("descriptor/32MiB bound changed", artifact, err)
	}
	for _, bad := range []string{"main", strings.ToUpper(commit), strings.Repeat("g", 40), ""} {
		if result, err := userMainDescriptor(bad, digest); err == nil || result != (userSnapshotArtifact{}) {
			t.Fatal("unfrozen/invalid Main descriptor", result, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, data, err := userMainSnapshot(ctx); err == nil || result != (userSnapshotArtifact{}) || data != nil {
		t.Fatal("production producer issued canceled requests", result, err)
	}
}
