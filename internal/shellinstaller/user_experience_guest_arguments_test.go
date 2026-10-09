//go:build shell_experience_windows_guest

package shellinstaller

import "slices"

const experienceGuestPattern = "^TestUserWindowsStageExperience(CanonicalFiles|PreservesPreimages|RefusesBeforeWrites)$"

// The guest entry is not an arbitrary test runner. No caller flag can widen
// the selection, omit execution, shorten coverage or remove its timeout.
func experienceGuestArguments() []string {
	return []string{"-test.run=" + experienceGuestPattern, "-test.v", "-test.count=1", "-test.timeout=45s"}
}

func experienceGuestInvocation(args []string, worker bool) bool {
	if !worker {
		return len(args) == 0
	}
	return slices.Equal(args, experienceGuestArguments())
}
