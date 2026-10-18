//go:build !linux && !darwin

package testpack

func snapshotInstallation(string) (map[string][]byte, error) { return nil, ErrUnsupportedPlatform }
