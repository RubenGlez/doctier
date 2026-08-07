package cmd

import (
	"errors"
	"os"
)

// writeOwnerOnly writes sensitive plaintext only after the destination has
// been restricted to the current user. Creating an empty placeholder first is
// intentional: platform ACL setup may require an existing filesystem object,
// and an ACL failure must occur before any plaintext reaches disk.
func writeOwnerOnly(path string, data []byte) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		f, createErr := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if createErr != nil {
			return createErr
		}
		if closeErr := f.Close(); closeErr != nil {
			return closeErr
		}
	} else if err != nil {
		return err
	}
	if err := restrictToCurrentUser(path); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
