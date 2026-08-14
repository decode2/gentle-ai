//go:build linux || darwin

package directrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gentleman-programming/gentle-ai/v2/internal/reviewtransaction"
	"golang.org/x/sys/unix"
)

type operationFileIdentity struct {
	dev, ino uint64
	size     int64
	mtime    uint64
}

type linuxOperationFiles struct {
	mu      sync.Mutex
	lease   *reviewtransaction.RepositoryIdentityLease
	root    int
	rootID  operationFileIdentity
	allowed map[string]operationFileIdentity
	closed  bool
	ops     unixOperationFileOps
}

// unixOperationFileOps is copied into each authority before it is used. Tests
// may replace one boundary on that authority without affecting another one.
type unixOperationFileOps struct {
	openAt   func(int, string, int, uint32) (int, error)
	fstat    func(int, *unix.Stat_t) error
	fstatAt  func(int, string, *unix.Stat_t, int) error
	read     func(int, []byte) (int, error)
	write    func(int, []byte) (int, error)
	dup      func(int) (int, error)
	temp     func(int, uint32, uint32, uint32) (string, int, error)
	unlinkAt func(int, string, int) error
	seek     func(int, int64, int) (int64, error)
	readDir  func(int, []byte) (int, error)
	flock    func(int, int) error
	fchown   func(int, int, int) error
	fchmod   func(int, uint32) error
	fsync    func(int) error
	rename   func(int, string, int, string) error
	close    func(int) error
	postRead func() error
}

func newUnixOperationFileOps() unixOperationFileOps {
	return unixOperationFileOps{openAt: unix.Openat, fstat: unix.Fstat, fstatAt: unix.Fstatat, read: unix.Read, write: unix.Write, dup: unix.Dup, temp: operationTemp, unlinkAt: unix.Unlinkat, seek: unix.Seek, readDir: unix.ReadDirent, flock: unix.Flock, fchown: unix.Fchown, fchmod: unix.Fchmod, fsync: unix.Fsync, rename: unix.Renameat, close: unix.Close, postRead: func() error { return nil }}
}

func newPlatformOperationFiles(ctx context.Context, lease *reviewtransaction.RepositoryIdentityLease, handoff Handoff) (operationFiles, error) {
	if lease == nil || handoff.Validate() != nil || lease.Validate(ctx) != nil {
		return nil, ErrOperationUnavailable
	}
	root, err := unix.Open(lease.Identity().RepositoryRoot, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrOperationUnavailable
	}
	f := &linuxOperationFiles{lease: lease, root: root, allowed: make(map[string]operationFileIdentity), ops: newUnixOperationFileOps()}
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
	if f == nil {
		return ReadResult{}, ErrOperationUnavailable
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.valid(ctx); err != nil {
		return ReadResult{}, err
	}
	if path([]byte(`"`+logical+`"`)) != nil || offset < 0 || limit < 1 || limit > maxContent {
		return ReadResult{}, ErrOperationInvalidPath
	}
	fd, err := f.openFile(logical)
	if err != nil {
		return ReadResult{}, err
	}
	defer f.ops.close(fd)
	before, err := f.operationStat(fd)
	if err != nil || before.size > maxOperationFileBytes {
		return ReadResult{}, ErrOperationLimit
	}
	contents, err := f.readExact(fd, before.size)
	if err != nil {
		return ReadResult{}, ErrOperationUnavailable
	}
	if f.ops.postRead() != nil {
		return ReadResult{}, ErrOperationUnavailable
	}
	after, err := f.operationStat(fd)
	parent, name, parentErr := f.parent(logical)
	if parentErr != nil {
		return ReadResult{}, ErrOperationUnavailable
	}
	named, namedErr := f.statAt(parent, name)
	_ = f.ops.close(parent)
	if err != nil || namedErr != nil || !sameOperationIdentity(before, after) || !sameOperationIdentity(before, named) || f.valid(ctx) != nil {
		return ReadResult{}, ErrOperationUnavailable
	}
	end := offset + limit
	if end < offset || end > int64(len(contents)) {
		end = int64(len(contents))
	}
	if offset > int64(len(contents)) {
		offset = int64(len(contents))
	}
	result, resultErr := NewReadResult(contents, contents[offset:end], offset, int64(len(contents)), offset != 0 || end != int64(len(contents)))
	if resultErr != nil {
		return ReadResult{}, ErrOperationUnavailable
	}
	return result, nil
}

func (f *linuxOperationFiles) Edit(ctx context.Context, logical, base string, replacements []Replacement) (out EditResult, err error) {
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
	published := false
	defer func() {
		if f.ops.close(parent) != nil && err == nil {
			out = EditResult{}
			if published {
				err = ErrOperationPublication
			} else {
				err = ErrOperationUnavailable
			}
		}
	}()
	fd, err := f.ops.openAt(parent, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return EditResult{}, ErrOperationNotFound
	}
	if err != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	sourceClosed := false
	defer func() {
		if !sourceClosed && f.ops.close(fd) != nil && err == nil {
			out = EditResult{}
			if published {
				err = ErrOperationPublication
			} else {
				err = ErrOperationUnavailable
			}
		}
	}()
	details, err := f.operationStatFull(fd)
	before := details.operationFileIdentity
	if err != nil || before.size > maxOperationFileBytes {
		return EditResult{}, ErrOperationLimit
	}
	if details.uid != uint32(unix.Geteuid()) || details.mode&0o7000 != 0 {
		return EditResult{}, ErrOperationUnavailable
	}
	lockFD, lockErr := f.ops.dup(fd)
	if lockErr != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	defer func() {
		if f.ops.close(lockFD) != nil && err == nil {
			out = EditResult{}
			if published {
				err = ErrOperationPublication
			} else {
				err = ErrOperationUnavailable
			}
		}
	}()
	if err := lockTarget(ctx, lockFD, f.ops.flock); err != nil {
		return EditResult{}, err
	}
	defer func() {
		if f.ops.flock(lockFD, unix.LOCK_UN) != nil && err == nil {
			out = EditResult{}
			if published {
				err = ErrOperationPublication
			} else {
				err = ErrOperationUnavailable
			}
		}
	}()
	old, err := f.readExact(fd, before.size)
	if err != nil || DigestSHA256(old) != base {
		return EditResult{}, ErrOperationConflict
	}
	final, err := applyReplacements(old, replacements)
	if err != nil || len(final) > maxOperationFileBytes {
		return EditResult{}, ErrOperationLimit
	}
	if string(final) == string(old) {
		result, resultErr := NewEditResult(old, false, "unchanged")
		if resultErr != nil {
			return EditResult{}, ErrOperationUnavailable
		}
		return result, nil
	}
	tmp, temp, err := f.ops.temp(parent, details.mode&0o777, details.uid, details.gid)
	if err != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	defer func() {
		if !published {
			_ = f.ops.unlinkAt(parent, tmp, 0)
		}
	}()
	ops := f.ops
	if ops.fchown(temp, int(details.uid), int(details.gid)) != nil || ops.fchmod(temp, details.mode&0o777) != nil || f.writeExact(temp, final) != nil || ops.fsync(temp) != nil {
		_ = ops.close(temp)
		return EditResult{}, ErrOperationUnavailable
	}
	if ops.close(temp) != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	named, err := f.statAt(parent, name)
	if err != nil || !sameOperationIdentity(before, named) || f.valid(ctx) != nil {
		return EditResult{}, ErrOperationConflict
	}
	currentFD, err := f.openFile(logical)
	if err != nil {
		return EditResult{}, ErrOperationConflict
	}
	currentBefore, currentStatErr := f.operationStat(currentFD)
	current, currentReadErr := f.readExact(currentFD, currentBefore.size)
	currentAfter, currentAfterErr := f.operationStat(currentFD)
	currentCloseErr := ops.close(currentFD)
	if currentStatErr != nil || currentReadErr != nil || currentAfterErr != nil || currentCloseErr != nil || !sameOperationIdentity(before, currentBefore) || !sameOperationIdentity(currentBefore, currentAfter) || DigestSHA256(current) != base {
		return EditResult{}, ErrOperationConflict
	}
	sourceClosed = true
	if f.ops.close(fd) != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	// A noncooperative external rename can still race in the interval before renameat.
	if ops.rename(parent, tmp, parent, name) != nil {
		return EditResult{}, ErrOperationUnavailable
	}
	published = true
	if ops.fsync(parent) != nil {
		return EditResult{}, ErrOperationPublication
	}
	verifyFD, err := f.openFile(logical)
	if err != nil {
		return EditResult{}, ErrOperationPublication
	}
	verifyBefore, statErr := f.operationStat(verifyFD)
	verifyBytes, readErr := f.readExact(verifyFD, verifyBefore.size)
	verifyAfter, afterErr := f.operationStat(verifyFD)
	closeErr := ops.close(verifyFD)
	if statErr != nil || readErr != nil || afterErr != nil || closeErr != nil || !sameOperationIdentity(verifyBefore, verifyAfter) || DigestSHA256(verifyBytes) != DigestSHA256(final) || f.valid(ctx) != nil {
		return EditResult{}, ErrOperationPublication
	}
	result, resultErr := NewEditResult(final, true, "published")
	if resultErr != nil {
		return EditResult{}, ErrOperationPublication
	}
	return result, nil
}

func (f *linuxOperationFiles) Tree(ctx context.Context, logical string) (InspectResult, error) {
	if f == nil {
		return InspectResult{}, ErrOperationUnavailable
	}
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
		defer f.ops.close(fd)
	}
	lines := []string{}
	evidenceBytes := 0
	if err := f.tree(ctx, fd, "/", 0, &lines, &evidenceBytes); err != nil {
		return InspectResult{}, err
	}
	evidence := []byte(strings.Join(lines, "\n"))
	if len(evidence) > maxContent {
		return InspectResult{}, ErrOperationLimit
	}
	if f.valid(ctx) != nil {
		return InspectResult{}, ErrOperationUnavailable
	}
	result, resultErr := NewInspectResult(evidence)
	if resultErr != nil {
		return InspectResult{}, ErrOperationUnavailable
	}
	return result, nil
}

// Tree evidence uses one unambiguous line per entry: "f /path", "d /path", or "l /path".
// Symlinks are listed but never traversed; all other special entries are refused.
func (f *linuxOperationFiles) tree(ctx context.Context, fd int, prefix string, depth int, lines *[]string, evidenceBytes *int) error {
	if depth > maxTreeDepth || len(*lines) > maxTreeEntries {
		return ErrOperationLimit
	}
	before, err := f.operationStatFull(fd)
	if err != nil || before.mode&unix.S_IFMT != unix.S_IFDIR {
		return ErrOperationUnavailable
	}
	entries, err := f.directoryNames(fd)
	if err != nil {
		return ErrOperationUnavailable
	}
	sort.Strings(entries)
	for _, name := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(name) == 0 || len(name) > maxTreeNameBytes || !utf8.ValidString(name) || strings.ContainsAny(name, "\n\r") {
			return ErrOperationUnavailable
		}
		st, err := f.statAtFull(fd, name)
		if err != nil {
			return ErrOperationConflict
		}
		child := strings.TrimSuffix(prefix, "/") + "/" + name
		if len(child) > maxTreePathBytes {
			return ErrOperationLimit
		}
		switch st.mode & unix.S_IFMT {
		case unix.S_IFREG:
			if err := appendTreeLine(lines, evidenceBytes, "f", child); err != nil {
				return err
			}
		case unix.S_IFLNK:
			if err := appendTreeLine(lines, evidenceBytes, "l", child); err != nil {
				return err
			}
		case unix.S_IFDIR:
			if err := appendTreeLine(lines, evidenceBytes, "d", child); err != nil {
				return err
			}
			next, err := f.ops.openAt(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return ErrOperationConflict
			}
			nextStat, statErr := f.operationStat(next)
			if statErr != nil || !sameOperationIdentity(st.operationFileIdentity, nextStat) {
				_ = f.ops.close(next)
				return ErrOperationConflict
			}
			err = f.tree(ctx, next, child, depth+1, lines, evidenceBytes)
			closeErr := f.ops.close(next)
			if err != nil || closeErr != nil {
				if err == nil {
					return ErrOperationUnavailable
				}
				return err
			}
		default:
			return ErrOperationUnavailable
		}
		if len(*lines) > maxTreeEntries {
			return ErrOperationLimit
		}
	}
	after, err := f.operationStat(fd)
	if err != nil {
		return ErrOperationUnavailable
	}
	if !sameOperationIdentity(before.operationFileIdentity, after) {
		return ErrOperationConflict
	}
	return nil
}

const (
	maxTreeNameBytes = 255
	maxTreePathBytes = 1024
)

func appendTreeLine(lines *[]string, evidenceBytes *int, kind, path string) error {
	lineBytes := len(kind) + 1 + len(path)
	if len(*lines) >= maxTreeEntries || *evidenceBytes+lineBytes+map[bool]int{true: 1}[len(*lines) > 0] > maxContent {
		return ErrOperationLimit
	}
	*evidenceBytes += lineBytes + map[bool]int{true: 1}[len(*lines) > 0]
	*lines = append(*lines, kind+" "+path)
	return nil
}

func (f *linuxOperationFiles) directoryNames(fd int) ([]string, error) {
	if _, err := f.ops.seek(fd, 0, 0); err != nil {
		return nil, err
	}
	var names []string
	buffer := make([]byte, 8192)
	for {
		n, err := f.ops.readDir(fd, buffer)
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
	mode     uint32
	uid, gid uint32
}

func operationStat(fd int) (operationFileIdentity, error) {
	st, err := operationStatFull(fd)
	return st.operationFileIdentity, err
}
func (f *linuxOperationFiles) operationStat(fd int) (operationFileIdentity, error) {
	st, err := f.operationStatFull(fd)
	return st.operationFileIdentity, err
}
func (f *linuxOperationFiles) operationStatFull(fd int) (operationStatResult, error) {
	var st unix.Stat_t
	if f.ops.fstat(fd, &st) != nil {
		return operationStatResult{}, ErrOperationUnavailable
	}
	return operationStatResult{operationFileIdentity: operationFileIdentity{uint64(st.Dev), st.Ino, st.Size, operationMtime(&st)}, mode: uint32(st.Mode), uid: st.Uid, gid: st.Gid}, nil
}
func operationStatFull(fd int) (operationStatResult, error) {
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil {
		return operationStatResult{}, ErrOperationUnavailable
	}
	return operationStatResult{operationFileIdentity: operationFileIdentity{uint64(st.Dev), st.Ino, st.Size, operationMtime(&st)}, mode: uint32(st.Mode), uid: st.Uid, gid: st.Gid}, nil
}
func (f *linuxOperationFiles) statAt(parent int, name string) (operationFileIdentity, error) {
	st, err := f.statAtFull(parent, name)
	return st.operationFileIdentity, err
}
func (f *linuxOperationFiles) statAtFull(parent int, name string) (operationStatResult, error) {
	var st unix.Stat_t
	if f.ops.fstatAt(parent, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
		return operationStatResult{}, ErrOperationUnavailable
	}
	return operationStatResult{operationFileIdentity: operationFileIdentity{uint64(st.Dev), st.Ino, st.Size, operationMtime(&st)}, mode: uint32(st.Mode), uid: st.Uid, gid: st.Gid}, nil
}
func isDirectory(fd int) bool {
	st, err := operationStatFull(fd)
	return err == nil && st.mode&unix.S_IFMT == unix.S_IFDIR
}
func sameOperationIdentity(a, b operationFileIdentity) bool {
	return a.dev == b.dev && a.ino == b.ino && a.size == b.size && a.mtime == b.mtime
}
func sameAuthorityIdentity(a, b operationFileIdentity) bool { return a.dev == b.dev && a.ino == b.ino }

func (f *linuxOperationFiles) walk(logical string, directory bool) (int, error) {
	current, err := unix.Dup(f.root)
	if err != nil {
		return -1, ErrOperationUnavailable
	}
	if logical == "" {
		return current, nil
	}
	for _, segment := range strings.Split(logical, "/") {
		next, openErr := f.ops.openAt(current, segment, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|map[bool]int{true: unix.O_DIRECTORY}[directory], 0)
		_ = f.ops.close(current)
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
	fd, openErr := f.ops.openAt(parent, name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	_ = f.ops.close(parent)
	if errors.Is(openErr, unix.ENOENT) {
		return -1, ErrOperationNotFound
	}
	if openErr != nil {
		return -1, ErrOperationUnavailable
	}
	st, statErr := f.operationStatFull(fd)
	if statErr != nil || st.mode&unix.S_IFMT != unix.S_IFREG {
		_ = f.ops.close(fd)
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
func (f *linuxOperationFiles) readExact(fd int, size int64) ([]byte, error) {
	value := make([]byte, size)
	for n := 0; n < len(value); {
		k, err := f.ops.read(fd, value[n:])
		if err != nil || k == 0 {
			return nil, ErrOperationUnavailable
		}
		n += k
	}
	var extra [1]byte
	n, err := f.ops.read(fd, extra[:])
	if err != nil || n != 0 {
		return nil, ErrOperationUnavailable
	}
	return value, nil
}
func (f *linuxOperationFiles) writeExact(fd int, value []byte) error {
	for len(value) > 0 {
		n, err := f.ops.write(fd, value)
		if err != nil || n == 0 {
			return ErrOperationUnavailable
		}
		value = value[n:]
	}
	return nil
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
func operationTemp(parent int, mode, uid, gid uint32) (string, int, error) {
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
func lockTarget(ctx context.Context, fd int, flock func(int, int) error) error {
	for {
		if err := flock(fd, unix.LOCK_EX|unix.LOCK_NB); err == nil {
			return nil
		} else if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return ErrOperationUnavailable
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
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
