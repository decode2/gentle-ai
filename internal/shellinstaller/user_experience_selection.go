package shellinstaller

import (
	"encoding/json"
	"runtime"
)

// The optional named suffix keeps legacy positional fields and Windows channel
// selectors unambiguous. Decoding is inert; inspection still owns admission.
func userEntryExperience(args []string) ([]string, *UserExperience, error) {
	if len(args) < 2 || args[len(args)-2] != "--experience" {
		return args, nil, nil
	}
	profile, err := DecodeUserExperience(args[len(args)-1])
	if err != nil {
		return nil, nil, err
	}
	return args[:len(args)-2], &profile, nil
}

func userExperienceConfirmationData(fields []string, profile *UserExperience) ([]byte, error) {
	if profile != nil {
		encoded, err := profile.Encode()
		if err != nil {
			return nil, err
		}
		fields = append(fields, encoded)
	}
	return json.Marshal(fields)
}

// A transport or valid DTO is not proof that the selected runtime applies it.
// Base persona/background use the existing canonical stage and pinned consumer.
// CodeGraph uses its authenticated Linux amd64 Separate producer/consumer chain.
// Context7 uses the existing canonical agent configuration and built-in MCP.
// GGA uses its authenticated optional CLI and explicit private configuration.
// Other explicit choices stay refused until their compatible chain exists.
func userExperienceSupported(profile *UserExperience) error {
	if profile == nil {
		return nil
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	if profile.SDKSource == "main-preview" {
		if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
			return experienceError("SDK Main + stock provider requires Linux amd64 Separate")
		}
		if profile.CodeGraph != "" || profile.Context7 != "" || profile.GGA != "" {
			return experienceError("Main SDK optional combinations require a compatible installed runtime")
		}
	}
	if profile.CodeGraph != "" && (runtime.GOOS != "linux" || runtime.GOARCH != "amd64") {
		return experienceError("explicit CodeGraph requires Linux amd64 Separate")
	}
	if profile.Context7 != "" && (runtime.GOOS != "linux" || runtime.GOARCH != "amd64") {
		return experienceError("explicit Context7 requires Linux amd64 Separate")
	}
	if profile.GGA != "" && (runtime.GOOS != "linux" || runtime.GOARCH != "amd64") {
		return experienceError("explicit GGA requires Linux amd64 Separate")
	}
	if profile.GlobalRDD != "" || profile.StrictTDD != "" {
		return experienceError("selected additional experience preferences require a compatible installed runtime")
	}
	return nil
}
