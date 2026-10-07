package shellinstaller

// UserInstallEntryValues preserves the legacy entry when no experience was
// selected. Explicit choices travel as one validated, canonical extra argument.
func UserInstallEntryValues(req UserInstallRequest) ([]string, error) {
	values := []string{req.Destination, req.Mode, req.SharedPrefix, req.SharedAgent, req.Confirmation}
	if req.Experience != nil {
		encoded, err := req.Experience.Encode()
		if err != nil {
			return nil, err
		}
		values = append(values, encoded)
	}
	return values, nil
}
