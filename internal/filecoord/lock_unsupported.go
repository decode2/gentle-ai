//go:build !android && !darwin && !dragonfly && !freebsd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris

package filecoord

import "context"

// The unsupported backend makes no platform capability claim or filesystem change.
func acquireBackend(context.Context, string, string) (*Lease, error) {
	return nil, unsupportedBackend()
}

func unsupportedBackend() error { return &UnsupportedError{} }
