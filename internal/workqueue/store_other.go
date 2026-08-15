//go:build !unix

package workqueue

import "errors"

var (
	ErrStoreNotInitialized = errors.New("workqueue store not initialized")
	ErrUnsafeStorePath     = errors.New("unsafe workqueue store path")
	ErrUnsupportedPlatform = errors.New("unsupported workqueue store platform")
)

// Store is unavailable outside Unix because this boundary requires Unix ownership checks.
type Store struct{}

func OpenStore(string) (*Store, error) { return nil, ErrUnsupportedPlatform }
