//go:build !windows

package cmd

import "os"

func restrictToCurrentUser(path string) error {
	return os.Chmod(path, 0o600)
}
