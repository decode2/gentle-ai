//go:build linux

package directrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"golang.org/x/sys/unix"
)

type operationFileIdentity struct {
	dev, ino uint64
	size     int64
	mtime    unix.Timespec
}

type linuxOperationFiles struct {
	mu      sync.Mutex
	lease   *reviewtransaction.RepositoryIdentityLease
	root    int
	rootID  operationFileIdentity
	allowed map[string]operationFileIdentity
	closed  bool
}

func newPlatformOperationFiles(ctx context.Context, lease *reviewtransaction.RepositoryIdentityLease, handoff Handoff) (operationFiles, error) {
	if lease == nil || handoff.Validate() != nil || lease.Validate(ctx) != nil {
		return nil, ErrOperationUnavailable
	}
	root, err := unix.Open(lease.Identity().RepositoryRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrOperationUnavailable
	}
	f := &linuxOperationFiles{lease: lease, root: root, allowed: make(map[string]operationFileIdentity)}
	if f.rootID, err = operationStat(root); err != nil || !isDirectory(root) {
		_ = unix.Close(root)
		return nil, ErrOperationUnavailable
	}
	for _, configured := range handoff.AllowedEditRoots {
		logical, ok := logicalEditRoot(lease.Identity().RepositoryRoot, configured)
		if !ok {
			_ = unix.Close(root)
			return nil, ErrOperationUnavailable
		}
		fd, err := f.walk(logical, true)
		if err != nil {
			_ = unix.Close(root)
			return nil, ErrOperationUnavailable
		}
		identity, statErr := operationStat(fd)
		_ = unix.Close(fd)
		if statErr != nil {
			_ = unix.Close(root)
			return nil, ErrOperationUnavailable
		}
		f.allowed[logical] = identity
	}
	return f, nil
}

func (f *linuxOperationFiles) Close() error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil
	}
	f.closed = true
	if unix.Close(f.root) != nil {
		return ErrOperationUnavailable
	}
	return nil
}

func (f *linuxOperationFiles) valid(ctx context.Context) error {
	if f == nil || f.closed || f.lease == nil || f.lease.Validate(ctx) != nil {
		return ErrOperationUnavailable
	}
	current, err := operationStat(f.root)
	if err != nil || !sameAuthorityIdentity(f.rootID, current) || !isDirectory(f.root) {
		return ErrOperationUnavailable
	}
	return nil
}

func (f *linuxOperationFiles) Read(ctx context.Context, logical string, offset, limit int64) (ReadResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.valid(ctx); err != nil {
		return ReadResult{}, err
	}
	if path([]byte(`"`+logical+`"`)) != nil || offset < 0 || limit < 1 || limit > maxOperationFileBytes {
		return ReadResult{}, ErrOperationInvalidPath
	}
	fd, err := f.openFile(logical)
	if err != nil {
		return ReadResult{}, err
	}
	defer unix.Close(fd)
	before, err := operationStat(fd)
	if err != nil || before.size > maxOperationFileBytes {
		return ReadResult{}, ErrOperationLimit
	}
	contents, err := readExact(fd, before.size)
	if err != nil {
		return ReadResult{}, ErrOperationUnavailable
	}
	after, err := operationStat(fd)
	if err != nil || !sameOperationIdentity(before, after) || f.valid(ctx) != nil {
		return ReadResult{}, ErrOperationUnavailable
	}
	end := offset + limit
	if end < offset || end > int64(len(contents)) {
		end = int64(len(contents))
	}
	if offset > int64(len(contents)) {
		offset = int64(len(contents))
	}
	return NewReadResult(contents, contents[offset:end], offset, int64(len(contents)), end < int64(len(contents))), nil
}

func (f *linuxOperationFiles) Edit(ctx context.Context, logical, base string, replacements []Replacement) (EditResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.valid(ctx); err != nil {
		return EditResult{}, err
	}
	if path([]byte(`"`+logical+`"`)) != nil || !f.editAllowed(logical) {
		return EditResult{}, ErrOperationInvalidPath
	}
	parent, name, err := f.parent(logical)
	if err != nil {
		return EditResult{}, err
	}
	defer unix.Close(parent)
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return EditResult{}, ErrOperationNotFound
	}
	if err != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	defer unix.Close(fd)
	before, err := operationStat(fd)
	if err != nil || before.size > maxOperationFileBytes {
		return EditResult{}, ErrOperationLimit
	}
	old, err := readExact(fd, before.size)
	if err != nil || DigestSHA256(old) != base {
		return EditResult{}, ErrOperationConflict
	}
	final, err := applyReplacements(old, replacements)
	if err != nil || len(final) > maxOperationFileBytes {
		return EditResult{}, ErrOperationLimit
	}
	if string(final) == string(old) {
		return NewEditResult(old, false), nil
	}
	tmp, temp, err := operationTemp(parent, uint32(beforeMode(fd)))
	if err != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	published := false
	defer func() {
		if !published {
			_ = unix.Unlinkat(parent, tmp, 0)
		}
	}()
	if writeExact(temp, final) != nil || unix.Fsync(temp) != nil {
		_ = unix.Close(temp)
		return EditResult{}, ErrOperationUnavailable
	}
	_ = unix.Close(temp)
	named, err := f.statAt(parent, name)
	if err != nil || !sameOperationIdentity(before, named) || DigestSHA256(old) != base || f.valid(ctx) != nil {
		return EditResult{}, ErrOperationConflict
	}
	if unix.Renameat(parent, tmp, parent, name) != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	published = true
	if unix.Fsync(parent) != nil {
		return EditResult{}, ErrOperationPublication
	}
	verifyFD, err := f.openFile(logical)
	if err != nil {
		return EditResult{}, ErrOperationPublication
	}
	verifyBefore, statErr := operationStat(verifyFD)
	verifyBytes, readErr := readExact(verifyFD, verifyBefore.size)
	verifyAfter, afterErr := operationStat(verifyFD)
	_ = unix.Close(verifyFD)
	if statErr != nil || readErr != nil || afterErr != nil || !sameOperationIdentity(verifyBefore, verifyAfter) || DigestSHA256(verifyBytes) != DigestSHA256(final) || f.valid(ctx) != nil {
		return EditResult{}, ErrOperationPublication
	}
	return NewEditResult(final, true), nil
}

func (f *linuxOperationFiles) Tree(ctx context.Context, logical string) (InspectResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.valid(ctx); err != nil {
		return InspectResult{}, err
	}
	if logical != "" && path([]byte(`"`+logical+`"`)) != nil {
		return InspectResult{}, ErrOperationInvalidPath
	}
	fd := f.root
	if logical != "" {
		var err error
		fd, err = f.walk(logical, true)
		if err != nil {
			return InspectResult{}, err
		}
		defer unix.Close(fd)
	}
	lines := []string{}
	if err := f.tree(ctx, fd, "/", 0, &lines); err != nil {
		return InspectResult{}, err
	}
	evidence := []byte(strings.Join(lines, "\n"))
	if len(evidence) > maxContent || f.valid(ctx) != nil {
		return InspectResult{}, ErrOperationLimit
	}
	return NewInspectResult(evidence), nil
}

func (f *linuxOperationFiles) tree(ctx context.Context, fd int, prefix string, depth int, lines *[]string) error {
	if depth > maxTreeDepth || len(*lines) > maxTreeEntries {
		return ErrOperationLimit
	}
	entries, err := directoryNames(fd)
	if err != nil {
		return ErrOperationUnavailable
	}
	sort.Strings(entries)
	for _, name := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		st, err := f.statAtFull(fd, name)
		if err != nil {
			return ErrOperationUnavailable
		}
		child := strings.TrimSuffix(prefix, "/") + "/" + name
		switch st.mode & unix.S_IFMT {
		case unix.S_IFREG:
			*lines = append(*lines, "f "+child)
		case unix.S_IFLNK:
			*lines = append(*lines, "l "+child)
		case unix.S_IFDIR:
			*lines = append(*lines, "d "+child)
			next, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return ErrOperationUnavailable
			}
			err = f.tree(ctx, next, child, depth+1, lines)
			_ = unix.Close(next)
			if err != nil {
				return err
			}
		default:
			return ErrOperationUnavailable
		}
		if len(*lines) > maxTreeEntries {
			return ErrOperationLimit
		}
	}
	return nil
}

func directoryNames(fd int) ([]string, error) {
	if _, err := unix.Seek(fd, 0, 0); err != nil {
		return nil, err
	}
	var names []string
	buffer := make([]byte, 8192)
	for {
		n, err := unix.ReadDirent(fd, buffer)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return names, nil
		}
		_, _, names = unix.ParseDirent(buffer[:n], -1, names)
	}
}

type operationStatResult struct {
	operationFileIdentity
	mode uint32
}

func operationStat(fd int) (operationFileIdentity, error) {
	st, err := operationStatFull(fd)
	return st.operationFileIdentity, err
}
func operationStatFull(fd int) (operationStatResult, error) {
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil {
		return operationStatResult{}, ErrOperationUnavailable
	}
	return operationStatResult{operationFileIdentity: operationFileIdentity{uint64(st.Dev), st.Ino, st.Size, st.Mtim}, mode: st.Mode}, nil
}
func (f *linuxOperationFiles) statAt(parent int, name string) (operationFileIdentity, error) {
	st, err := f.statAtFull(parent, name)
	return st.operationFileIdentity, err
}
func (f *linuxOperationFiles) statAtFull(parent int, name string) (operationStatResult, error) {
	var st unix.Stat_t
	if unix.Fstatat(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return operationStatResult{}, ErrOperationUnavailable
	}
	return operationStatResult{operationFileIdentity: operationFileIdentity{uint64(st.Dev), st.Ino, st.Size, st.Mtim}, mode: st.Mode}, nil
}
func isDirectory(fd int) bool {
	st, err := operationStatFull(fd)
	return err == nil && st.mode&unix.S_IFMT == unix.S_IFDIR
}
func sameOperationIdentity(a, b operationFileIdentity) bool {
	return a.dev == b.dev && a.ino == b.ino && a.size == b.size && a.mtime == b.mtime
}
func sameAuthorityIdentity(a, b operationFileIdentity) bool { return a.dev == b.dev && a.ino == b.ino }
func beforeMode(fd int) uint32 {
	st, err := operationStatFull(fd)
	if err != nil {
		return 0o600
	}
	return st.mode & 0o777
}

func (f *linuxOperationFiles) walk(logical string, directory bool) (int, error) {
	current, err := unix.Dup(f.root)
	if err != nil {
		return -1, ErrOperationUnavailable
	}
	if logical == "" {
		return current, nil
	}
	for _, segment := range strings.Split(logical, "/") {
		next, openErr := unix.Openat(current, segment, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|map[bool]int{true: unix.O_DIRECTORY}[directory], 0)
		_ = unix.Close(current)
		if openErr != nil {
			return -1, ErrOperationUnavailable
		}
		current = next
	}
	return current, nil
}
func (f *linuxOperationFiles) parent(logical string) (int, string, error) {
	parts := strings.Split(logical, "/")
	if len(parts) == 1 {
		fd, err := unix.Dup(f.root)
		return fd, parts[0], err
	}
	fd, err := f.walk(strings.Join(parts[:len(parts)-1], "/"), true)
	return fd, parts[len(parts)-1], err
}
func (f *linuxOperationFiles) openFile(logical string) (int, error) {
	parent, name, err := f.parent(logical)
	if err != nil {
		return -1, err
	}
	fd, openErr := unix.Openat(parent, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	_ = unix.Close(parent)
	if errors.Is(openErr, unix.ENOENT) {
		return -1, ErrOperationNotFound
	}
	if openErr != nil {
		return -1, ErrOperationUnavailable
	}
	st, statErr := operationStatFull(fd)
	if statErr != nil || st.mode&unix.S_IFMT != unix.S_IFREG {
		_ = unix.Close(fd)
		return -1, ErrOperationUnavailable
	}
	return fd, nil
}
func (f *linuxOperationFiles) editAllowed(logical string) bool {
	for root, want := range f.allowed {
		if root == "" || logical == root || strings.HasPrefix(logical, root+"/") {
			fd, err := f.walk(root, true)
			if err != nil {
				return false
			}
			got, statErr := operationStat(fd)
			_ = unix.Close(fd)
			return statErr == nil && sameAuthorityIdentity(want, got)
		}
	}
	return false
}

func logicalEditRoot(repository, configured string) (string, bool) {
	if configured == repository {
		return "", true
	}
	if !strings.HasPrefix(configured, repository+"/") {
		return "", false
	}
	logical := strings.TrimPrefix(configured, repository+"/")
	return logical, path([]byte(`"`+logical+`"`)) == nil
}
func readExact(fd int, size int64) ([]byte, error) {
	value := make([]byte, size)
	for n := 0; n < len(value); {
		k, err := unix.Read(fd, value[n:])
		if err != nil || k == 0 {
			return nil, ErrOperationUnavailable
		}
		n += k
	}
	var extra [1]byte
	n, err := unix.Read(fd, extra[:])
	if err != nil || n != 0 {
		return nil, ErrOperationUnavailable
	}
	return value, nil
}
func writeExact(fd int, value []byte) error {
	for len(value) > 0 {
		n, err := unix.Write(fd, value)
		if err != nil || n == 0 {
			return ErrOperationUnavailable
		}
		value = value[n:]
	}
	return nil
}
func operationTemp(parent int, mode uint32) (string, int, error) {
	for range 8 {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			break
		}
		name := ".direct-" + hex.EncodeToString(random[:])
		fd, err := unix.Openat(parent, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, mode)
		if errors.Is(err, unix.EEXIST) {
			continue
		}
		if err == nil {
			return name, fd, nil
		}
	}
	return "", -1, ErrOperationUnavailable
}
func applyReplacements(old []byte, values []Replacement) ([]byte, error) {
	var out []byte
	current := int64(0)
	for _, value := range values {
		if value.Start < current || value.End < value.Start || value.End > int64(len(old)) {
			return nil, ErrOperationConflict
		}
		out = append(out, old[current:value.Start]...)
		out = append(out, value.Text...)
		current = value.End
	}
	return append(out, old[current:]...), nil
}
