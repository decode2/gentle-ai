package reviewtransaction

import (
	"strings"
	"unicode/utf8"
)

// secureWindowsChildName accepts one safe Windows namespace component.
func secureWindowsChildName(name string) bool {
	if name == "" || !utf8.ValidString(name) || name == "." || name == ".." ||
		strings.ContainsAny(name, "\\/:\x00") || strings.HasSuffix(name, " ") || strings.HasSuffix(name, ".") {
		return false
	}
	base := strings.ToUpper(strings.Split(name, ".")[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CLOCK$" {
		return false
	}
	return !(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9')
}
