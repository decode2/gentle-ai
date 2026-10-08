//go:build linux || darwin

package shellinstaller

import "strings"

func userBinding(root, name string) string {
	root = strings.ReplaceAll(root, "'", "'\\''")
	binding := "#!/bin/sh\nexec '" + root + "/supervisor' shell launch '" + root + "' \"$@\"\n"
	if name == "gentle-shell" {
		binding = "#!/bin/sh\nif test \"${1-}\" = install; then shift; exec '" + root + "/supervisor' shell install \"$@\"; fi\n" + binding[len("#!/bin/sh\n"):]
	}
	return binding
}
