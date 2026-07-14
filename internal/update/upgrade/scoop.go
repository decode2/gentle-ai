package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var scoopExecCommand = exec.CommandContext

const scoopCleanupTimeout = 5 * time.Second

const gentleAIHomepage = "https://github.com/gentleman-programming/gentle-ai"

var scoopOwnershipDetector = defaultScoopOwnershipDetector

func defaultScoopOwnershipDetector() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	active, err := os.Executable()
	if err != nil {
		return false
	}
	return scoopOwnsExecutable(active, scoopRoots(os.Getenv, os.UserHomeDir), os.ReadFile, filepath.EvalSymlinks)
}

func scoopRoots(getenv func(string) string, userHome func() (string, error)) []string {
	userRoot := strings.TrimSpace(getenv("SCOOP"))
	if userRoot == "" {
		home := strings.TrimSpace(getenv("USERPROFILE"))
		if home == "" {
			home, _ = userHome()
		}
		if home != "" {
			userRoot = joinWindows(home, "scoop")
		}
	}
	roots := make([]string, 0, 1)
	seen := map[string]bool{}
	for _, root := range []string{userRoot} {
		canonical, _, ok := canonicalWindowsPath(root)
		if ok && !seen[canonical] {
			seen[canonical] = true
			roots = append(roots, root)
		}
	}
	return roots
}

func scoopOwnsExecutable(active string, roots []string, readFile func(string) ([]byte, error), evalSymlinks func(string) (string, error)) bool {
	resolvedActive, err := evalSymlinks(active)
	if err != nil {
		return false
	}
	activeCanonical, _, ok := canonicalWindowsPath(resolvedActive)
	if !ok || !strings.HasSuffix(activeCanonical, `\gentle-ai.exe`) {
		return false
	}

	for _, root := range roots {
		appBase := joinWindows(root, "apps", "gentle-ai")
		current := joinWindows(appBase, "current")
		resolvedBase, baseErr := evalSymlinks(appBase)
		resolvedCurrent, currentErr := evalSymlinks(current)
		if baseErr != nil || currentErr != nil || !windowsPathWithin(resolvedCurrent, resolvedBase) || !windowsPathWithin(resolvedActive, resolvedCurrent) {
			continue
		}
		data, readErr := readFile(joinWindows(resolvedCurrent, "manifest.json"))
		if readErr != nil || !officialScoopManifest(data) {
			continue
		}
		return true
	}
	return false
}

func officialScoopManifest(data []byte) bool {
	var manifest struct {
		Version  string `json:"version"`
		Homepage string `json:"homepage"`
	}
	if json.Unmarshal(data, &manifest) != nil || strings.TrimSpace(manifest.Version) == "" {
		return false
	}
	homepage := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(manifest.Homepage)), "/")
	return homepage == gentleAIHomepage
}

func joinWindows(parts ...string) string {
	if len(parts) == 0 {
		return ""
	}
	joined := strings.TrimRight(strings.TrimSpace(parts[0]), `\/`)
	for _, part := range parts[1:] {
		part = strings.Trim(strings.TrimSpace(part), `\/`)
		if part != "" {
			joined += `\` + part
		}
	}
	return joined
}

func canonicalWindowsPath(raw string) (string, string, bool) {
	if strings.ContainsRune(raw, 0) || strings.Contains(raw, `"`) {
		return "", "", false
	}
	value := strings.ReplaceAll(strings.TrimSpace(raw), "/", `\`)
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, `\\?\unc\`) {
		value = `\\` + value[len(`\\?\UNC\`):]
	} else if strings.HasPrefix(lower, `\\?\`) {
		value = value[len(`\\?\`):]
	} else if strings.HasPrefix(lower, `\\.\`) {
		return "", "", false
	}

	var volume string
	var rest string
	if len(value) >= 3 && value[1] == ':' && value[2] == '\\' && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) {
		volume, rest = strings.ToLower(value[:2]), value[3:]
	} else if strings.HasPrefix(value, `\\`) {
		items := strings.FieldsFunc(value[2:], func(r rune) bool { return r == '\\' })
		if len(items) < 2 || items[0] == "." || items[1] == "." {
			return "", "", false
		}
		volume = `\\` + strings.ToLower(items[0]) + `\` + strings.ToLower(items[1])
		rest = strings.Join(items[2:], `\`)
	} else {
		return "", "", false
	}

	segments := make([]string, 0)
	for _, segment := range strings.Split(rest, `\`) {
		switch segment {
		case "", ".":
		case "..":
			if len(segments) == 0 {
				return "", "", false
			}
			segments = segments[:len(segments)-1]
		default:
			segments = append(segments, strings.ToLower(segment))
		}
	}
	canonical := volume + `\`
	if len(segments) > 0 {
		canonical += strings.Join(segments, `\`)
	}
	return strings.TrimSuffix(canonical, `\`), volume, true
}

func windowsPathWithin(candidate, root string) bool {
	candidatePath, candidateVolume, candidateOK := canonicalWindowsPath(candidate)
	rootPath, rootVolume, rootOK := canonicalWindowsPath(root)
	return candidateOK && rootOK && candidateVolume == rootVolume && (candidatePath == rootPath || strings.HasPrefix(candidatePath, rootPath+`\`))
}

func scoopCommand(ctx context.Context, args ...string) ([]byte, error) {
	cmd := scoopExecCommand(ctx, "scoop", args...)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return out, fmt.Errorf("scoop %s: %w (command error: %v, output: %s)", strings.Join(args, " "), ctxErr, err, strings.TrimSpace(string(out)))
		}
		return out, fmt.Errorf("scoop %s: %w (output: %s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func scoopUpgrade(ctx context.Context) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	priorOut, err := scoopCommand(ctx, "config", "IGNORE_RUNNING_PROCESSES")
	if err != nil {
		return err
	}
	prior := strings.ToLower(strings.TrimSpace(string(priorOut)))
	restore := []string{"config", "rm", "IGNORE_RUNNING_PROCESSES"}
	if prior == "true" || prior == "false" {
		restore = []string{"config", "IGNORE_RUNNING_PROCESSES", prior}
	} else if !strings.Contains(prior, "is not set") {
		return fmt.Errorf("read Scoop IGNORE_RUNNING_PROCESSES: unexpected output %q", strings.TrimSpace(string(priorOut)))
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), scoopCleanupTimeout)
		defer cancel()
		if _, restoreErr := scoopCommand(cleanupCtx, restore...); restoreErr != nil {
			err = errors.Join(err, fmt.Errorf("restore Scoop setting: %w", restoreErr))
		}
	}()
	if _, err = scoopCommand(ctx, "config", "IGNORE_RUNNING_PROCESSES", "true"); err != nil {
		return err
	}
	out, err := scoopCommand(ctx, "update", "gentle-ai")
	if err != nil {
		return err
	}
	if strings.Contains(strings.ToLower(string(out)), "running process detected, skip updating") {
		return fmt.Errorf("scoop update gentle-ai: running process prevented update (output: %s)", strings.TrimSpace(string(out)))
	}
	return nil
}
