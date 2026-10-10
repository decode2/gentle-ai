package shellinstaller

import "encoding/json"

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
// Additional explicit choices stay refused until their installed chain exists.
func userExperienceSupported(profile *UserExperience) error {
	if profile == nil {
		return nil
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	if profile.GlobalRDD != "" || profile.Context7 != "" || profile.StrictTDD != "" || profile.GGA != "" || profile.CodeGraph != "" {
		return experienceError("selected additional experience preferences require a compatible installed runtime")
	}
	return nil
}
