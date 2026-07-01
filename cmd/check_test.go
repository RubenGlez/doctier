package cmd

import (
	"testing"

	"github.com/rubenglez/doctier/internal/agex"
)

func TestCheckBlocksCleartextPrivate(t *testing.T) {
	root := initRepo(t, `version: 1
docs:
  - path: "secret.md"
    visibility: private
    lifetime: durable
recipients_file: .doctier/recipients.txt
`)
	write(t, root, ".doctier/recipients.txt", pubKeyLine(t)+"\n")
	write(t, root, "secret.md", "TOP SECRET\n")
	git(t, root, "add", "-A") // staged as plaintext (no filter configured)
	if err := runCheck([]string{"--staged"}); err == nil {
		t.Fatal("expected check to fail: private file staged in cleartext")
	}
}

func TestCheckBlocksHybridArmorPlusPlaintext(t *testing.T) {
	// A blob that merely STARTS with the armor header must not pass: that is the
	// keyless-user-appends-notes-below-the-armor leak.
	root := initRepo(t, `version: 1
docs:
  - path: "secret.md"
    visibility: private
    lifetime: durable
recipients_file: .doctier/recipients.txt
`)
	write(t, root, ".doctier/recipients.txt", pubKeyLine(t)+"\n")
	hybrid := agex.ArmorHeader + "\nc29tZSBjaXBoZXJ0ZXh0\n-----END AGE ENCRYPTED FILE-----\nTOP SECRET appended in plaintext\n"
	write(t, root, "secret.md", hybrid)
	git(t, root, "add", "-A")
	if err := runCheck([]string{"--staged"}); err == nil {
		t.Fatal("expected check to fail: armor header followed by plaintext must not pass as encrypted")
	}
}

func TestCheckCatchesRenameIntoPrivatePath(t *testing.T) {
	// git mv keeps the blob (no clean filter re-run) and shows as a rename; with
	// diff-filter=ACM the pre-commit check would skip it entirely.
	root := initRepo(t, `version: 1
docs:
  - path: "secret/**"
    visibility: private
    lifetime: durable
recipients_file: .doctier/recipients.txt
`)
	write(t, root, ".doctier/recipients.txt", pubKeyLine(t)+"\n")
	write(t, root, "notes.md", "public then private\n")
	git(t, root, "add", "-A")
	git(t, root, "commit", "-qm", "init")
	write(t, root, "secret/.keep", "")
	git(t, root, "mv", "notes.md", "secret/notes.md")
	if err := runCheck([]string{"--staged"}); err == nil {
		t.Fatal("expected check to fail: plaintext renamed into a private path")
	}
}

func TestCheckReportsUnusableRecipients(t *testing.T) {
	root := initRepo(t, `version: 1
docs:
  - path: "secret/**"
    visibility: private
    lifetime: durable
recipients_file: .doctier/recipients.txt
`)
	write(t, root, ".doctier/recipients.txt", "# no keys yet\n")
	if err := runCheck(nil); err == nil {
		t.Fatal("expected check to fail: encrypted rules but no usable recipients")
	}
}

func TestCheckBlocksSensitiveStaged(t *testing.T) {
	root := initRepo(t, `version: 1
docs:
  - path: "_scratch/**"
    visibility: private
    lifetime: ephemeral
    sensitive: true
`)
	write(t, root, "_scratch/n.md", "scratch\n")
	git(t, root, "add", "-f", "_scratch/n.md")
	if err := runCheck([]string{"--staged"}); err == nil {
		t.Fatal("expected check to fail: sensitive ephemeral staged")
	}
}

func TestCheckUncoveredBlockOptIn(t *testing.T) {
	root := initRepo(t, `version: 1
docs: []
policy: { uncovered: block }
`)
	write(t, root, "random.md", "x\n")
	git(t, root, "add", "random.md")
	if err := runCheck([]string{"--staged"}); err == nil {
		t.Fatal("expected check to fail: uncovered doc with policy.uncovered=block")
	}
}

func TestCheckAllowsUncoveredByDefault(t *testing.T) {
	root := initRepo(t, `version: 1
docs: []
`)
	write(t, root, "random.md", "x\n")
	git(t, root, "add", "random.md")
	if err := runCheck([]string{"--staged"}); err != nil {
		t.Fatalf("expected default (allow) to pass, got %v", err)
	}
}

func TestCheckPassesPublicPlaintext(t *testing.T) {
	root := initRepo(t, `version: 1
docs:
  - path: "docs/**"
    visibility: public
    lifetime: durable
`)
	write(t, root, "docs/a.md", "hello\n")
	git(t, root, "add", "-A")
	if err := runCheck([]string{"--staged"}); err != nil {
		t.Fatalf("expected public plaintext to pass, got %v", err)
	}
}
