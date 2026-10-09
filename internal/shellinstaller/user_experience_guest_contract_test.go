//go:build shell_experience_windows_guest

package shellinstaller

import "testing"

func TestExperienceGuestInvocation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		worker bool
		args   []string
		want   bool
	}{
		{"supervisor", false, nil, true},
		{"fixed worker", true, experienceGuestArguments(), true},
		{"supervisor selector", false, []string{"-test.run=.*"}, false},
		{"empty worker", true, nil, false},
		{"wrong selection", true, []string{"-test.run=.*", "-test.v", "-test.count=1", "-test.timeout=45s"}, false},
		{"missing timeout", true, []string{"-test.run=" + experienceGuestPattern, "-test.v", "-test.count=1"}, false},
		{"extra flag", true, append(experienceGuestArguments(), "-test.short"), false},
		{"list only", true, []string{"-test.list=.*"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := experienceGuestInvocation(tc.args, tc.worker); got != tc.want {
				t.Fatalf("invocation accepted=%v, want=%v", got, tc.want)
			}
		})
	}
	args := experienceGuestArguments()
	args[0] = "-test.run=.*"
	if !experienceGuestInvocation(experienceGuestArguments(), true) {
		t.Fatal("argument slices alias between invocations")
	}
}
