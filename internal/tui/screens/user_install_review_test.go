package screens

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/gentleman-programming/gentle-ai/v4/internal/shellinstaller"
)

func reviewPages(t *testing.T, req shellinstaller.UserInstallRequest, preview string, width, height int) string {
	t.Helper()
	last := UserInstallReviewLastOffset(req, preview, width, height)
	var lines []string
	for offset := 0; offset <= last; offset++ {
		view := ansi.Strip(RenderUserInstallReview(req, preview, 0, offset, width, height))
		rows := strings.Split(view, "\n")
		start := 1
		if height >= 6 || width >= 100 && height >= 10 {
			start = 2
		}
		data := rows[start : len(rows)-1]
		if width >= 100 && height >= 10 {
			for i := range data {
				_, data[i], _ = strings.Cut(data[i], "│")
			}
		}
		if offset < last {
			data = data[:1]
		}
		lines = append(lines, data...)
	}
	return strings.Join(lines, "")
}

func reviewDense(text string) string { return strings.Join(strings.Fields(text), "") }

func TestUserInstallReviewData(t *testing.T) {
	req := shellinstaller.UserInstallRequest{Destination: "/target", Mode: "shared", SharedPrefix: "/prefix", SharedAgent: "/agent", Confirmation: strings.Repeat("0123456789abcdef", 4)}
	var preview strings.Builder
	for i := 0; i < 45; i++ {
		fmt.Fprintf(&preview, "settings preimage MARK%02d recovery warning %s\n", i, strings.Repeat("paragraph", 12))
	}
	for _, size := range [][2]int{{120, 50}, {100, 12}, {80, 24}, {40, 10}, {10, 4}, {8, 3}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			got := reviewDense(reviewPages(t, req, preview.String(), size[0], size[1]))
			for _, want := range []string{req.Destination, "Mode: shared", "/target/bin/gentle-shell", "/target/bin/pi", "Shared prefix: /prefix", "Shared agent: /agent", req.Confirmation, "Supplier: 4.0.0", "Native: 4.0.0", "SRI", "age3", "informational", "existing defaults unchanged", "manager", "kernel", "custody", "authentic acquisition", "revalidated after terminal restored", "No installation or preference writes before confirmed backend handoff", "Back: edit destination", "not installed health", preview.String()} {
				if !strings.Contains(got, reviewDense(want)) {
					t.Fatalf("paged data missing %q", want)
				}
			}
			if UserInstallReviewLastOffset(req, preview.String(), size[0], size[1]) == 0 {
				t.Fatal("long shared preview needs paging")
			}
		})
	}
}

func TestUserInstallReviewExperience(t *testing.T) {
	for _, persona := range []string{"gentleman", "neutral", "unmanaged"} {
		for _, background := range []string{"on", "off"} {
			t.Run(persona+"/"+background, func(t *testing.T) {
				profile := shellinstaller.UserExperience{Persona: persona, BackgroundSubagents: background}
				req := shellinstaller.UserInstallRequest{Mode: "separate", Experience: &profile}
				got := reviewDense(reviewPages(t, req, "", 80, 24))
				for _, want := range []string{"Persona: " + persona, "Background subagents: " + background, "New-target-only", "target/config", "not your personal Pi", "project overrides", "may override"} {
					if !strings.Contains(got, reviewDense(want)) {
						t.Fatalf("missing %q", want)
					}
				}
				if persona == "unmanaged" && !strings.Contains(got, "preserve") {
					t.Fatal("unmanaged must preserve")
				}
				if strings.Contains(got, "custom") || strings.Contains(got, "existingdefaultsunchanged") || strings.Contains(got, "Sharedprefix") {
					t.Fatal("invented persona or shared/default scope")
				}
				if *req.Experience != profile {
					t.Fatal("view mutated profile")
				}
			})
		}
	}
}

func TestUserInstallReviewWideSummary(t *testing.T) {
	for _, mode := range []string{"shared", "separate"} {
		for _, profile := range []*shellinstaller.UserExperience{nil, {Persona: "neutral", BackgroundSubagents: "off"}, {Persona: "unmanaged", BackgroundSubagents: "on"}} {
			for _, size := range [][2]int{{100, 12}, {120, 50}} {
				for cursor := 0; cursor < 3; cursor++ {
					req := shellinstaller.UserInstallRequest{Destination: "/live-target", Mode: mode, Experience: profile}
					view := ansi.Strip(RenderUserInstallReview(req, "", cursor, 0, size[0], size[1]))
					rows := strings.Split(view, "\n")
					var left []string
					for _, row := range rows {
						if text, _, found := strings.Cut(row, "│"); found {
							left = append(left, text)
						}
					}
					plan := strings.Join(left, "\n")
					want := []string{"Install Gentle-Shell", "Back", "Cancel", "Target: /live-target", "Mode: " + mode, "Back: edit destination"}
					if profile != nil {
						want = append(want, "Persona: "+profile.Persona)
						if size[1] >= 50 {
							want = append(want, "Background subagents: "+profile.BackgroundSubagents)
						}
					} else if strings.Contains(plan, "Persona:") || strings.Contains(plan, "Background subagents:") {
						t.Fatal("nil profile invented summary choices")
					}
					for _, text := range want {
						if !strings.Contains(plan, text) {
							t.Fatalf("%s %v cursor %d: left summary missing %q", mode, size, cursor, text)
						}
					}
					footer := rows[len(rows)-1]
					if !strings.Contains(footer, "esc: cancel") || strings.Contains(view, "configure") {
						t.Fatal("incorrect review navigation wording")
					}
				}
			}
		}
	}
}

func TestUserInstallReviewViewport(t *testing.T) {
	req := shellinstaller.UserInstallRequest{Destination: "/" + strings.Repeat("界longtarget", 30), Confirmation: strings.Repeat("a", 64)}
	preview := strings.Repeat("\x1b[31mlong preview 界\x1b[0m ", 60)
	for _, size := range [][2]int{{120, 50}, {80, 24}, {40, 10}, {10, 4}, {8, 3}} {
		for cursor, action := range []string{"Install", "Back", "Cancel"} {
			t.Run(fmt.Sprint(size, cursor), func(t *testing.T) {
				last := UserInstallReviewLastOffset(req, preview, size[0], size[1])
				for _, offset := range []int{-1, 0, last, last + 50} {
					view := ansi.Strip(RenderUserInstallReview(req, preview, cursor, offset, size[0], size[1]))
					rows := strings.Split(view, "\n")
					if len(rows) > size[1] {
						t.Fatal("height exceeded")
					}
					for _, row := range rows {
						if ansi.StringWidth(row) > size[0] {
							t.Fatalf("width exceeded: %q", row)
						}
					}
					if !strings.Contains(view, ">"+action) || !strings.Contains(strings.ToLower(view), "esc") {
						t.Fatalf("focus/escape missing: %q", view)
					}
					if size[0] >= 100 && (!strings.Contains(view, "Plan") || !strings.Contains(view, "│About: "+action) || !strings.Contains(view, "pgup/pgdn") || !strings.Contains(view, "Ready to install Gentle-Shell")) {
						t.Fatal("wide review chrome missing")
					}
				}
			})
		}
	}
	got := reviewDense(reviewPages(t, req, preview, 40, 10))
	if !strings.Contains(got, reviewDense(req.Destination)) {
		t.Fatal("long target lost through wrapping")
	}
}
