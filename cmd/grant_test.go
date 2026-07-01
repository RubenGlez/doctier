package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGrantNoArgReencrypts(t *testing.T) {
	// The revoke flow: remove a line from the recipients file, then run grant
	// with no key — it must re-encrypt without demanding a new recipient.
	root := initRepo(t, `version: 1
docs:
  - path: "secret/**"
    visibility: private
    lifetime: durable
recipients_file: .doctier/recipients.txt
`)
	write(t, root, ".doctier/recipients.txt", pubKeyLine(t)+"\n")
	if err := runGrant(nil); err != nil {
		t.Fatalf("grant with no key must re-encrypt to the current set, got: %v", err)
	}
}

func TestGrantIgnoresCommentedOutKeys(t *testing.T) {
	root := initRepo(t, `version: 1
docs: []
recipients_file: .doctier/recipients.txt
`)
	key := pubKeyLine(t)
	write(t, root, ".doctier/recipients.txt", "# "+key+"\n")

	// A commented-out key is not an active recipient; granting it must succeed.
	if err := runGrant([]string{key}); err != nil {
		t.Fatalf("granting a key that only appears commented out must succeed, got: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".doctier/recipients.txt"))
	if err != nil {
		t.Fatal(err)
	}
	active := false
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == key {
			active = true
		}
	}
	if !active {
		t.Fatal("the key must be present as an active (uncommented) line")
	}

	// Granting the same key again must be rejected.
	if err := runGrant([]string{key}); err == nil {
		t.Fatal("granting an already-active key must fail")
	}
}
