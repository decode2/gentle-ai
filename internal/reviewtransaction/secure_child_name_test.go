package reviewtransaction

import "testing"

func TestSecureWindowsChildName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"CON.txt", false}, {"prn.log", false}, {"AUX.dat", false},
		{"NUL.tmp", false}, {"COM1.foo", false}, {"LPT9.bar", false},
		{"cOn.multi.part", false}, {"PRN.log.", false}, {"aux.dat ", false},
		{"console.txt", true}, {"com10.foo", true}, {"lpt10.bar", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := secureWindowsChildName(test.name); got != test.want {
				t.Fatalf("secureWindowsChildName(%q) = %t, want %t", test.name, got, test.want)
			}
		})
	}
}
