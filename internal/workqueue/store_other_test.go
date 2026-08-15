//go:build !unix

package workqueue

import (
	"errors"
	"testing"
)

func TestOpenStoreFailsClosedOutsideUnix(t *testing.T) {
	if _, err := OpenStore("C:\\repo\\.git"); !errors.Is(err, ErrUnsupportedPlatform) {
		t.Fatalf("OpenStore() error = %v, want %v", err, ErrUnsupportedPlatform)
	}
}
