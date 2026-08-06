//go:build windows

package cmd

import (
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/sys/windows"

	"github.com/rubenglez/doctier/internal/agex"
)

func TestWindowsSmudgeLeavesCiphertextForSecureUnlock(t *testing.T) {
	privPEM, pubLine := keyPair(t)
	recip, err := agessh.ParseRecipient(pubLine)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := agex.Encrypt([]byte("secret\n"), []age.Recipient{recip})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCTIER_SSH_KEY", "")
	t.Setenv("DOCTIER_IDENTITY", string(privPEM))

	got, err := smudge("secret/doc.md", ct)
	if err != nil {
		t.Fatalf("smudge: %v", err)
	}
	if !agex.ValidCiphertext(got) {
		t.Fatal("Windows smudge must leave ciphertext for doctier unlock to write with an owner-only DACL")
	}
}

func TestUnlockHardensExistingPlaintextWithoutClobberingIt(t *testing.T) {
	root := initRepo(t, privManifest)
	privPEM, pubLine := keyPair(t)
	write(t, root, ".doctier/recipients.txt", pubLine+"\n")
	recip, err := agessh.ParseRecipient(pubLine)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := agex.Encrypt([]byte("committed\n"), []age.Recipient{recip})
	if err != nil {
		t.Fatal(err)
	}
	write(t, root, "secret/doc.md", string(ct))
	git(t, root, "add", "-A")
	write(t, root, "secret/doc.md", "uncommitted work\n")

	t.Setenv("DOCTIER_SSH_KEY", "")
	t.Setenv("DOCTIER_IDENTITY", string(privPEM))
	if err := runUnlock(nil); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "secret/doc.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "uncommitted work\n" {
		t.Fatalf("unlock overwrote plaintext edits, got %q", got)
	}
	assertOwnerOnlyPermissions(t, filepath.Join(root, "secret/doc.md"))
}

func assertOwnerOnlyPermissions(t *testing.T, path string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatal(err)
	}
	control, _, err := sd.Control()
	if err != nil {
		t.Fatal(err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatal("unlock DACL must be protected from inherited access entries")
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("unlock must install one protected user ACE, got %#v", dacl)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	gotSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if !gotSID.Equals(user.User.Sid) {
		t.Fatalf("unlock ACL belongs to %s, want current user %s", gotSID, user.User.Sid)
	}
}
