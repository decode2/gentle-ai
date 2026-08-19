package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The public boundary is the installed OpenCode coordinator prompt: `install`
// materializes it into the sandbox's opencode.json. Runtime attempts separately
// prove that an explicit auto choice cannot bypass item authority or the join.
func issue3470Fixture(sandbox *Sandbox) error {
	if err := issue1302Fixture(sandbox); err != nil {
		return err
	}
	binDir := filepath.Join(sandbox.Root, "opencode-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	binary, content := "opencode", "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then printf '1.15.11\\n'; fi\n"
	if runtime.GOOS == "windows" {
		binary, content = "opencode.exe", "@echo off\r\nif \"%1\" == \"--version\" echo 1.15.11\r\n"
	}
	if err := os.WriteFile(filepath.Join(binDir, binary), []byte(content), 0o755); err != nil {
		return err
	}
	sandbox.PathOverride = binDir
	return nil
}

var issue3470InstallCapability = &Capability{
	Verb:  []string{"install"},
	Flags: []string{"--agent", "--component", "--opencode-background-subagents"},
}

func issue3470InstallArgs(*Sandbox) ([]string, error) {
	return []string{"install", "--agent", "opencode", "--component", "sdd", "--opencode-background-subagents=on"}, nil
}

func issue3470AssertMaterializedPolicy(sandbox *Sandbox, observation Observation) error {
	if observation.ExitCode != 0 {
		return fmt.Errorf("public OpenCode install failed: %s", observation.Stderr)
	}
	content, err := os.ReadFile(filepath.Join(sandbox.Home, ".config", "opencode", "opencode.json"))
	if err != nil {
		return fmt.Errorf("read materialized OpenCode settings: %w", err)
	}
	var settings struct {
		Agent map[string]struct {
			Prompt string `json:"prompt"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(content, &settings); err != nil {
		return fmt.Errorf("parse materialized OpenCode settings: %w", err)
	}
	prompt := settings.Agent["gentle-orchestrator"].Prompt
	for _, rule := range []string{
		"Background capability does not select it.",
		"In automatic execution, silently cache `serialized` for the change.",
		"at least two ready items with compatible metadata, satisfied dependencies, pairwise disjoint canonical scopes, and available OpenCode background launch capability.",
		"only at that opportunity ask once",
		"if capability is absent, disabled, or unknown, serialize silently.",
		"Do not add this policy to the mandatory four-group SDD Session Preflight or create a fifth preflight group.",
		"Only explicit `auto` may use OpenCode `background: true`",
		"every item still acquires and settles independently",
		"Missing or incompatible metadata, unavailable capability, overlapping, dependent, malformed, shared, or unknown scopes stay serialized or blocked by existing authority.",
	} {
		if !strings.Contains(prompt, rule) {
			return fmt.Errorf("materialized coordinator policy omitted %q", rule)
		}
	}
	if strings.Count(prompt, "ask once whether to use serialized or automatic scheduling") != 1 {
		return fmt.Errorf("materialized coordinator policy did not cache one interactive choice")
	}
	return nil
}

func issue3470DefaultSerialized(r *journeyRun) error {
	status, err := issue1302StatusRead(r)
	if err != nil {
		return err
	}
	aReady, _ := status.item("a")
	bReady, _ := status.item("b")
	if !aReady || !bReady {
		return fmt.Errorf("fixture lacks two ready disjoint items: %#v", status)
	}

	// The coordinator has no policy set despite available background capability,
	// so it launches and settles only A before considering another actor.
	a, err := issue1302AttemptRun(r, "a", "issue3470-default-a")
	if err != nil || a.State != "proceed" || a.Token == "" {
		return fmt.Errorf("default A acquire = %#v: %v", a, err)
	}
	runtime, err := proveRuntime(r.sandbox)
	if err != nil || len(runtime.Attempts) != 1 {
		return fmt.Errorf("default policy did not retain one actor: %#v: %v", runtime, err)
	}
	if err := issue1302Settle(r, a.Token, "issue3470-default-a-settle", "a"); err != nil {
		return err
	}
	return r.sandbox.write(filepath.Join(sddChangeRoot(r.sandbox), "tasks.md"), issue1302Tasks(map[string]bool{"p": true, "a": true}))
}

func issue3470ExplicitAuto(r *journeyRun) error {
	// Explicit auto may concurrently acquire B and D because their canonical
	// roots are disjoint. C remains blocked until B has settled.
	b, err := issue1302AttemptRun(r, "b", "issue3470-auto-b")
	if err != nil || b.State != "proceed" || b.Token == "" {
		return fmt.Errorf("auto B acquire = %#v: %v", b, err)
	}
	d, err := issue1302AttemptRun(r, "d", "issue3470-auto-d")
	if err != nil || d.State != "proceed" || d.Token == "" || b.Token == d.Token {
		return fmt.Errorf("auto D acquire = %#v: %v", d, err)
	}
	if err := issue1302Refused(r, "c", "issue3470-auto-c-early", 3); err != nil {
		return err
	}
	if err := issue1302Settle(r, b.Token, "issue3470-auto-b-settle", "b"); err != nil {
		return err
	}
	if err := issue1302Settle(r, d.Token, "issue3470-auto-d-settle", "d"); err != nil {
		return err
	}
	if err := r.sandbox.write(filepath.Join(sddChangeRoot(r.sandbox), "tasks.md"), issue1302Tasks(map[string]bool{"p": true, "a": true, "b": true, "d": true})); err != nil {
		return err
	}
	c, err := issue1302AttemptRun(r, "c", "issue3470-auto-c")
	if err != nil || c.State != "proceed" || c.Token == "" {
		return fmt.Errorf("auto C acquire = %#v: %v", c, err)
	}
	if err := issue1302Settle(r, c.Token, "issue3470-auto-c-settle", "c"); err != nil {
		return err
	}
	if err := r.sandbox.write(filepath.Join(sddChangeRoot(r.sandbox), "tasks.md"), issue1302Tasks(map[string]bool{"p": true, "a": true, "b": true, "c": true, "d": true})); err != nil {
		return err
	}
	status, err := issue1302StatusRead(r)
	if err != nil || status.NextRecommended != "verify" || status.Dependencies.Verify != "ready" {
		return fmt.Errorf("joined auto status = %#v: %v", status, err)
	}
	return nil
}

func issue3470Journeys() []Journey {
	return []Journey{{
		ID:     "j112-sdd-parallel-apply-policy",
		Review: reviewUntouched,
		Title:  "Unset SDD item scheduling serializes; explicit auto preserves item settlement and join",
		Source: "https://github.com/Gentleman-Programming/gentle-ai/issues/3470",
		Steps: []Step{
			{Name: "fixture: compatible item metadata and background-capable OpenCode runtime", Fixture: issue3470Fixture},
			{Name: "public install materializes coordinator-owned serialized default", Requires: issue3470InstallCapability, Args: issue3470InstallArgs, After: issue3470AssertMaterializedPolicy},
			{Name: "disable RDD", Requires: modeCapability, Args: productArgs("review", "mode", "disable", "--json")},
			{Name: "unset policy launches one item and settles before projection", Requires: sddAttemptAcquireCapability, Composite: issue3470DefaultSerialized},
			{Name: "explicit auto admits only disjoint items and joins after settlement", Requires: sddAttemptAcquireCapability, Composite: issue3470ExplicitAuto},
		},
	}}
}
