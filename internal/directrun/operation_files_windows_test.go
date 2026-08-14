//go:build windows

package directrun

import (
	"context"
	"errors"
	"testing"
)

func TestWindowsOperationFilesFailClosedUntilRelativeAuthorityExists(t *testing.T) {
	files, err := newPlatformOperationFiles(context.Background(), nil, Handoff{})
	if files != nil || !errors.Is(err, ErrOperationUnsupported) {
		t.Fatalf("files=%v err=%v", files, err)
	}
}
