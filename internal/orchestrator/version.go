package orchestrator

import (
	"net/http"
)

// Version is the daemon build identity; the update banner compares it with
// the latest published release tag.
const Version = "0.1.0-alpha.1"

// daemonVersion reports the running daemon's build.
func (d *DaemonHTTP) daemonVersion(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": Version})
}
