//go:build !linux && !darwin

package command

import "fmt"

func readInstalledOwnership(string) ([]byte, error) {
	return nil, fmt.Errorf("installed test-pack ownership snapshots require Linux or Darwin")
}
