//go:build !linux && !darwin

package testpack

// Fail before filesystem access where the descriptor contract is not qualified.
func snapshot(string) (map[string][]byte, error) { return nil, ErrUnsupportedPlatform }
