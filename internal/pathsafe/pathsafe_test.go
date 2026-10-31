package pathsafe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dev.agus.bersihin/internal/testenv"
)

const home = "/Users/alice"

func guard() Guard { return NewGuard(home, []string{"/Volumes/Data"}) }

func TestSafeLexical(t *testing.T) {
	testenv.MacPaths(t)
	long := "/Users/alice/" + strings.Repeat("a", 5000)
	tests := []struct {
		name string
		path string
		want bool
	}{
		// allowed: the user's own files
		{"file in Downloads", home + "/Downloads/movie.mkv", true},
		{"file in Documents subfolder", home + "/Documents/Work/report.pdf", true},
		{"folder on Desktop", home + "/Desktop/old stuff", true},
		{"project folder directly in home", home + "/Projects", true},
		{"hidden dev cache in home", home + "/.npm", true},
		{"app leftovers deep in Library are denied by default", home + "/Library/Application Support/Foo/cache.db", false},
		{"Library/Caches entry is denied by default", home + "/Library/Caches/com.foo", false},
		{"trailing slash is harmless", home + "/Downloads/movie.mkv/", true},
		{"double slashes are collapsed", home + "//Downloads//movie.mkv", true},
		{"dot segments are collapsed", home + "/./Downloads/./movie.mkv", true},
		{"name starting with two dots", home + "/Downloads/..hidden", true},
		{"file inside Trash", home + "/.Trash/old.zip", true},

		// cloud folders at any depth, other sync and credential folders
		{"Dropbox nested in a project folder", home + "/Work/Dropbox/a", false},
		{"Dropbox variant nested", home + "/Work/Dropbox (Personal)/a.txt", false},
		{"Box nested", home + "/Work/Box/a.txt", false},
		{"ownCloud top", home + "/ownCloud/a.txt", false},
		{"MEGA nested", home + "/Stuff/MEGA/a.txt", false},
		{"pCloud Drive nested", home + "/Stuff/pCloud Drive/a.txt", false},
		{"Yandex.Disk nested", home + "/Stuff/Yandex.Disk/a.txt", false},
		{"Nextcloud nested", home + "/Stuff/Nextcloud/a.txt", false},
		{"Syncthing default folder", home + "/Sync/a.txt", false},
		{"folder named like Dropbox only by prefix", home + "/Work/Dropboxes/a", true},
		{"claude config", home + "/.claude/settings.json", false},
		{"codex", home + "/.codex/auth.json", false},
		{"cloudflared", home + "/.cloudflared/cert.pem", false},
		{"mcp-auth", home + "/.mcp-auth/tokens", false},
		{"appstoreconnect", home + "/.appstoreconnect/key.p8", false},
		{"ollama", home + "/.ollama/id_ed25519", false},
		{"keyrings", home + "/.local/share/keyrings/login.keyring", false},
		{"mozilla", home + "/.mozilla/firefox", false},
		{"thunderbird", home + "/.thunderbird/p", false},
		{"gem credentials", home + "/.gem/credentials", false},
		{"m2 settings", home + "/.m2/settings.xml", false},
		{"m2 repository stays allowed", home + "/.m2/repository", true},
		{"gradle properties", home + "/.gradle/gradle.properties", false},
		{"gradle caches stay allowed", home + "/.gradle/caches", true},
		{"vault token", home + "/.vault-token", false},
		{"boto", home + "/.boto", false},
		{"s3cfg", home + "/.s3cfg", false},
		{"pypirc", home + "/.pypirc", false},
		{"cloud folder nested on a data drive", "/Volumes/Data/Backup/Dropbox/a", false},
		{"cloud folder nested on a data drive deeper", "/Volumes/Data/Backup/x/OneDrive - Corp/a", false},
		{"plain nested folder on a data drive", "/Volumes/Data/Backup/a", true},

		// shape
		{"empty", "", false},
		{"relative", "Downloads/movie.mkv", false},
		{"relative dot", "./Downloads/movie.mkv", false},
		{"tilde is not expanded here", "~/Downloads/movie.mkv", false},
		{"NUL byte", home + "/Downloads/a\x00b", false},
		{"too long", long, false},
		{"filesystem root", "/", false},
		{"double slash root", "//", false},

		// traversal
		{"dotdot out of home", home + "/Downloads/../../bob/secret", false},
		{"dotdot to etc", home + "/../../etc/passwd", false},
		{"dotdot that stays inside", home + "/Downloads/../Projects", false},
		{"dotdot onto a protected folder", home + "/Projects/../Documents", false},
		{"dotdot into ssh", home + "/Downloads/../.ssh/id_ed25519", false},
		{"dotdot at the end", home + "/Downloads/..", false},

		// home itself and outside
		{"home itself", home, false},
		{"home with trailing slash", home + "/", false},
		{"parent of home", "/Users", false},
		{"other user", "/Users/bob/Downloads/x", false},
		{"sibling with the same prefix", "/Users/alice2/Downloads/x", false},
		{"tmp", "/tmp/x", false},

		// protected top level folders
		{"Desktop", home + "/Desktop", false},
		{"Documents", home + "/Documents", false},
		{"Downloads", home + "/Downloads", false},
		{"Pictures", home + "/Pictures", false},
		{"Movies", home + "/Movies", false},
		{"Music", home + "/Music", false},
		{"Library", home + "/Library", false},
		{"Applications", home + "/Applications", false},
		{"Public", home + "/Public", false},
		{".Trash", home + "/.Trash", false},
		{".config", home + "/.config", false},
		{".local", home + "/.local", false},
		{"Desktop trailing slash", home + "/Desktop/", false},
		{"Desktop dot segment", home + "/./Desktop", false},
		{"Documents lower case", home + "/documents", false},
		{"Documents upper case", home + "/DOCUMENTS", false},
		{"Downloads mixed case", home + "/dOwNlOaDs", false},
		{"Library lower case", home + "/library", false},

		// Library level two
		{"Application Support", home + "/Library/Application Support", false},
		{"Caches", home + "/Library/Caches", false},
		{"Preferences", home + "/Library/Preferences", false},
		{"Preferences lower case", home + "/LIBRARY/preferences", false},
		{"Containers", home + "/Library/Containers", false},

		// keys and credentials
		{".ssh", home + "/.ssh", false},
		{".ssh key", home + "/.ssh/id_ed25519", false},
		{".ssh lower/upper case", home + "/.SSH/known_hosts", false},
		{".gnupg", home + "/.gnupg", false},
		{".gnupg file", home + "/.gnupg/private-keys-v1.d/x.key", false},
		{"Keychains", home + "/Library/Keychains", false},
		{"Keychains file", home + "/Library/Keychains/login.keychain-db", false},
		{"Keychains case variant", home + "/library/keychains/login.keychain-db", false},
		{"nested .ssh", home + "/Projects/server/.ssh/id_rsa", false},
		{"nested .gnupg", home + "/Projects/.gnupg", false},
		{"nested Keychains under a Library", home + "/Projects/Library/Keychains/x", false},
		{"Keychains not under Library is fine", home + "/Projects/Keychains/x", true},

		// cloud
		{"iCloud Drive (Mobile Documents)", home + "/Library/Mobile Documents", false},
		{"iCloud Drive file", home + "/Library/Mobile Documents/com~apple~CloudDocs/Doc.pages", false},
		{"iCloud app container file", home + "/Library/Mobile Documents/iCloud~md~obsidian/Documents/note.md", false},
		{"CloudStorage", home + "/Library/CloudStorage", false},
		{"CloudStorage Dropbox", home + "/Library/CloudStorage/Dropbox/photo.jpg", false},
		{"CloudStorage OneDrive", home + "/Library/CloudStorage/OneDrive-Acme/x.docx", false},
		{"CloudStorage case variant", home + "/library/cloudstorage/GoogleDrive-me@x.com/x", false},
		{"Mobile Documents case variant", home + "/LIBRARY/MOBILE DOCUMENTS/x", false},
		{"Dropbox root", home + "/Dropbox", false},
		{"iCloud Drive root", home + "/iCloud Drive", false},
		{"iCloudDrive root", home + "/iCloudDrive", false},
		{"Google Drive root", home + "/Google Drive", false},
		{"My Drive root", home + "/My Drive", false},
		{"OneDrive root", home + "/OneDrive", false},
		{"OneDrive company root", home + "/OneDrive - Acme Corp", false},
		{"Creative Cloud Files root", home + "/Creative Cloud Files", false},
		{"Yandex.Disk root", home + "/Yandex.Disk", false},
		{"Dropbox lower case", home + "/dropbox", false},
		{"Dropbox subfolder is protected", home + "/Dropbox/old", false},
		{"Dropbox deep file is protected", home + "/Dropbox/a/b/c.txt", false},
		{"Google Drive subfolder is protected", home + "/Google Drive/x", false},
		{"OneDrive company subfolder is protected", home + "/OneDrive - Acme Corp/x/y", false},
		{"iCloud Drive subfolder is protected", home + "/iCloud Drive/x", false},
		{"Dropbox lower case subfolder", home + "/dropbox/Old", false},
		{"Dropbox look-alike name is fine", home + "/Dropbox2/old", true},
		{"Dropbox personal", home + "/Dropbox (Personal)", false},
		{"Dropbox personal inside", home + "/Dropbox (Personal)/x/y", false},
		{"Dropbox team", home + "/Dropbox (Acme Inc)/x", false},
		{"Box", home + "/Box/x", false},
		{"pCloud Drive", home + "/pCloud Drive/x", false},
		{"Nextcloud", home + "/Nextcloud/x", false},
		{"MEGA", home + "/MEGA/x", false},
		{"SynologyDrive", home + "/SynologyDrive/x", false},
		{"Boxes look-alike is fine", home + "/Boxes/x", true},
		{"Megabyte look-alike is fine", home + "/Megabyte/x", true},

		// OS folders (only reachable if the "home" lives there)
		{"System", "/System/Library/CoreServices", false},
		{"Library", "/Library/Preferences", false},
		{"Applications", "/Applications", false},
		{"app bundle", "/Applications/Safari.app", false},
		{"usr", "/usr/local/bin/x", false},
		{"private var", "/private/var/db/x", false},
		{"Volumes", "/Volumes", false},

		// data volume
		{"file on data volume", "/Volumes/Data/Videos/old.mov", true},
		{"folder on data volume", "/Volumes/Data/Archive", true},
		{"data volume root", "/Volumes/Data", false},
		{"data volume root slash", "/Volumes/Data/", false},
		{"other volume", "/Volumes/Other/x", false},
		{"volume prefix sibling", "/Volumes/Data2/x", false},
		{"volume dotdot", "/Volumes/Data/../Other/x", false},
		{"volume Trashes", "/Volumes/Data/.Trashes/501/x", false},
		{"volume Spotlight", "/Volumes/Data/.Spotlight-V100", false},
		{"volume System", "/Volumes/Data/System/x", false},
		{"volume Library", "/Volumes/Data/Library/x", false},
		{"volume Users", "/Volumes/Data/Users/bob/x", false},
		{"volume Applications", "/Volumes/Data/Applications/x.app", false},
		{"volume Backups.backupdb", "/Volumes/Data/Backups.backupdb/x", false},
		{"volume system folder case", "/Volumes/Data/SYSTEM/x", false},
		{"volume .ssh", "/Volumes/Data/Projects/.ssh/id", false},
		{"volume Dropbox root", "/Volumes/Data/Dropbox", false},
		{"volume Dropbox inside", "/Volumes/Data/Dropbox/old", false},
		{"volume Dropbox deep", "/Volumes/Data/Dropbox/a/b/c", false},
		{"volume Google Drive inside", "/Volumes/Data/Google Drive/x", false},
		{"volume OneDrive inside", "/Volumes/Data/OneDrive - Acme/x", false},
		{"volume Dropbox team", "/Volumes/Data/Dropbox (Acme)/x", false},
		{"volume Box", "/Volumes/Data/Box/x", false},
		{"volume pCloud Drive", "/Volumes/Data/pCloud Drive/x", false},
		{"volume Nextcloud", "/Volumes/Data/Nextcloud/x", false},
		{"volume MEGA", "/Volumes/Data/MEGA/x", false},
		{"volume SynologyDrive", "/Volumes/Data/SynologyDrive/x", false},
		{"volume Dropbox look-alike is fine", "/Volumes/Data/Dropbox2/old", true},
		{"volume hidden top", "/Volumes/Data/.hidden/x", false},
	}
	g := guard()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := g.Safe(tc.path); got != tc.want {
				t.Errorf("Safe(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestSafeCaseSensitive(t *testing.T) {
	testenv.MacPaths(t)
	g := guard()
	g.FoldCase = false
	tests := []struct {
		path string
		want bool
	}{
		{home + "/Documents", false},
		{home + "/documents", true}, // a different folder on a case-sensitive volume
		{home + "/Documents/x", true},
		{home + "/.ssh/x", false},
		{home + "/.SSH/x", true},
	}
	for _, tc := range tests {
		if got := g.Safe(tc.path); got != tc.want {
			t.Errorf("Safe(%q) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestSafeKnownFolderRedirectedToDataVolume(t *testing.T) {
	testenv.MacPaths(t)
	g := guard()
	g.Known = append(g.Known, "/Volumes/Data/Users/alice/Documents")
	g.Drives = []string{"/Volumes/Data", "/Volumes/Archive"}
	for path, want := range map[string]bool{
		"/Volumes/Data/Users/alice/Documents":         false,
		"/Volumes/Data/Users/alice/Documents/old.doc": false, // Users is OS territory anyway
		"/Volumes/Data/Projects":                      true,
		"/Volumes/Archive/Stuff":                      true,
	} {
		if got := g.Safe(path); got != want {
			t.Errorf("Safe(%q) = %v, want %v", path, got, want)
		}
	}
	// a folder holding a known folder must not go either
	g.Known = []string{"/Volumes/Data/Holder/Documents"}
	if g.Safe("/Volumes/Data/Holder") {
		t.Error("a folder that holds Documents was allowed")
	}
}

func TestSafeDriveHoldingHome(t *testing.T) {
	// A volume that contains or equals the home folder is the system volume.
	g := NewGuard(home, []string{"/", "/Users", home})
	if g.Safe("/Users/bob/x") || g.Safe("/x") {
		t.Error("system volume treated as a data volume")
	}
}

func TestZeroGuardAllowsNothing(t *testing.T) {
	var g Guard
	for _, p := range []string{"/", "/Users/alice/x", "/tmp/x", ""} {
		if g.Safe(p) {
			t.Errorf("zero guard allowed %q", p)
		}
	}
	if NewGuard("/", nil).Safe("/x") {
		t.Error("home = / allowed something")
	}
	if NewGuard("relative", nil).Safe("relative/x") {
		t.Error("relative home allowed something")
	}
}

// realTree builds home/{Downloads,Documents,Projects,.ssh} and an outside folder in temp dirs.
func realTree(t *testing.T) (g Guard, homeDir, outside string) {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	homeDir = filepath.Join(base, "home", "alice")
	outside = filepath.Join(base, "outside")
	for _, d := range []string{"Downloads", "Documents", "Projects", ".ssh", "Library/Keychains", "Library/CloudStorage/Dropbox"} {
		if err := os.MkdirAll(filepath.Join(homeDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	g = NewGuard(homeDir, nil)
	g.SystemRoots = nil // the temp dir may live under /var on a Mac
	return g, homeDir, outside
}

func touch(t *testing.T, p string) {
	t.Helper()
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := testenv.Symlink(t, target, link); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
}

func TestResolve(t *testing.T) {
	testenv.MacPaths(t)
	g, h, outside := realTree(t)
	touch(t, filepath.Join(h, "Downloads", "a.zip"))
	touch(t, filepath.Join(outside, "precious"))
	touch(t, filepath.Join(h, ".ssh", "id_rsa"))
	touch(t, filepath.Join(h, "Library", "CloudStorage", "Dropbox", "f.txt"))

	// links
	symlink(t, outside, filepath.Join(h, "Downloads", "escape"))                       // dir link out of home
	symlink(t, filepath.Join(outside, "precious"), filepath.Join(h, "Downloads", "l")) // file link out of home
	symlink(t, filepath.Join(h, ".ssh"), filepath.Join(h, "Projects", "sshlink"))      // dir link onto a key store
	symlink(t, filepath.Join(h, "Documents"), filepath.Join(h, "Projects", "docs"))    // dir link onto a protected folder
	symlink(t, filepath.Join(h, "Downloads", "nowhere"), filepath.Join(h, "Downloads", "broken"))

	r := NewResolver(g)
	tests := []struct {
		name string
		path string
		want Verdict
	}{
		{"plain file", filepath.Join(h, "Downloads", "a.zip"), OK},
		{"plain folder", filepath.Join(h, "Projects"), OK},
		{"trailing slash", filepath.Join(h, "Downloads", "a.zip") + "/", OK},
		{"missing", filepath.Join(h, "Downloads", "missing"), Gone},
		{"missing parent", filepath.Join(h, "Downloads", "no", "such"), Gone},
		{"symlink to dir outside", filepath.Join(h, "Downloads", "escape"), Blocked},
		{"through symlink to dir outside", filepath.Join(h, "Downloads", "escape", "precious"), Blocked},
		{"symlink to file outside", filepath.Join(h, "Downloads", "l"), Blocked},
		{"through symlink onto .ssh", filepath.Join(h, "Projects", "sshlink", "id_rsa"), Blocked},
		{"symlink onto .ssh itself", filepath.Join(h, "Projects", "sshlink"), Blocked},
		{"through symlink onto Documents folder", filepath.Join(h, "Projects", "docs"), Blocked},
		{"broken symlink is still a link", filepath.Join(h, "Downloads", "broken"), Blocked},
		{"dotdot", h + "/Downloads/../Projects", Blocked},
		{"dotdot out", h + "/../../outside/precious", Blocked},
		{"outside", filepath.Join(outside, "precious"), Blocked},
		{"home itself", h, Blocked},
		{"Documents itself", filepath.Join(h, "Documents"), Blocked},
		{"Documents case variant", filepath.Join(h, "documents"), Blocked},
		{"ssh key", filepath.Join(h, ".ssh", "id_rsa"), Blocked},
		{"cloud file", filepath.Join(h, "Library", "CloudStorage", "Dropbox", "f.txt"), Blocked},
		{"relative", "Downloads/a.zip", Blocked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			real, got := r.Resolve(tc.path)
			if got != tc.want {
				t.Fatalf("Resolve(%q) = %v (%q), want %v", tc.path, got, real, tc.want)
			}
			if got == OK && real != filepath.Clean(tc.path) {
				t.Errorf("canonical path = %q, want %q", real, filepath.Clean(tc.path))
			}
		})
	}
}

func TestResolveHomeThroughSymlink(t *testing.T) {
	testenv.MacPaths(t)
	// /base/link -> /base/real/alice ; the user's home is reached through the link
	base, _ := filepath.EvalSymlinks(t.TempDir())
	real := filepath.Join(base, "real", "alice")
	if err := os.MkdirAll(filepath.Join(real, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(real, "Downloads", "a"))
	link := filepath.Join(base, "link")
	symlink(t, real, link)

	g := NewGuard(link, nil)
	g.SystemRoots = nil
	r := NewResolver(g)
	got, v := r.Resolve(filepath.Join(link, "Downloads", "a"))
	if v != OK || got != filepath.Join(real, "Downloads", "a") {
		t.Fatalf("got %q %v", got, v)
	}
	if _, v := r.Resolve(filepath.Join(link, "Downloads")); v != Blocked {
		t.Fatalf("Downloads itself via home link: %v", v)
	}
	if _, v := r.Resolve(filepath.Join(link, "Documents")); v == OK {
		t.Fatal("Documents allowed through home link")
	}
}

func TestResolveDataDrive(t *testing.T) {
	testenv.MacPaths(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	h := filepath.Join(base, "home", "alice")
	vol := filepath.Join(base, "vol")
	for _, d := range []string{h, filepath.Join(vol, "Stuff"), filepath.Join(vol, "System")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	touch(t, filepath.Join(vol, "Stuff", "a"))
	touch(t, filepath.Join(vol, "System", "b"))
	symlink(t, h, filepath.Join(vol, "Stuff", "tohome"))

	g := NewGuard(h, []string{vol})
	g.SystemRoots = nil
	r := NewResolver(g)
	if _, v := r.Resolve(filepath.Join(vol, "Stuff", "a")); v != OK {
		t.Errorf("file on data volume: %v", v)
	}
	if _, v := r.Resolve(filepath.Join(vol, "System", "b")); v != Blocked {
		t.Errorf("OS folder on data volume: %v", v)
	}
	if _, v := r.Resolve(vol); v != Blocked {
		t.Errorf("volume root: %v", v)
	}
	if _, v := r.Resolve(filepath.Join(vol, "Stuff", "tohome")); v != Blocked {
		t.Errorf("link to home: %v", v)
	}
}

func TestResolverDropsLinkedDrives(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	h := filepath.Join(base, "home", "alice")
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "Stuff"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(h, 0o755)
	touch(t, filepath.Join(real, "Stuff", "a"))
	linked := filepath.Join(base, "linked")
	symlink(t, real, linked)

	g := NewGuard(h, []string{linked})
	g.SystemRoots = nil
	if _, v := NewResolver(g).Resolve(filepath.Join(linked, "Stuff", "a")); v != Blocked {
		t.Errorf("mount that is a link accepted: %v", v)
	}
}

func TestContainedIn(t *testing.T) {
	testenv.MacPaths(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "caches")
	outside := filepath.Join(base, "outside")
	os.MkdirAll(filepath.Join(root, "app", "sub"), 0o755)
	os.MkdirAll(outside, 0o755)
	touch(t, filepath.Join(root, "app", "sub", "f"))
	touch(t, filepath.Join(outside, "f"))
	symlink(t, outside, filepath.Join(root, "evil"))
	symlink(t, filepath.Join(outside, "f"), filepath.Join(root, "evilfile"))
	// a sibling folder sharing the root's name as a prefix
	os.MkdirAll(filepath.Join(base, "caches-other"), 0o755)

	tests := []struct {
		name   string
		target string
		want   bool
	}{
		{"entry", filepath.Join(root, "app"), true},
		{"deep entry", filepath.Join(root, "app", "sub", "f"), true},
		{"root itself", root, false},
		{"root with slash", root + "/", false},
		{"link to outside dir", filepath.Join(root, "evil"), false},
		{"through link", filepath.Join(root, "evil", "f"), false},
		{"link to outside file", filepath.Join(root, "evilfile"), false},
		{"outside", filepath.Join(outside, "f"), false},
		{"sibling prefix", filepath.Join(base, "caches-other"), false},
		{"dotdot", root + "/app/../../outside/f", false},
		{"missing", filepath.Join(root, "ghost"), false},
		{"relative", "app", false},
		{"empty", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContainedIn(root, tc.target, false); got != tc.want {
				t.Errorf("ContainedIn(%q) = %v, want %v", tc.target, got, tc.want)
			}
		})
	}
	// root reached through a link: both are resolved
	linkRoot := filepath.Join(base, "rootlink")
	symlink(t, root, linkRoot)
	if !ContainedIn(linkRoot, filepath.Join(linkRoot, "app"), false) {
		t.Error("root reached through a link should still contain its entries")
	}
}

func TestAppBundle(t *testing.T) {
	// Only the lexical rules can be tested without /Applications.
	h := "/Users/alice"
	for path, want := range map[string]bool{
		"/Applications/Safari.app/":     false, // does not exist here
		"/Applications":                 false,
		"/Applications/Foo":             false,
		"/System/Applications/Mail.app": false,
		"/usr/local/Foo.app":            false,
		"/Applications/../etc/Foo.app":  false,
		"relative/Foo.app":              false,
		"":                              false,
	} {
		if got := AppBundle(h, path); got != want {
			t.Errorf("AppBundle(%q) = %v, want %v", path, got, want)
		}
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	apps := filepath.Join(base, "Applications")
	os.MkdirAll(filepath.Join(apps, "Foo.app"), 0o755)
	os.MkdirAll(filepath.Join(apps, "Dir"), 0o755)
	symlink(t, filepath.Join(apps, "Foo.app"), filepath.Join(apps, "Link.app"))
	if !AppBundle(base, filepath.Join(apps, "Foo.app")) {
		t.Error("real bundle in ~/Applications refused")
	}
	if AppBundle(base, filepath.Join(apps, "Link.app")) {
		t.Error("symlinked bundle accepted")
	}
	if AppBundle(base, filepath.Join(apps, "Dir")) {
		t.Error("non-bundle accepted")
	}
}

func TestTildeAndExpand(t *testing.T) {
	testenv.MacPaths(t)
	for in, want := range map[string]string{
		home:                   "~",
		home + "/":             "~",
		home + "/Downloads/a":  "~/Downloads/a",
		"/Users/alice2/x":      "/Users/alice2/x",
		"/tmp/x":               "/tmp/x",
		"/users/alice/Library": "~/Library",
	} {
		if got := Tilde(home, in); got != want {
			t.Errorf("Tilde(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"~":           home,
		"~/Downloads": home + "/Downloads",
		"~bob/x":      "~bob/x",
		"/abs":        "/abs",
	} {
		if got := ExpandTilde(home, in); got != want {
			t.Errorf("ExpandTilde(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContainedInRejectsLinkInTheMiddle(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "caches")
	os.MkdirAll(filepath.Join(root, "real", "sub"), 0o755)
	touch(t, filepath.Join(root, "real", "sub", "f"))
	symlink(t, filepath.Join(root, "real"), filepath.Join(root, "alias")) // stays inside root
	if ContainedIn(root, filepath.Join(root, "alias", "sub", "f"), false) {
		t.Error("a path through a link was accepted, even though the link stays inside the root")
	}
	if !ContainedIn(root, filepath.Join(root, "real", "sub", "f"), false) {
		t.Error("the real path was refused")
	}
}

func TestSafeUnicodeAndSpellingVariants(t *testing.T) {
	testenv.MacPaths(t)
	g := guard()
	nfd := func(s string) string { return strings.NewReplacer("\u00E9", "e\u0301").Replace(s) }
	tests := []struct {
		name string
		path string
		want bool
	}{
		// protected folders under other spellings
		{"long s in Documents", home + "/Document\u017F", false},
		{"long s in Downloads", home + "/Download\u017F", false},
		{"long s in Documents file below", home + "/Document\u017F/x", true}, // below a protected top is fine, as for Documents/x
		{"Kelvin sign in Keychains", home + "/Library/\u212Aeychains/login.keychain-db", false},
		{"long s twice in .ssh", home + "/.\u017F\u017Fh/id", false},
		{"zero width non joiner", home + "/Docu\u200Cments", false},
		{"zero width space is not ignorable", home + "/Docu\u200Bments", true},
		{"byte order mark", home + "/\ufeffDocuments", false},
		{"right to left override", home + "/Docu\u202Ements", false},
		{"invisible inside .ssh", home + "/.s\u200Csh/id_rsa", false},
		{"invisible inside CloudStorage", home + "/Library/Cloud\u200CStorage/Dropbox/f", false},
		{"invisible Library level two", home + "/Library/Pref\u200Cerences", false},
		{"invisible in Mobile Documents", home + "/Library/Mobile\u200C Documents/x", false},
		{"NFD of a plain name stays the same", home + "/Documents", false},
		{"invisible inside a volume's Users", "/Volumes/Data/Us\u200Cers/bob/x", false},
		// NFC / NFD of non-ASCII names
		{"NFC user folder is allowed", home + "/Proj\u00E9ct/file", true},
		{"NFD user folder is allowed", home + "/" + nfd("Proj\u00E9ct") + "/file", true},
		{"NFD home spelling", "/Users/ali" + "c" + "e/Downloads/x", true},
		// slash variants
		{"trailing slash on protected", home + "/Documents/", false},
		{"trailing dot segment", home + "/Documents/.", false},
		{"dot segment before", home + "/./Documents/.", false},
		{"two trailing slashes", home + "/Documents//", false},
		{"dot after file", home + "/Downloads/a.zip/.", true},
		{"cloud root slash dot", home + "/Dropbox/.", false},
		// secrets and extended list
		{".aws", home + "/.aws/credentials", false},
		{".kube", home + "/.kube/config", false},
		{".docker", home + "/.docker/config.json", false},
		{".netrc", home + "/.netrc", false},
		{".password-store", home + "/.password-store/mail.gpg", false},
		{"gcloud", home + "/.config/gcloud/credentials.db", false},
		{"gh", home + "/.config/gh/hosts.yml", false},
		{"other .config entry is denied", home + "/.config/htop/htoprc", false},
		{".npmrc", home + "/.npmrc", false},
		{".yarnrc", home + "/.yarnrc", false},
		{".pgpass", home + "/.pgpass", false},
		{".cargo credentials", home + "/.cargo/credentials", false},
		{".cargo credentials.toml", home + "/.cargo/credentials.toml", false},
		{".cargo credentials case", home + "/.cargo/CREDENTIALS.toml", false},
		{".cargo registry is fine", home + "/.cargo/registry/cache/x", true},
		{".azure", home + "/.azure/accessTokens.json", false},
		{".terraform.d", home + "/.terraform.d/credentials.tfrc.json", false},
		{".password-store nested", home + "/.password-store/a/b.gpg", false},
		{".config deep", home + "/.config/anything/deep/file", false},
		{".npmrc elsewhere is fine", home + "/Projects/app/.npmrc", true},
		{"Group Containers", home + "/Library/Group Containers/group.com.apple.notes/x", false},
		{"Messages", home + "/Library/Messages/chat.db", false},
		{"Mail", home + "/Library/Mail/V10/x", false},
		{"1Password", home + "/Library/Application Support/1Password/data.sqlite", false},
		{"Application Support/Foo is denied", home + "/Library/Application Support/Foo/x", false},
		{"Containers Docker.raw", home + "/Library/Containers/com.docker.docker/Data/vms/0/data/Docker.raw", false},
		{"Library LaunchAgents", home + "/Library/LaunchAgents/x.plist", false},
		{"Library other spelling", home + "/LIBRARY/Application Support/Foo/x", false},
		{"photos library", home + "/Pictures/Photos Library.photoslibrary", false},
		{"inside photos library", home + "/Pictures/Photos Library.photoslibrary/originals/a.heic", false},
		{"photos library upper case", home + "/Pictures/X.PHOTOSLIBRARY/a", false},
		{"photos library elsewhere", home + "/Projects/Old.photoslibrary", false},
		{"photos library on a volume", "/Volumes/Data/Old.photoslibrary/a", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := g.Safe(tc.path); got != tc.want {
				t.Errorf("Safe(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestFoldMatchesHomeSpellings(t *testing.T) {
	testenv.MacPaths(t)
	// the home folder written with other case, composition or invisible characters
	g := NewGuard("/Users/Zo\u00EB", nil) // NFC \u00EB
	for path, want := range map[string]bool{
		"/Users/Zo\u00EB/Documents":            false,
		"/Users/Zoe\u0308/Documents":           false, // NFD spelling of the same home
		"/users/zoe\u0308/documents":           false,
		"/Users/Zoe\u0308/Downloads/a":         true,
		"/Users/Zo\u200C\u00EB/Downloads/a":    true, // invisible char in the home itself: still the home
		"/Users/Zo\u200C\u00EB":                false,
		"/Users/Zoe\u0308":                     false,
		"/Users/Zoe\u0308/Library/Keychains/x": false,
		"/Users/Zoe/Downloads/a":               false, // a different account
	} {
		if got := g.Safe(path); got != want {
			t.Errorf("Safe(%q) = %v, want %v", path, got, want)
		}
	}
	// a Kelvin sign and a long s fold to k and s
	g = NewGuard("/Users/alice", nil)
	if g.Same("/Users/alice/Desktop", "/Users/alice/De\u017Fktop") != true {
		t.Error("long s should fold to s")
	}
	if !g.Same("/Users/alice/\u212A", "/Users/alice/k") {
		t.Error("Kelvin sign should fold to k")
	}
	// a case-sensitive guard is untouched
	g.FoldCase = false
	if g.Same("/a/K", "/a/\u212A") || g.Same("/a/Docs", "/a/docs") {
		t.Error("case-sensitive guard folded")
	}
}

func TestSafeRefusesFolderHoldingARedirectedUserFolder(t *testing.T) {
	testenv.MacPaths(t)
	g := guard()
	g.Known = append(g.Known, home+"/Work/Documents") // Documents is a link into ~/Work
	for path, want := range map[string]bool{
		home + "/Work":             false,
		home + "/Work/Documents":   false,
		home + "/Work/Documents/a": true,
		home + "/Work/Other":       true,
		home + "/Work/":            false,
		home + "/Work/.":           false,
		home + "/WORK":             false,
		home + "/Projects":         true,
	} {
		if got := g.Safe(path); got != want {
			t.Errorf("Safe(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestResolveFolderHoldingRedirectedDocuments(t *testing.T) {
	testenv.MacPaths(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	h := filepath.Join(base, "alice")
	os.MkdirAll(filepath.Join(h, "Work", "Documents"), 0o755)
	os.MkdirAll(filepath.Join(h, "Work", "Other"), 0o755)
	symlink(t, filepath.Join(h, "Work", "Documents"), filepath.Join(h, "Documents"))
	g := NewGuard(h, nil)
	g.SystemRoots = nil
	r := NewResolver(g)
	for p, want := range map[string]Verdict{
		filepath.Join(h, "Work"):              Blocked,
		filepath.Join(h, "Work", "Documents"): Blocked,
		filepath.Join(h, "Documents"):         Blocked,
		filepath.Join(h, "Work", "Other"):     OK,
	} {
		if _, v := r.Resolve(p); v != want {
			t.Errorf("Resolve(%q) = %v, want %v", p, v, want)
		}
	}
}

func TestProtectedByIdentity(t *testing.T) {
	testenv.MacPaths(t)
	g, h, _ := realTree(t)
	os.MkdirAll(filepath.Join(h, "Library", "Caches"), 0o755)
	r := NewResolver(g)
	alias := filepath.Join(t.TempDir(), "alias")
	symlink(t, filepath.Join(h, ".ssh"), alias)
	aliasDocs := filepath.Join(t.TempDir(), "docs")
	symlink(t, filepath.Join(h, "Documents"), aliasDocs)
	aliasCaches := filepath.Join(t.TempDir(), "caches")
	symlink(t, filepath.Join(h, "Library", "Caches"), aliasCaches)
	touch(t, filepath.Join(h, ".ssh", "k"))

	// the helper looks at inodes (it follows the alias here), whatever the name says
	if !r.protectedByIdentity(alias) || !r.protectedByIdentity(filepath.Join(alias, "k")) {
		t.Error(".ssh and its contents must be protected by identity")
	}
	if !r.protectedByIdentity(aliasDocs) || !r.protectedByIdentity(aliasCaches) || !r.protectedByIdentity(h) {
		t.Error("exact protected folders must be recognised by identity")
	}
	if r.protectedByIdentity(filepath.Join(h, "Projects")) {
		t.Error("Projects is not protected")
	}
	touch(t, filepath.Join(h, "Documents", "a.txt"))
	if r.protectedByIdentity(filepath.Join(h, "Documents", "a.txt")) {
		t.Error("a file in Documents is not protected by identity (only the folder is)")
	}
}

func TestTildeOfUncleanedPaths(t *testing.T) {
	testenv.MacPaths(t)
	for in, want := range map[string]string{
		home + "/Downloads/../Downloads/a": "~/Downloads/a",
		home + "//Downloads///a/":          "~/Downloads/a",
		home + "/./":                       "~",
		"/Users/alice/../bob/x":            "/Users/bob/x",
		"/USERS/ALICE/x":                   "~/x",
		"relative/path":                    "relative/path",
		"":                                 "",
		"/Users/Alice\u200C/x":             "~/x",
	} {
		if got := Tilde(home, in); got != want {
			t.Errorf("Tilde(%q) = %q, want %q", in, got, want)
		}
	}
	// NFD home spelled differently from the path: the suffix keeps the path's own spelling
	if got := Tilde("/Users/Zo\u00EB", "/Users/Zoe\u0308/Caf\u00E9"); got != "~/Caf\u00E9" {
		t.Errorf("got %q", got)
	}
}

func TestContainedInRootCaseVariants(t *testing.T) {
	testenv.MacPaths(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "Caches")
	os.MkdirAll(filepath.Join(root, "app"), 0o755)
	touch(t, filepath.Join(root, "app", "f"))
	// On a case-sensitive disk a differently cased root is another (missing)
	// folder. On a case-insensitive one (the macOS default) it is the same
	// folder: the folded comparison accepts it, the exact one does not.
	_, statErr := os.Lstat(filepath.Join(base, "caches"))
	insensitive := statErr == nil
	for _, target := range []string{filepath.Join(base, "caches", "app"), filepath.Join(base, "CACHES", "app", "f")} {
		if got := ContainedIn(root, target, true); got != insensitive {
			t.Errorf("ContainedIn(fold) accepted %q = %v, case-insensitive disk = %v", target, got, insensitive)
		}
		if ContainedIn(root, target, false) {
			t.Errorf("ContainedIn(exact) accepted %q", target)
		}
	}
	if !ContainedIn(root, filepath.Join(root, "app", "f"), true) {
		t.Error("exact spelling refused")
	}
	if ContainedIn(root, root+"/.", true) || ContainedIn(root, root+"/", true) {
		t.Error("the root itself with a slash variant was accepted")
	}
	if !ContainedIn(root, filepath.Join(root, "app")+"/.", true) {
		t.Error("trailing /. variant of a real entry refused")
	}
}

// A folder named "..<ZWNJ>" folds to ".." and must never be read as a parent step.
func TestFoldedDotDotSegmentsAreRefused(t *testing.T) {
	testenv.MacPaths(t)
	const zw = "..‌"
	g := guard()
	for _, p := range []string{
		home + "/proj/" + zw + "/" + zw + "/" + zw + "/etc/x",
		home + "/Downloads/" + zw + "/.ssh/id_rsa",
		home + "/proj/.ssh/" + zw + "/id_rsa",
		home + "/Library/Keychains/" + zw + "/" + zw + "/Work/login.keychain-db",
		home + "/‌",    // a segment that folds to nothing
		home + "/.‌/x", // folds to "."
		"/Volumes/Data/a/" + zw + "/" + zw + "/Users/bob/x",
	} {
		if g.Safe(p) {
			t.Errorf("Safe(%q) = true", p)
		}
	}
	// Inside / Same never read the folded segment as a step up
	if g.Inside(home+"/a", home+"/a/b/"+zw+"/"+zw+"/"+zw+"/c") {
		t.Error("Inside followed a folded ..")
	}
	if Tilde(home, "/Users/bob/"+zw+"/alice/x") != filepath.Clean("/Users/bob/"+zw+"/alice/x") {
		t.Errorf("Tilde read a folded .. as a step: %q", Tilde(home, "/Users/bob/"+zw+"/alice/x"))
	}
}

func TestFoldedDotDotOnDisk(t *testing.T) {
	const zw = "..‌"
	base, _ := filepath.EvalSymlinks(t.TempDir())
	h := filepath.Join(base, "Users", "alice")
	outside := filepath.Join(base, "tmp")
	victim := filepath.Join(outside, "a", "b", zw, zw, zw, "Users", "alice", "victim.txt")
	os.MkdirAll(filepath.Dir(victim), 0o755)
	os.WriteFile(victim, []byte("x"), 0o644)
	os.MkdirAll(h, 0o755)
	key := filepath.Join(h, "proj", ".ssh", zw, "id_rsa")
	os.MkdirAll(filepath.Dir(key), 0o755)
	os.WriteFile(key, []byte("x"), 0o644)
	kc := filepath.Join(h, "Library", "Keychains", zw, zw, "Work", "login.keychain-db")
	os.MkdirAll(filepath.Dir(kc), 0o755)
	os.WriteFile(kc, []byte("x"), 0o644)

	g := NewGuard(h, nil)
	g.SystemRoots = nil
	r := NewResolver(g)
	for _, p := range []string{victim, key, kc} {
		if real, v := r.Resolve(p); v == OK {
			t.Errorf("Resolve(%q) = OK (%q)", p, real)
		}
	}
	// ContainedIn too: a target outside the root reached through folded ".."
	root := filepath.Join(h, "Library", "Caches")
	os.MkdirAll(root, 0o755)
	tgt := filepath.Join(base, "x", "y", zw, zw, zw, "Users", "alice", "Library", "Caches", "f")
	os.MkdirAll(tgt, 0o755)
	if ContainedIn(h, tgt, true) || ContainedIn(root, filepath.Join(root, "a", zw, zw, "b"), true) {
		t.Error("ContainedIn followed a folded ..")
	}
}

// A folder that CONTAINS the real location of a protected item is as
// protected as the item: moving ~/.config to ~/dotfiles/config must not make
// ~/dotfiles trashable.
func TestResolveRefusesFolderHoldingRelocatedProtectedItem(t *testing.T) {
	testenv.MacPaths(t)
	g, h, _ := realTree(t)
	os.MkdirAll(filepath.Join(h, "dotfiles", "config"), 0o755)
	os.MkdirAll(filepath.Join(h, "dotfiles2"), 0o755)
	touch(t, filepath.Join(h, "dotfiles", "npmrc"))
	touch(t, filepath.Join(h, "dotfiles", "config", "x"))
	touch(t, filepath.Join(h, "dotfiles2", "plain"))
	symlink(t, filepath.Join(h, "dotfiles", "npmrc"), filepath.Join(h, ".npmrc"))
	symlink(t, filepath.Join(h, "dotfiles", "config"), filepath.Join(h, ".config"))

	r := NewResolver(g)
	for name, p := range map[string]string{
		"folder holding both":  filepath.Join(h, "dotfiles"),
		"the relocated file":   filepath.Join(h, "dotfiles", "npmrc"),
		"the relocated folder": filepath.Join(h, "dotfiles", "config"),
		"inside it":            filepath.Join(h, "dotfiles", "config", "x"),
	} {
		if real, v := r.Resolve(p); v != Blocked {
			t.Errorf("%s: Resolve(%q) = %v (%q), want Blocked", name, p, v, real)
		}
	}
	if _, v := r.Resolve(filepath.Join(h, "dotfiles2", "plain")); v != OK {
		t.Errorf("unrelated sibling: %v", v)
	}
}

func TestResolveRefusesFolderHoldingMovedLibrary(t *testing.T) {
	testenv.MacPaths(t)
	base, _ := filepath.EvalSymlinks(t.TempDir())
	h := filepath.Join(base, "home", "alice")
	vol := filepath.Join(base, "vol")
	os.MkdirAll(h, 0o755)
	os.MkdirAll(filepath.Join(vol, "Moved", "Library", "Caches"), 0o755)
	touch(t, filepath.Join(vol, "Moved", "Library", "Caches", "f"))
	touch(t, filepath.Join(vol, "Moved", "other"))
	touch(t, filepath.Join(vol, "Elsewhere"))
	symlink(t, filepath.Join(vol, "Moved", "Library"), filepath.Join(h, "Library"))

	g := NewGuard(h, []string{vol})
	g.SystemRoots = nil
	r := NewResolver(g)
	for name, p := range map[string]string{
		"folder holding the real Library": filepath.Join(vol, "Moved"),
		"the real Library":                filepath.Join(vol, "Moved", "Library"),
		"inside the real Library":         filepath.Join(vol, "Moved", "Library", "Caches", "f"),
	} {
		if real, v := r.Resolve(p); v != Blocked {
			t.Errorf("%s: Resolve(%q) = %v (%q), want Blocked", name, p, v, real)
		}
	}
	if _, v := r.Resolve(filepath.Join(vol, "Elsewhere")); v != OK {
		t.Errorf("unrelated file on the volume: %v", v)
	}
}

func TestResolveRefusesOtherDevices(t *testing.T) {
	g, h, _ := realTree(t)
	drive := filepath.Join(filepath.Dir(h), "drive")
	if err := os.MkdirAll(filepath.Join(drive, "Archive"), 0o755); err != nil {
		t.Fatal(err)
	}
	g.Drives = []string{drive}
	home := filepath.Join(h, "Downloads", "a.zip")
	onDrive := filepath.Join(drive, "Archive", "b.zip")
	touch(t, home)
	touch(t, onDrive)

	r := NewResolver(g)
	for _, p := range []string{home, onDrive} {
		if _, v := r.Resolve(p); v != OK {
			t.Fatalf("%s: verdict %v, want OK on its own device", p, v)
		}
	}
	// pretend home is another volume than the file's: a mount inside home
	r.homeDev++
	if _, v := r.Resolve(home); v != Blocked {
		t.Errorf("file on another device than home: verdict %v, want Blocked", v)
	}
	r.homeDev--
	r.drives[0].dev++
	if _, v := r.Resolve(onDrive); v != Blocked {
		t.Errorf("file on another device than its drive: verdict %v, want Blocked", v)
	}
	// unknown devices are refused
	r.drives[0].dev = 0
	if _, v := r.Resolve(onDrive); v != Blocked {
		t.Errorf("unknown drive device: verdict %v, want Blocked", v)
	}
}

func TestResolveRefusesSyncthingFolders(t *testing.T) {
	g, h, _ := realTree(t)
	for _, d := range []string{"Work/shared/.stfolder", "Work/plain"} {
		if err := os.MkdirAll(filepath.Join(h, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	touch(t, filepath.Join(h, "Work", "shared", "a.txt"))
	touch(t, filepath.Join(h, "Work", "plain", "a.txt"))
	r := NewResolver(g)
	tests := []struct {
		name string
		path string
		want Verdict
	}{
		{"file in a folder with .stfolder", filepath.Join(h, "Work", "shared", "a.txt"), Blocked},
		{"the folder with .stfolder", filepath.Join(h, "Work", "shared"), Blocked},
		{"file in an ordinary folder", filepath.Join(h, "Work", "plain", "a.txt"), OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, v := r.Resolve(tt.path); v != tt.want {
				t.Errorf("verdict %v, want %v", v, tt.want)
			}
		})
	}
}

func TestIsPackageName(t *testing.T) {
	tests := map[string]bool{
		"a.sparsebundle": true, "a.sparseimage": true, "Movie.fcpbundle": true, "Lib.imovielibrary": true, "Song.logicx": true,
		"Photos Library.photoslibrary": true, "Music.musiclibrary": true, "TV.tvlibrary": true, "vm.pvm": true, "vm.utm": true,
		"vm.vmwarevm": true, "Foo.APP": true, "x.bundle": true, "x.framework": true, "x.xcarchive": true,
		"Novel.scriv": true, "db.dtBase2": true, "db.dtSparse": true, "c.lrcat-data": true, "c.cocatalog": true, "c.cocatalogdb": true,
		"Old.migratedphotolibrary": true, "x.sketch": true, "p.playground": true, "x.nib": true, "x.prefPane": true, "x.qlgenerator": true,
		"x.mdimporter": true, "x.saver": true, "x.wdgt": true, "proj.git": true, "Proj.GIT": true, "git": false, "digit": false,
		"image.dmg": false, "movie.mkv": false, "app": false, "Projects": false,
	}
	for name, want := range tests {
		if got := IsPackageName(name); got != want {
			t.Errorf("IsPackageName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestSafeCloudFolderNamesAtAnyDepth(t *testing.T) {
	testenv.MacPaths(t)
	g := guard()
	for _, n := range []string{
		"Creative Cloud Files", "Creative Cloud Files (Work)", "Creative Cloud Files Individual", "Resilio Sync", "resilio sync",
		"Seafile", "Seafile Shared", "Tresorit", "Tresorit Drive", "Sync.com", "SYNC.COM", "SugarSync", "SugarSync Folder",
		"Egnyte", "Egnyte Drive", "ShareFile", "ShareFile Drive",
	} {
		for _, p := range []string{home + "/" + n + "/a.txt", home + "/Work/" + n + "/a.txt", home + "/Work/" + n} {
			if g.Safe(p) {
				t.Errorf("Safe(%q) = true, want false", p)
			}
		}
	}
	for _, p := range []string{home + "/Work/Seafoam/a.txt", home + "/Work/Resilio/a.txt", home + "/Work/Syncs/a.txt"} {
		if !g.Safe(p) {
			t.Errorf("Safe(%q) = false, want true", p)
		}
	}
}

// Nothing inside a package or git store is removed on its own, however the
// path is spelled; the package as a whole may go.
func TestResolveRefusesPathsInsidePackages(t *testing.T) {
	testenv.MacPaths(t)
	g, h, _ := realTree(t)
	for _, f := range []string{
		"Code/App.app/Contents/f.bin", "Code/proj.git/objects/pack/p.pack", "Code/repo/.git/config",
		"Code/Custom.thing/Contents/Info.plist", "Code/Custom.thing/Contents/Resources/f.bin",
		"Code/mirror/HEAD", "Code/mirror/objects/pack/p.pack", "Code/Novel.scriv/Files/a.rtf",
		"Code/Deep/Lib.PHOTOSLIBRARY/a/b/c.heic", "Code/plain/a.txt", "Code/notrepo/HEAD",
	} {
		touch0(t, filepath.Join(h, f))
	}
	if err := os.MkdirAll(filepath.Join(h, "Code", "mirror", "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(h, "Code", "App.app"), filepath.Join(h, "Code", "plain", "applink"))
	r := NewResolver(g)
	tests := []struct {
		name string
		path string
		want Verdict
	}{
		{"file in an app", filepath.Join(h, "Code", "App.app", "Contents", "f.bin"), Blocked},
		{"folder in an app", filepath.Join(h, "Code", "App.app", "Contents"), Blocked},
		{"file in a bare repo by suffix", filepath.Join(h, "Code", "proj.git", "objects", "pack", "p.pack"), Blocked},
		{"file in .git", filepath.Join(h, "Code", "repo", ".git", "config"), Blocked},
		{"file in a custom bundle", filepath.Join(h, "Code", "Custom.thing", "Contents", "Resources", "f.bin"), Blocked},
		{"file in a bare repo by shape", filepath.Join(h, "Code", "mirror", "objects", "pack", "p.pack"), Blocked},
		{"file in a Scrivener project", filepath.Join(h, "Code", "Novel.scriv", "Files", "a.rtf"), Blocked},
		{"file deep in a Photos library", filepath.Join(h, "Code", "Deep", "Lib.PHOTOSLIBRARY", "a", "b", "c.heic"), Blocked},
		{"package missing inside", filepath.Join(h, "Code", "App.app", "Contents", "gone"), Blocked},
		{"through a link into an app", filepath.Join(h, "Code", "plain", "applink", "Contents", "f.bin"), Blocked},
		{"the app itself", filepath.Join(h, "Code", "App.app"), OK},
		{"the bare repo itself", filepath.Join(h, "Code", "proj.git"), OK},
		{"the custom bundle itself", filepath.Join(h, "Code", "Custom.thing"), OK},
		{"folder holding packages", filepath.Join(h, "Code"), OK},
		{"plain file", filepath.Join(h, "Code", "plain", "a.txt"), OK},
		{"folder with HEAD but no objects or refs", filepath.Join(h, "Code", "notrepo"), OK},
		{"file in it", filepath.Join(h, "Code", "notrepo", "HEAD"), OK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if real, v := r.Resolve(tt.path); v != tt.want {
				t.Errorf("Resolve(%q) = %v (%q), want %v", tt.path, v, real, tt.want)
			}
		})
	}
}

func touch0(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, p)
}

// A bare repository needs all three parts: the objects folder is made too.
func TestResolveBareRepoShapeNeedsObjectsAndRefs(t *testing.T) {
	g, h, _ := realTree(t)
	if err := os.MkdirAll(filepath.Join(h, "Code", "mirror", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	touch0(t, filepath.Join(h, "Code", "mirror", "HEAD"))
	touch0(t, filepath.Join(h, "Code", "mirror", "x.bin"))
	r := NewResolver(g)
	if _, v := r.Resolve(filepath.Join(h, "Code", "mirror", "x.bin")); v != OK {
		t.Fatalf("without refs/ it is not a repository: verdict %v", v)
	}
	if err := os.MkdirAll(filepath.Join(h, "Code", "mirror", "refs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, v := r.Resolve(filepath.Join(h, "Code", "mirror", "x.bin")); v != Blocked {
		t.Fatalf("with HEAD, objects/ and refs/: verdict %v, want Blocked", v)
	}
}

func TestResolveSyncMarkersOfAnyType(t *testing.T) {
	testenv.MacPaths(t)
	g, h, outside := realTree(t)
	for _, d := range []string{"Work/file-marker", "Work/link-marker", "Work/broken-marker", "Work/resilio", "Work/resilio-file", "Work/plain"} {
		touch0(t, filepath.Join(h, d, "a.txt"))
	}
	touch(t, filepath.Join(h, "Work", "file-marker", ".stfolder"))
	symlink(t, outside, filepath.Join(h, "Work", "link-marker", ".stfolder"))
	symlink(t, filepath.Join(h, "Work", "nowhere"), filepath.Join(h, "Work", "broken-marker", ".stfolder"))
	if err := os.MkdirAll(filepath.Join(h, "Work", "resilio", ".sync"), 0o755); err != nil {
		t.Fatal(err)
	}
	touch(t, filepath.Join(h, "Work", "resilio-file", ".sync"))
	r := NewResolver(g)
	for _, d := range []string{"file-marker", "link-marker", "broken-marker", "resilio", "resilio-file"} {
		if _, v := r.Resolve(filepath.Join(h, "Work", d, "a.txt")); v != Blocked {
			t.Errorf("%s: verdict %v, want Blocked", d, v)
		}
	}
	if _, v := r.Resolve(filepath.Join(h, "Work", "plain", "a.txt")); v != OK {
		t.Errorf("plain: verdict %v, want OK", v)
	}
}

// An installer named like a sync service is a file, not a sync folder; a
// folder with the same name stays refused, and so does everything below one.
func TestResolveCloudNamesApplyToFoldersNotFiles(t *testing.T) {
	g, h, _ := realTree(t)
	for _, f := range []string{
		"Downloads/Tresorit.dmg", "Downloads/Resilio Sync.dmg", "Downloads/SugarSync Setup.pkg", "Downloads/Dropbox.dmg",
		"Downloads/OneDrive.pkg", "Downloads/Plain.dmg",
		"Downloads/Tresorit/inside.txt", "Work/Egnyte Drive/inside.txt",
	} {
		touch0(t, filepath.Join(h, f))
	}
	if err := os.MkdirAll(filepath.Join(h, "Downloads", "Seafile Shared", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := NewResolver(g)
	tests := []struct {
		name string
		path string
		want Verdict
	}{
		{"Tresorit installer", filepath.Join(h, "Downloads", "Tresorit.dmg"), OK},
		{"Resilio Sync installer", filepath.Join(h, "Downloads", "Resilio Sync.dmg"), OK},
		{"SugarSync installer", filepath.Join(h, "Downloads", "SugarSync Setup.pkg"), OK},
		{"Dropbox installer", filepath.Join(h, "Downloads", "Dropbox.dmg"), OK},
		{"OneDrive installer", filepath.Join(h, "Downloads", "OneDrive.pkg"), OK},
		{"plain installer", filepath.Join(h, "Downloads", "Plain.dmg"), OK},
		{"cloud-named folder itself", filepath.Join(h, "Downloads", "Tresorit"), Blocked},
		{"cloud-named empty folder tree", filepath.Join(h, "Downloads", "Seafile Shared"), Blocked},
		{"file in a cloud-named folder", filepath.Join(h, "Downloads", "Tresorit", "inside.txt"), Blocked},
		{"file in a nested cloud-named folder", filepath.Join(h, "Work", "Egnyte Drive", "inside.txt"), Blocked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if real, v := r.Resolve(tt.path); v != tt.want {
				t.Errorf("Resolve(%q) = %v (%q), want %v", tt.path, v, real, tt.want)
			}
		})
	}
	// the lexical check alone cannot see a file, so it still refuses a name it cannot examine
	if g.Safe(filepath.Join(h, "Downloads", "Tresorit")) {
		t.Error("lexical Safe: a real cloud-named folder must be refused")
	}
	if g.Safe(home+"/Downloads/Tresorit.dmg") || g.Safe("/Volumes/Data/Seafile.dmg") {
		t.Error("a cloud-named path that cannot be examined must stay refused")
	}
}

// A bare repository whose HEAD is a link is still a repository; the new
// package types are never entered.
func TestResolveMorePackageShapes(t *testing.T) {
	testenv.MacPaths(t)
	g, h, _ := realTree(t)
	for _, d := range []string{"Code/linkrepo/objects", "Code/linkrepo/refs"} {
		if err := os.MkdirAll(filepath.Join(h, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	touch0(t, filepath.Join(h, "Code", "elsewhere", "ref"))
	symlink(t, filepath.Join(h, "Code", "elsewhere", "ref"), filepath.Join(h, "Code", "linkrepo", "HEAD"))
	touch0(t, filepath.Join(h, "Code", "linkrepo", "x.bin"))
	for _, ext := range []string{"mlpackage", "xcframework", "textbundle", "avd", "graffle", "abbu", "hdd", "MLPACKAGE"} {
		touch0(t, filepath.Join(h, "Code", "Thing."+ext, "inner", "f.bin"))
		if !IsPackageName("Thing." + ext) {
			t.Errorf("IsPackageName(Thing.%s) = false", ext)
		}
	}
	r := NewResolver(g)
	if _, v := r.Resolve(filepath.Join(h, "Code", "linkrepo", "x.bin")); v != Blocked {
		t.Errorf("file in a bare repo with a HEAD link: verdict %v, want Blocked", v)
	}
	for _, ext := range []string{"mlpackage", "xcframework", "textbundle", "avd", "graffle", "abbu", "hdd"} {
		if _, v := r.Resolve(filepath.Join(h, "Code", "Thing."+ext, "inner", "f.bin")); v != Blocked {
			t.Errorf("file in .%s: verdict %v, want Blocked", ext, v)
		}
		if _, v := r.Resolve(filepath.Join(h, "Code", "Thing."+ext)); v != OK {
			t.Errorf(".%s itself: verdict %v, want OK", ext, v)
		}
	}
}
