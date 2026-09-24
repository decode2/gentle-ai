package shellinstaller

// Channel identifies a Gentle-Shell release channel.
type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelMain   Channel = "main"

	DefaultChannel Channel = ChannelStable
)

func (c Channel) Valid() bool {
	return c == ChannelStable || c == ChannelMain
}
