//go:build linux

package directrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"golang.org/x/sys/unix"
)

const retainedGitTimeout = 10 * time.Second

type retainedGitInspector struct {
	lease   *reviewtransaction.RepositoryIdentityLease
	program retainedProgram
	cwd     int
}

type gitChange struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"`
	Status    string `json:"status"`
	Additions int64  `json:"additions,omitempty"`
	Deletions int64  `json:"deletions,omitempty"`
	Binary    bool   `json:"binary,omitempty"`
}

type gitStatus struct {
	Head      string      `json:"head"`
	Upstream  string      `json:"upstream,omitempty"`
	Ahead     int64       `json:"ahead,omitempty"`
	Behind    int64       `json:"behind,omitempty"`
	Changes   []gitChange `json:"changes"`
	Untracked []string    `json:"untracked"`
}

type gitInspection struct {
	Status   gitStatus   `json:"status"`
	Staged   []gitChange `json:"staged"`
	Unstaged []gitChange `json:"unstaged"`
	Evidence Digest      `json:"evidence_sha256"`
}

func newRetainedGitInspector(lease *reviewtransaction.RepositoryIdentityLease) (*retainedGitInspector, error) {
	if lease == nil {
		return nil, ErrOperationUnavailable
	}
	program, err := retainedProgramFor("git")
	if err != nil {
		return nil, ErrCommandTargetUnsupported
	}
	cwd, err := unix.Open(lease.Identity().RepositoryRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		retainedCloseProgram(program)
		return nil, ErrOperationUnavailable
	}
	return &retainedGitInspector{lease: lease, program: program, cwd: cwd}, nil
}

func (g *retainedGitInspector) Close() {
	if g == nil {
		return
	}
	retainedCloseProgram(g.program)
	_ = unix.Close(g.cwd)
}

// inspect is intentionally private until the operation contract is wired.
func (g *retainedGitInspector) inspect(ctx context.Context) (gitInspection, error) {
	if g == nil || g.lease.Validate(ctx) != nil {
		return gitInspection{}, ErrOperationUnavailable
	}
	statusRaw, err := g.run(ctx, []string{"git", "status", "--porcelain=v2", "-z", "--branch"})
	if err != nil {
		return gitInspection{}, err
	}
	stagedNames, err := g.run(ctx, []string{"git", "diff", "--cached", "--name-status", "-z", "--find-renames", "--no-ext-diff", "--no-textconv"})
	if err != nil {
		return gitInspection{}, err
	}
	stagedStats, err := g.run(ctx, []string{"git", "diff", "--cached", "--numstat", "-z", "--find-renames", "--no-ext-diff", "--no-textconv"})
	if err != nil {
		return gitInspection{}, err
	}
	unstagedNames, err := g.run(ctx, []string{"git", "diff", "--name-status", "-z", "--find-renames", "--no-ext-diff", "--no-textconv"})
	if err != nil {
		return gitInspection{}, err
	}
	unstagedStats, err := g.run(ctx, []string{"git", "diff", "--numstat", "-z", "--find-renames", "--no-ext-diff", "--no-textconv"})
	if err != nil {
		return gitInspection{}, err
	}
	status, err := parseGitStatus(statusRaw)
	if err != nil {
		return gitInspection{}, err
	}
	staged, err := parseGitDiff(stagedNames, stagedStats)
	if err != nil {
		return gitInspection{}, err
	}
	unstaged, err := parseGitDiff(unstagedNames, unstagedStats)
	if err != nil {
		return gitInspection{}, err
	}
	result := gitInspection{Status: status, Staged: staged, Unstaged: unstaged}
	payload, err := json.Marshal(struct {
		Status           gitStatus `json:"status"`
		Staged, Unstaged []gitChange
	}{status, staged, unstaged})
	if err != nil {
		return gitInspection{}, ErrOperationUnavailable
	}
	result.Evidence = Digest(DigestSHA256(payload))
	return result, nil
}

func (g *retainedGitInspector) run(ctx context.Context, argv []string) ([]byte, error) {
	if err := g.lease.Validate(ctx); err != nil {
		return nil, ErrOperationUnavailable
	}
	capture, err := runRetainedProgram(ctx, defaultRetainedExecTestHook, g.program, g.cwd, argv, retainedGitTimeout)
	if err != nil || capture.exitCode != 0 || len(capture.stderr) != 0 {
		return nil, ErrOperationUnavailable
	}
	if err := g.lease.Validate(ctx); err != nil {
		return nil, ErrOperationUnavailable
	}
	return capture.stdout, nil
}

func parseGitStatus(raw []byte) (gitStatus, error) {
	var result gitStatus
	if len(raw) == 0 {
		return result, errors.New("missing git status records")
	}
	records, err := gitNULRecords(raw)
	if err != nil {
		return result, err
	}
	for i := 0; i < len(records); i++ {
		r := string(records[i])
		switch {
		case strings.HasPrefix(r, "# branch.oid "):
			// The object identity is intentionally not exposed as inspection output.
		case strings.HasPrefix(r, "# branch.head "):
			result.Head = strings.TrimPrefix(r, "# branch.head ")
		case strings.HasPrefix(r, "# branch.upstream "):
			result.Upstream = strings.TrimPrefix(r, "# branch.upstream ")
		case strings.HasPrefix(r, "# branch.ab "):
			parts := strings.Fields(strings.TrimPrefix(r, "# branch.ab "))
			if len(parts) != 2 || !strings.HasPrefix(parts[0], "+") || !strings.HasPrefix(parts[1], "-") {
				return result, errors.New("invalid git branch counts")
			}
			result.Ahead, err = gitNumber(parts[0][1:])
			if err != nil {
				return result, err
			}
			result.Behind, err = gitNumber(parts[1][1:])
			if err != nil {
				return result, err
			}
		case strings.HasPrefix(r, "# stash "):
			if _, err := gitNumber(strings.TrimPrefix(r, "# stash ")); err != nil {
				return result, err
			}
		case strings.HasPrefix(r, "1 "):
			change, err := parseGitStatusTracked(r, 8)
			if err != nil {
				return result, err
			}
			result.Changes = append(result.Changes, change)
		case strings.HasPrefix(r, "2 "):
			if i+1 >= len(records) {
				return result, errors.New("missing rename source")
			}
			change, err := parseGitStatusTracked(r, 9)
			if err != nil {
				return result, err
			}
			change.OldPath = string(records[i+1])
			if err := gitPath(change.OldPath); err != nil {
				return result, err
			}
			result.Changes = append(result.Changes, change)
			i++
		case strings.HasPrefix(r, "u "):
			change, err := parseGitStatusTracked(r, 10)
			if err != nil {
				return result, err
			}
			result.Changes = append(result.Changes, change)
		case strings.HasPrefix(r, "? "):
			path := strings.TrimPrefix(r, "? ")
			if err := gitPath(path); err != nil {
				return result, err
			}
			result.Untracked = append(result.Untracked, path)
		case strings.HasPrefix(r, "! "):
			return result, errors.New("ignored status record")
		default:
			return result, errors.New("unknown git status record")
		}
	}
	if result.Head == "" || !utf8.ValidString(result.Head) || !utf8.ValidString(result.Upstream) {
		return result, errors.New("missing git branch head")
	}
	return result, nil
}

func parseGitStatusTracked(record string, fields int) (gitChange, error) {
	parts := strings.SplitN(record, " ", fields+1)
	if len(parts) != fields+1 || len(parts[1]) != 2 {
		return gitChange{}, errors.New("invalid tracked status record")
	}
	path := parts[fields]
	if err := gitPath(path); err != nil {
		return gitChange{}, err
	}
	status := parts[1]
	if status == "??" || status == "!!" || !strings.ContainsRune(".MADRCUT", rune(status[0])) || !strings.ContainsRune(".MADRCUT", rune(status[1])) {
		return gitChange{}, errors.New("invalid tracked status")
	}
	return gitChange{Path: path, Status: status}, nil
}

func parseGitDiff(names, stats []byte) ([]gitChange, error) {
	changes, err := parseGitNameStatus(names)
	if err != nil {
		return nil, err
	}
	statChanges, err := parseGitNumstat(stats)
	if err != nil {
		return nil, err
	}
	if len(changes) != len(statChanges) {
		return nil, errors.New("git diff records mismatch")
	}
	for i := range changes {
		if changes[i].Path != statChanges[i].Path || changes[i].OldPath != statChanges[i].OldPath {
			return nil, errors.New("git diff path mismatch")
		}
		changes[i].Additions, changes[i].Deletions, changes[i].Binary = statChanges[i].Additions, statChanges[i].Deletions, statChanges[i].Binary
	}
	return changes, nil
}

func parseGitNameStatus(raw []byte) ([]gitChange, error) {
	records, err := gitNULRecords(raw)
	if err != nil {
		return nil, err
	}
	var out []gitChange
	for i := 0; i < len(records); i++ {
		status := string(records[i])
		if status == "" || i+1 >= len(records) {
			return nil, errors.New("invalid name-status record")
		}
		i++
		path := string(records[i])
		if err := gitPath(path); err != nil {
			return nil, err
		}
		change := gitChange{Status: status, Path: path}
		if status[0] == 'R' || status[0] == 'C' {
			if len(status) < 2 || len(status) > 4 {
				return nil, errors.New("invalid rename status")
			}
			score, scoreErr := gitNumber(status[1:])
			if scoreErr != nil || score > 100 {
				return nil, errors.New("invalid rename status")
			}
			if i+1 >= len(records) {
				return nil, errors.New("missing rename destination")
			}
			change.OldPath, change.Path = path, string(records[i+1])
			if err := gitPath(change.Path); err != nil {
				return nil, err
			}
			i++
		} else if len(status) != 1 || !strings.ContainsRune("AMDTUXB", rune(status[0])) {
			return nil, errors.New("unknown name-status")
		}
		out = append(out, change)
	}
	return out, nil
}

func parseGitNumstat(raw []byte) ([]gitChange, error) {
	records, err := gitNULRecords(raw)
	if err != nil {
		return nil, err
	}
	var out []gitChange
	for i := 0; i < len(records); i++ {
		parts := strings.SplitN(string(records[i]), "\t", 3)
		if len(parts) != 3 {
			return nil, errors.New("invalid numstat record")
		}
		change := gitChange{Path: parts[2]}
		if change.Path == "" {
			if i+2 >= len(records) {
				return nil, errors.New("missing numstat rename paths")
			}
			change.OldPath, change.Path = string(records[i+1]), string(records[i+2])
			if err := gitPath(change.OldPath); err != nil {
				return nil, err
			}
			if err := gitPath(change.Path); err != nil {
				return nil, err
			}
			i += 2
		} else if err := gitPath(change.Path); err != nil {
			return nil, err
		}
		if parts[0] == "-" && parts[1] == "-" {
			change.Binary = true
		} else {
			change.Additions, err = gitNumber(parts[0])
			if err != nil {
				return nil, err
			}
			change.Deletions, err = gitNumber(parts[1])
			if err != nil {
				return nil, err
			}
		}
		out = append(out, change)
	}
	return out, nil
}

func gitNULRecords(raw []byte) ([][]byte, error) {
	if len(raw) > retainedOutputLimit {
		return nil, errors.New("invalid NUL records")
	}
	if len(raw) == 0 {
		return nil, nil
	}
	if raw[len(raw)-1] != 0 {
		return nil, errors.New("invalid NUL records")
	}
	records := strings.Split(string(raw[:len(raw)-1]), "\x00")
	for _, r := range records {
		if !utf8.ValidString(r) {
			return nil, errors.New("invalid UTF-8")
		}
	}
	out := make([][]byte, len(records))
	for i := range records {
		out[i] = []byte(records[i])
	}
	return out, nil
}
func gitPath(path string) error {
	if path == "" || !utf8.ValidString(path) || strings.ContainsRune(path, 0) {
		return errors.New("invalid git path")
	}
	return nil
}
func gitNumber(value string) (int64, error) {
	if value == "" || strings.HasPrefix(value, "+") || strings.HasPrefix(value, "-") {
		return 0, errors.New("invalid git number")
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid git number")
	}
	return n, nil
}
