//go:build windows

package cmd

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

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
