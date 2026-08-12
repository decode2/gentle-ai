package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
)

func TestOpenCodeBackgroundIntentValidation(t *testing.T) {
	for _, tt := range []struct {
		raw, want, errText string
	}{
		{"auto", "auto", ""}, {"on", "on", ""}, {"off", "off", ""}, {"maybe", "", "valid values"},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			got, err := model.ParseOpenCodeBackgroundIntent(tt.raw)
			if string(got) != tt.want || (tt.errText == "" && err != nil) || (tt.errText != "" && (err == nil || !strings.Contains(err.Error(), tt.errText))) {
				t.Fatalf("parse(%q) = %q, %v", tt.raw, got, err)
			}
		})
	}
	if _, err := ResolveOpenCodeBackground(OpenCodeBackgroundResolveInput{EnvSet: true, EnvValue: "maybe"}); err == nil || !strings.Contains(err.Error(), "auto, on, or off") {
		t.Fatalf("resolver error = %v, want actionable vocabulary", err)
	}
}

func TestResolveOpenCodeBackground(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   OpenCodeBackgroundResolveInput
		want OpenCodeBackgroundResolution
	}{
		{"cli outranks env and prior", OpenCodeBackgroundResolveInput{CLISet: true, CLIValue: model.OpenCodeBackgroundOff, EnvSet: true, EnvValue: "on", PriorManaged: model.OpenCodeBackgroundOn}, OpenCodeBackgroundResolution{Intent: model.OpenCodeBackgroundOff, Effective: model.OpenCodeBackgroundOff, Persist: model.OpenCodeBackgroundOff}},
		{"env outranks prior", OpenCodeBackgroundResolveInput{EnvSet: true, EnvValue: "on", PriorManaged: model.OpenCodeBackgroundOff}, OpenCodeBackgroundResolution{Intent: model.OpenCodeBackgroundOn, Effective: model.OpenCodeBackgroundOn, Persist: model.OpenCodeBackgroundOn}},
		{"prior managed on avoids prompt", OpenCodeBackgroundResolveInput{PriorManaged: model.OpenCodeBackgroundOn, Interactive: true}, OpenCodeBackgroundResolution{Intent: model.OpenCodeBackgroundOn, Effective: model.OpenCodeBackgroundOn}},
		{"prior managed off avoids prompt", OpenCodeBackgroundResolveInput{PriorManaged: model.OpenCodeBackgroundOff, Interactive: true}, OpenCodeBackgroundResolution{Intent: model.OpenCodeBackgroundOff, Effective: model.OpenCodeBackgroundOff}},
		{"unresolved interactive needs prompt", OpenCodeBackgroundResolveInput{Interactive: true}, OpenCodeBackgroundResolution{Intent: model.OpenCodeBackgroundAuto, Effective: model.OpenCodeBackgroundAuto, NeedsPrompt: true}},
		{"unresolved noninteractive stays foreground", OpenCodeBackgroundResolveInput{}, OpenCodeBackgroundResolution{Intent: model.OpenCodeBackgroundAuto, Effective: model.OpenCodeBackgroundOff}},
		{"empty env is ignored", OpenCodeBackgroundResolveInput{EnvSet: true, Interactive: true}, OpenCodeBackgroundResolution{Intent: model.OpenCodeBackgroundAuto, Effective: model.OpenCodeBackgroundAuto, NeedsPrompt: true}},
		{"empty env is ignored noninteractive", OpenCodeBackgroundResolveInput{EnvSet: true}, OpenCodeBackgroundResolution{Intent: model.OpenCodeBackgroundAuto, Effective: model.OpenCodeBackgroundOff}},
		{"explicit auto consults prior policy", OpenCodeBackgroundResolveInput{CLISet: true, CLIValue: model.OpenCodeBackgroundAuto, EnvSet: true, EnvValue: "on", PriorManaged: model.OpenCodeBackgroundOn}, OpenCodeBackgroundResolution{Intent: model.OpenCodeBackgroundAuto, Effective: model.OpenCodeBackgroundOn}},
		{"explicit auto is interactive auto", OpenCodeBackgroundResolveInput{CLISet: true, CLIValue: model.OpenCodeBackgroundAuto, Interactive: true}, OpenCodeBackgroundResolution{Intent: model.OpenCodeBackgroundAuto, Effective: model.OpenCodeBackgroundAuto}},
		{"explicit auto cannot enable without prior", OpenCodeBackgroundResolveInput{CLISet: true, CLIValue: model.OpenCodeBackgroundAuto, EnvSet: true, EnvValue: "on"}, OpenCodeBackgroundResolution{Intent: model.OpenCodeBackgroundAuto, Effective: model.OpenCodeBackgroundOff}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveOpenCodeBackground(tt.in)
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("resolution = %#v, error = %v, want %#v", got, err, tt.want)
			}
		})
	}
}

func TestResolveOpenCodeBackgroundRejectsInvalidValues(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   OpenCodeBackgroundResolveInput
	}{
		{
			name: "invalid explicit CLI value",
			in:   OpenCodeBackgroundResolveInput{CLISet: true, CLIValue: model.OpenCodeBackgroundIntent("maybe")},
		},
		{
			name: "invalid prior managed state",
			in:   OpenCodeBackgroundResolveInput{PriorManaged: model.OpenCodeBackgroundIntent("maybe")},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ResolveOpenCodeBackground(tt.in); err == nil {
				t.Fatal("ResolveOpenCodeBackground() error = nil, want validation error")
			}
		})
	}
}

func TestResolveOpenCodeBackgroundPersistence(t *testing.T) {
	for _, tt := range []struct {
		name    string
		in      OpenCodeBackgroundResolveInput
		persist model.OpenCodeBackgroundIntent
	}{
		{
			name: "explicit auto",
			in:   OpenCodeBackgroundResolveInput{CLISet: true, CLIValue: model.OpenCodeBackgroundAuto},
		},
		{
			name: "default auto",
			in:   OpenCodeBackgroundResolveInput{},
		},
		{
			name: "inherited prior on",
			in:   OpenCodeBackgroundResolveInput{PriorManaged: model.OpenCodeBackgroundOn},
		},
		{
			name: "inherited prior off",
			in:   OpenCodeBackgroundResolveInput{PriorManaged: model.OpenCodeBackgroundOff},
		},
		{
			name:    "explicit on",
			in:      OpenCodeBackgroundResolveInput{CLISet: true, CLIValue: model.OpenCodeBackgroundOn},
			persist: model.OpenCodeBackgroundOn,
		},
		{
			name:    "explicit off",
			in:      OpenCodeBackgroundResolveInput{CLISet: true, CLIValue: model.OpenCodeBackgroundOff},
			persist: model.OpenCodeBackgroundOff,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveOpenCodeBackground(tt.in)
			if err != nil {
				t.Fatalf("ResolveOpenCodeBackground() error = %v", err)
			}
			if got.Persist != tt.persist {
				t.Errorf("Persist = %q, want %q", got.Persist, tt.persist)
			}
		})
	}
}

func TestOpenCodeBackgroundStateIsOptionalAndLossless(t *testing.T) {
	home := t.TempDir()
	want := state.InstallState{
		InstalledAgents: []string{"opencode"}, ManagedAssetDigest: "sha256:asset", SelectionConfigured: true,
		Components: []model.ComponentID{model.ComponentSDD}, CommunityTools: []string{"codegraph"}, CommunityToolsConfigured: true,
		Persona: "neutral", PersonaPresent: true, PendingSync: true, RDDMode: "off", BackgroundIntent: model.OpenCodeBackgroundOn,
	}
	if err := state.Write(home, want); err != nil {
		t.Fatal(err)
	}
	got, err := state.Read(home)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("state round-trip = %#v, error = %v, want %#v", got, err, want)
	}

	legacy := t.TempDir()
	if err := os.MkdirAll(filepath.Join(legacy, ".gentle-ai"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state.Path(legacy), []byte(`{"installed_agents":["opencode"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = state.Read(legacy)
	if err != nil || got.BackgroundIntent != "" || len(got.InstalledAgents) != 1 {
		t.Fatalf("legacy state = %#v, error = %v", got, err)
	}
}

func TestOpenCodeBackgroundStateMergePreservesIntent(t *testing.T) {
	got := state.MergeAgents(state.InstallState{
		InstalledAgents:  []string{"claude-code"},
		BackgroundIntent: model.OpenCodeBackgroundOff,
	}, []string{"opencode"})
	if got.BackgroundIntent != model.OpenCodeBackgroundOff || len(got.InstalledAgents) != 2 {
		t.Fatalf("merged state = %#v", got)
	}
}
