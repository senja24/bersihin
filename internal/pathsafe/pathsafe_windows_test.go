//go:build windows

package pathsafe

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// junction makes link a junction (mount point) to target, as mklink /J does.
func junction(t *testing.T, target, link string) {
	t.Helper()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Skipf("mklink /J: %v %s", err, out)
	}
}

func realWinTree(t *testing.T) (Guard, string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	h := filepath.Join(base, "ika")
	for _, d := range []string{"Documents", "Downloads", "Projects", ".ssh", filepath.Join("AppData", "Roaming", "Microsoft", "Credentials")} {
		if err := os.MkdirAll(filepath.Join(h, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return NewWindowsGuard(h, nil, nil, func(string) string { return "" }), h
}

func TestWindowsResolveRefusesJunctions(t *testing.T) {
	g, h := realWinTree(t)
	os.WriteFile(filepath.Join(h, "Projects", "a.iso"), []byte("x"), 0o644)
	// a junction itself, one into a protected folder and a path through one
	junction(t, filepath.Join(h, "AppData", "Roaming", "Microsoft", "Credentials"), filepath.Join(h, "Projects", "creds"))
	junction(t, filepath.Join(h, "Downloads"), filepath.Join(h, "Projects", "dl"))
	os.WriteFile(filepath.Join(h, "Downloads", "b.zip"), []byte("x"), 0o644)
	r := NewResolver(g)
	if _, v := r.Resolve(filepath.Join(h, "Projects", "a.iso")); v != OK {
		t.Fatalf("a plain file must pass, got %v", v)
	}
	for _, p := range []string{
		filepath.Join(h, "Projects", "creds"),
		filepath.Join(h, "Projects", "dl"),
		filepath.Join(h, "Projects", "dl", "b.zip"),
	} {
		if _, v := r.Resolve(p); v != Blocked {
			t.Errorf("Resolve(%s) = %v, want Blocked", p, v)
		}
	}
	if ContainedIn(filepath.Join(h, "Projects"), filepath.Join(h, "Projects", "dl", "b.zip"), true) {
		t.Error("ContainedIn must refuse a path through a junction")
	}
}

func TestWindowsResolveRefusesOtherSpellings(t *testing.T) {
	g, h := realWinTree(t)
	r := NewResolver(g)
	for _, p := range []string{
		filepath.Join(h, "Documents") + ".",
		filepath.Join(h, "DOCUMENTS"),
		filepath.Join(h, ".SSH", "id"),
		filepath.Join(h, "appdata", "roaming", "microsoft", "credentials"),
		`\\?\` + filepath.Join(h, "Projects"),
		`\\localhost\C$\Users`,
		filepath.Join(h, "Projects") + `:stream`,
	} {
		if _, v := r.Resolve(p); v != Blocked {
			t.Errorf("Resolve(%q) = %v, want Blocked", p, v)
		}
	}
}

func TestWindowsForPlatformUsesWindowsRules(t *testing.T) {
	g := ForPlatform(`C:\Users\ika`, nil)
	if !g.Windows || !g.FoldCase || len(g.Protected) == 0 {
		t.Fatalf("ForPlatform = %+v", g)
	}
	if g.Safe(`C:\Windows\Temp\x`) || g.Safe(`C:\Users\ika\AppData\Local\Temp\x`) || !g.Safe(`C:\Users\ika\Videos\x.mkv`) {
		t.Error("Windows rules not applied")
	}
}

func TestWindowsTildeUsesBackslashes(t *testing.T) {
	if got := Tilde(`C:\Users\ika`, `c:\users\IKA\Downloads\a.zip`); got != `~\Downloads\a.zip` {
		t.Errorf("Tilde = %q", got)
	}
	if got := ExpandTilde(`C:\Users\ika`, `~\Downloads`); got != `C:\Users\ika\Downloads` {
		t.Errorf("ExpandTilde = %q", got)
	}
}
