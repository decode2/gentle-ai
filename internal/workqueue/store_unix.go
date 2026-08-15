//go:build unix

package workqueue

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

var (
	ErrStoreNotInitialized = errors.New("workqueue store not initialized")
	ErrUnsafeStorePath     = errors.New("unsafe workqueue store path")
	ErrUnsupportedPlatform = errors.New("unsupported workqueue store platform")
)

// Store is the read-only boundary for a workqueue state stored under Git's common directory.
type Store struct {
	common, state  string
	commonIdentity fs.FileInfo
	authority      []string
	identities     []fs.FileInfo
}

// OpenStore validates and creates only the store-owned authority directories.
func OpenStore(common string) (*Store, error) {
	common, err := canonicalDirectory(common)
	if err != nil {
		return nil, fmt.Errorf("open workqueue store: %w", ErrUnsafeStorePath)
	}
	commonInfo, err := os.Lstat(common)
	if err != nil || !commonInfo.IsDir() || commonInfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("open workqueue store: %w", ErrUnsafeStorePath)
	}
	parts := []string{"gentle-ai", "workqueue", "v1"}
	s := &Store{common: common, commonIdentity: commonInfo, authority: make([]string, len(parts)), identities: make([]fs.FileInfo, len(parts))}
	current := common
	for i, part := range parts {
		current = filepath.Join(current, part)
		if err := os.Mkdir(current, 0700); err != nil && !os.IsExist(err) {
			return nil, fmt.Errorf("open workqueue store: %w", ErrUnsafeStorePath)
		}
		info, err := safeDirectory(current)
		if err != nil {
			return nil, fmt.Errorf("open workqueue store: %w", err)
		}
		s.authority[i], s.identities[i] = current, info
	}
	s.state = filepath.Join(current, "state.json")
	return s, nil
}

// Load reads and validates state.json; it never initializes or writes state.
func (s *Store) Load(graph GraphSnapshot) (State, error) {
	if err := s.validate(); err != nil {
		return State{}, fmt.Errorf("load workqueue store: %w", err)
	}
	info, err := os.Lstat(s.state)
	if errors.Is(err, os.ErrNotExist) {
		return State{}, fmt.Errorf("load workqueue store: %w", ErrStoreNotInitialized)
	}
	if err != nil || !safeState(info) {
		return State{}, fmt.Errorf("load workqueue store: %w", ErrUnsafeStorePath)
	}
	data, err := readState(s.state, info)
	if err != nil {
		return State{}, fmt.Errorf("read workqueue state: %w", err)
	}
	state, err := Decode(graph, data)
	if err != nil {
		return State{}, fmt.Errorf("read workqueue state: %w", err)
	}
	return state, nil
}

func readState(path string, expected fs.FileInfo) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !os.SameFile(expected, info) || !safeState(info) {
		return nil, ErrUnsafeStorePath
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Store) validate() error {
	current, err := canonicalDirectory(s.common)
	info, statErr := os.Lstat(s.common)
	if err != nil || statErr != nil || current != s.common || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(s.commonIdentity, info) {
		return ErrUnsafeStorePath
	}
	for i, path := range s.authority {
		info, err := safeDirectory(path)
		if err != nil || !os.SameFile(s.identities[i], info) {
			return ErrUnsafeStorePath
		}
	}
	return nil
}

func safeDirectory(path string) (fs.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !ownedBy(info, os.Geteuid()) || info.Mode().Perm()&0022 != 0 {
		return nil, ErrUnsafeStorePath
	}
	return info, nil
}

func safeState(info fs.FileInfo) bool {
	return info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && ownedBy(info, os.Geteuid()) && info.Mode().Perm() == 0600
}

func ownedBy(info fs.FileInfo, uid int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == uid
}
