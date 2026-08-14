//go:build darwin

package directrun

import (
	"os"
	"strings"
	"testing"
)

func TestDarwinRecordBackendVarAliasUsesRetainedAuthority(t *testing.T) {
	repo := t.TempDir()
	if !strings.HasPrefix(repo, "/var/") {
		t.Skipf("temporary directory does not use /var: %q", repo)
	}
	alias := "/private" + repo
	if _, err := os.Stat(alias); err != nil {
		t.Skipf("/private/var alias is unavailable: %v", err)
	}
	if out, err := gitInit(repo); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}

	first, lease := linuxBackend(t, repo)
	store, err := NewStore(first, lease)
	check(t, err)
	handoff := testHandoff(t)
	issued, err := store.Issue(t.Context(), handoff)
	check(t, err)

	second, aliasLease := linuxBackend(t, alias)
	aliasStore, err := NewStore(second, aliasLease)
	check(t, err)
	got, err := aliasStore.Read(t.Context(), handoff.Identity)
	if err != nil || got.Revision != issued.Revision {
		t.Fatalf("alias read = %#v, %v", got, err)
	}
}
