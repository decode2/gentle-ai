package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/gentleman-programming/gentle-ai/v2/internal/agents"
	"github.com/gentleman-programming/gentle-ai/v2/internal/components/sdd"
	"github.com/gentleman-programming/gentle-ai/v2/internal/model"
	"github.com/gentleman-programming/gentle-ai/v2/internal/state"
	"github.com/gentleman-programming/gentle-ai/v2/internal/system"
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

func TestDryRunReportsBackgroundIntentWithoutWritingState(t *testing.T) {
	flags, err := ParseInstallFlags([]string{"--opencode-background-subagents=off"})
	if err != nil || !flags.OpenCodeBackgroundSubagentsSet || flags.OpenCodeBackgroundSubagents != "off" {
		t.Fatalf("explicit background flag = %#v, err = %v", flags, err)
	}
	var help strings.Builder
	PrintInstallHelp(&help)
	if !strings.Contains(help.String(), "--opencode-background-subagents=auto|on|off") || !strings.Contains(help.String(), OpenCodeBackgroundSubagentsEnv) {
		t.Fatalf("install help omits background contract: %s", help.String())
	}
	t.Setenv(OpenCodeBackgroundSubagentsEnv, "on")
	resolved, err := resolveOpenCodeBackgroundCLI(false, "", state.InstallState{BackgroundIntent: model.OpenCodeBackgroundOff})
	if err != nil || resolved.Effective != model.OpenCodeBackgroundOn || resolved.Persist != model.OpenCodeBackgroundOn {
		t.Fatalf("environment resolution = %#v, err = %v", resolved, err)
	}
	home := t.TempDir()
	original := osUserHomeDir
	osUserHomeDir = func() (string, error) { return home, nil }
	t.Cleanup(func() { osUserHomeDir = original })
	result, err := RunInstall([]string{"--dry-run", "--agent", "opencode", "--opencode-background-subagents=on"}, system.DetectionResult{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Background.Intent != model.OpenCodeBackgroundOn || result.Background.Effective != model.OpenCodeBackgroundOn {
		t.Fatalf("background resolution = %#v", result.Background)
	}
	if _, err := os.Stat(state.Path(home)); !os.IsNotExist(err) {
		t.Fatalf("dry-run state error = %v, want no state file", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "opencode")); !os.IsNotExist(err) {
		t.Fatalf("dry-run asset directory error = %v, want no assets", err)
	}
	report := RenderDryRun(result)
	if !strings.Contains(report, "policy effective: on") || !strings.Contains(report, "runtime ready: false") || !strings.Contains(report, "activation: pending") || !strings.Contains(report, "runtime remains foreground") {
		t.Fatalf("untruthful dry-run report: %s", report)
	}
}

func TestBackgroundPolicyForwardingIsOpenCodeOnly(t *testing.T) {
	original := injectSDD
	t.Cleanup(func() { injectSDD = original })
	for _, tt := range []struct {
		agent model.AgentID
		want  bool
	}{{model.AgentOpenCode, true}, {model.AgentKilocode, false}} {
		var got bool
		injectSDD = func(_ string, adapter agents.Adapter, _ model.SDDModeID, options ...sdd.InjectOptions) (sdd.InjectionResult, error) {
			if adapter.Agent() != tt.agent {
				t.Fatalf("adapter = %q, want %q", adapter.Agent(), tt.agent)
			}
			if len(options) > 0 {
				got = options[0].IncludeOpenCodeBackgroundPolicy
			}
			return sdd.InjectionResult{}, nil
		}
		if err := (componentApplyStep{component: model.ComponentSDD, homeDir: t.TempDir(), workspaceDir: t.TempDir(), scope: ScopeGlobal, agents: []model.AgentID{tt.agent}, selection: model.Selection{SDDMode: model.SDDModeSingle}, backgroundPolicy: true}).Run(); err != nil {
			t.Fatal(err)
		}
		if got != tt.want {
			t.Fatalf("policy = %t, want %t", got, tt.want)
		}
	}
}

func installTestHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	oldHome, oldRun, oldLookPath := osUserHomeDir, runCommand, cmdLookPath
	osUserHomeDir = func() (string, error) { return home, nil }
	runCommand = func(string, ...string) error { return nil }
	cmdLookPath = missingBinaryLookPath
	t.Cleanup(func() {
		osUserHomeDir, runCommand, cmdLookPath = oldHome, oldRun, oldLookPath
	})
	return home
}

func TestInstallPublishesIntentTransactionally(t *testing.T) {
	for _, tt := range []struct {
		name, intent, wantErr string
		injectErr             error
		dropAssets            bool
		writeErr              error
		wantAsset             bool
	}{
		{name: "success on", intent: "on", wantAsset: true},
		{name: "success off", intent: "off", wantAsset: true},
		{name: "pipeline failure", intent: "on", injectErr: errors.New("pipeline injection failed"), wantErr: "execute install pipeline"},
		{name: "post verification failure", intent: "on", dropAssets: true, wantErr: "post-apply verification failed", wantAsset: true},
		{name: "state write failure", intent: "on", writeErr: errors.New("state disk full"), wantErr: "persist install state", wantAsset: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := installTestHome(t)
			oldInject, oldWrite := injectSDD, writeInstallState
			injectSDD = func(path string, adapter agents.Adapter, mode model.SDDModeID, options ...sdd.InjectOptions) (sdd.InjectionResult, error) {
				if tt.injectErr != nil {
					return sdd.InjectionResult{}, tt.injectErr
				}
				if tt.dropAssets {
					return sdd.InjectionResult{}, nil
				}
				return sdd.Inject(path, adapter, mode, options...)
			}
			writeInstallState = func(path string, value state.InstallState) error {
				if tt.writeErr != nil {
					return tt.writeErr
				}
				return oldWrite(path, value)
			}
			t.Cleanup(func() { injectSDD, writeInstallState = oldInject, oldWrite })

			result, err := RunInstall([]string{"--agent", "opencode", "--component", "sdd", "--opencode-background-subagents=" + tt.intent}, system.DetectionResult{})
			if (err != nil) != (tt.wantErr != "") || (err != nil && !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("RunInstall() error = %v, want %q", err, tt.wantErr)
			}
			if tt.wantErr == "" && (!result.Verify.Ready || result.BackgroundPolicyEnabled || (tt.intent == "on" && !strings.Contains(result.Verify.FinalNote, "activation remains pending"))) {
				t.Fatalf("success report = %#v, want pending foreground activation", result)
			}
			got, readErr := state.Read(home)
			if tt.wantErr == "" {
				if readErr != nil || got.BackgroundIntent != model.OpenCodeBackgroundIntent(tt.intent) {
					t.Fatalf("published intent = %q, read error = %v", got.BackgroundIntent, readErr)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatalf("state after %s = %v, want absent", tt.name, readErr)
			}
			if _, statErr := os.Stat(filepath.Join(home, ".config", "opencode", "opencode.json")); (statErr == nil) != tt.wantAsset {
				t.Fatalf("asset after %s: err = %v, want present = %t", tt.name, statErr, tt.wantAsset)
			}
		})
	}
}

func TestPersistInstallStateFullInstallPreservesUnrelatedFields(t *testing.T) {
	home := t.TempDir()
	lastCheck := time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)
	recorded := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	existing := state.InstallState{InstalledAgents: []string{"claude-code"}, ManagedAssetDigest: "old", LastUpdateCheck: &lastCheck, PendingSync: true, RDDMode: "off", RDDModeRecordedAt: &recorded, BackgroundIntent: model.OpenCodeBackgroundOff}
	if err := state.Write(home, existing); err != nil {
		t.Fatal(err)
	}
	fresh := state.InstallState{InstalledAgents: []string{"opencode"}, SelectionConfigured: true, Components: []model.ComponentID{model.ComponentPermission}, Persona: "gentleman", BackgroundIntent: model.OpenCodeBackgroundOn}
	if err := persistInstallState(home, fresh, []string{"opencode"}, InstallFlags{}, "new"); err != nil {
		t.Fatal(err)
	}
	got, err := state.Read(home)
	if err != nil {
		t.Fatal(err)
	}
	want := fresh
	want.ManagedAssetDigest = "new"
	want.LastUpdateCheck, want.PendingSync = existing.LastUpdateCheck, existing.PendingSync
	want.RDDMode, want.RDDModeRecordedAt = existing.RDDMode, existing.RDDModeRecordedAt
	want.PersonaPresent = true
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("full install state = %#v, want %#v", got, want)
	}
}

func TestInstallBackgroundInvalidSourcesFailBeforeMutation(t *testing.T) {
	for _, tt := range []struct {
		name  string
		args  []string
		env   string
		prior bool
	}{
		{name: "invalid CLI", args: []string{"--opencode-background-subagents=maybe"}},
		{name: "invalid environment", env: "maybe"},
		{name: "invalid prior state", prior: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			oldHome, oldInject := osUserHomeDir, injectSDD
			osUserHomeDir = func() (string, error) { return home, nil }
			injectCalls := 0
			injectSDD = func(string, agents.Adapter, model.SDDModeID, ...sdd.InjectOptions) (sdd.InjectionResult, error) {
				injectCalls++
				return sdd.InjectionResult{}, nil
			}
			t.Cleanup(func() { osUserHomeDir, injectSDD = oldHome, oldInject })
			t.Setenv(OpenCodeBackgroundSubagentsEnv, tt.env)
			var before []byte
			if tt.prior {
				if err := state.Write(home, state.InstallState{BackgroundIntent: model.OpenCodeBackgroundIntent("maybe")}); err != nil {
					t.Fatal(err)
				}
				before, _ = os.ReadFile(state.Path(home))
			}
			args := append([]string{"--agent", "opencode", "--component", "sdd"}, tt.args...)
			if _, err := RunInstall(args, system.DetectionResult{}); err == nil {
				t.Fatal("RunInstall() error = nil, want preflight rejection")
			}
			if injectCalls != 0 {
				t.Fatalf("inject calls = %d, want preflight before pipeline", injectCalls)
			}
			if _, statErr := os.Stat(filepath.Join(home, ".config", "opencode")); !os.IsNotExist(statErr) {
				t.Fatalf("asset mutation = %v", statErr)
			}
			if tt.prior {
				after, _ := os.ReadFile(state.Path(home))
				if !reflect.DeepEqual(after, before) {
					t.Fatalf("prior state mutated: before %s after %s", before, after)
				}
			}
		})
	}
}
