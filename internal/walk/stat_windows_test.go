//go:build windows

package walk

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsWalkSkipsJunctions(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret.bin"), make([]byte, 100), 0o644)
	os.WriteFile(filepath.Join(root, "a.bin"), make([]byte, 10), 0o644)
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "link"), outside).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v %s", err, out)
	}
	var seen []string
	st := Walk(context.Background(), root, Options{OnFile: func(f File) { seen = append(seen, f.Name) }, OnDir: func(p string, _ int) { seen = append(seen, p) }})
	if st.Files != 1 || st.Bytes != 10 || len(seen) != 1 {
		t.Fatalf("the junction was followed or reported: %+v %v", st, seen)
	}
}

func TestWindowsIdentity(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	os.WriteFile(a, []byte("hello"), 0o644)
	if err := os.Link(a, filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}
	fa, err := Lstat(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, _ := Lstat(filepath.Join(dir, "b.txt"))
	if Dev(fa) == 0 || Ino(fa) == 0 || Nlink(fa) != 2 || CTime(fa) == 0 {
		t.Fatalf("identity not read: dev %d ino %d nlink %d ctime %d", Dev(fa), Ino(fa), Nlink(fa), CTime(fa))
	}
	if Dev(fa) != Dev(fb) || Ino(fa) != Ino(fb) {
		t.Error("two names of one file must share the identity")
	}
	m, at, b := Times(fa)
	if m == 0 || at == 0 || b == 0 {
		t.Errorf("times: %d %d %d", m, at, b)
	}
	plain, _ := os.Lstat(a)
	if Dev(plain) != 0 || Nlink(plain) != 0 {
		t.Error("a plain os.Lstat result has no identity")
	}
	// the walk fills it when asked, and the device for SameDevice
	var got File
	Walk(context.Background(), dir, Options{Identity: true, SameDevice: true, OnFile: func(f File) {
		if f.Name == "a.txt" {
			got = f
		}
	}})
	if got.Ino != Ino(fa) || got.Nlink != 2 {
		t.Errorf("walk identity: %+v", got.Extra)
	}
}

func TestWindowsCloudPlaceholderIsDataless(t *testing.T) {
	// FILE_ATTRIBUTE_OFFLINE can be set by anyone, unlike the recall attributes
	dir := t.TempDir()
	p := filepath.Join(dir, "online-only.docx")
	os.WriteFile(p, make([]byte, 4096), 0o644)
	if out, err := exec.Command("attrib", "+O", p).CombinedOutput(); err != nil {
		t.Skipf("attrib +O: %v %s", err, out)
	}
	fi, _ := os.Lstat(p)
	if !extra(fi).IsDataless() || Allocated(fi) != 0 {
		t.Error("an offline file must count as a cloud placeholder")
	}
}
