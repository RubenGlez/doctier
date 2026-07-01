package cmd

import (
	"bytes"
	"path/filepath"

	"github.com/rubenglez/doctier/internal/agex"
	"github.com/rubenglez/doctier/internal/config"
	"github.com/rubenglez/doctier/internal/gitx"
)

// recipientsPath resolves the recipients file relative to the repo root.
func recipientsPath(m *config.Manifest, root string) string {
	p := m.Visibility.Private.RecipientsFile
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(root, p)
}

// reuseCiphertext returns the already-staged ciphertext for file when it
// decrypts to the same plaintext, so identical content does not churn the blob.
// It returns nil when there is nothing to reuse (no staged blob, no key, or the
// content changed).
func reuseCiphertext(file string, plaintext []byte) []byte {
	staged, err := gitx.StagedBlob(file)
	if err != nil || !agex.IsEncrypted(staged) {
		return nil
	}
	id, err := agex.LoadIdentity("")
	if err != nil {
		return nil
	}
	dec, err := agex.Decrypt(staged, id)
	if err != nil {
		return nil
	}
	if bytes.Equal(dec, plaintext) {
		return staged
	}
	return nil
}
