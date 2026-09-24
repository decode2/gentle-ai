package shellinstaller

import "testing"

func TestChannel(t *testing.T) {
	t.Run("supports stable and main", func(t *testing.T) {
		for _, channel := range []Channel{ChannelStable, ChannelMain} {
			if !channel.Valid() {
				t.Errorf("%q should be valid", channel)
			}
		}
	})

	t.Run("rejects unsupported channels", func(t *testing.T) {
		for _, channel := range []Channel{"", "beta", "STABLE"} {
			if channel.Valid() {
				t.Errorf("%q should be invalid", channel)
			}
		}
	})

	t.Run("defaults to stable", func(t *testing.T) {
		if DefaultChannel != ChannelStable {
			t.Fatalf("DefaultChannel = %q, want %q", DefaultChannel, ChannelStable)
		}
	})
}
