package cmd

import "testing"

func TestCheckBlocksCleartextPrivate(t *testing.T) {
	root := initRepo(t, `version: 1
docs:
  - path: "secret.md"
    visibility: private
    lifetime: durable
recipients_file: .doctier/recipients.txt
`)
	write(t, root, "secret.md", "TOP SECRET\n")
	git(t, root, "add", "secret.md") // staged as plaintext (no filter configured)
	if err := runCheck([]string{"--staged"}); err == nil {
		t.Fatal("expected check to fail: private file staged in cleartext")
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
