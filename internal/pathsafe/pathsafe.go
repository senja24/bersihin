// Package pathsafe decides which paths the app may move to the Trash or
// delete. It is platform neutral: the macOS and Windows rules are data (see
// NewGuard and NewWindowsGuard), so the table tests run on any OS; only the
// path syntax (separator, drive letters) is the running OS's.
//
// The check is two-staged, like the reference app's: a lexical guard on the
// path as given, then the same guard again on the canonical path (realpath),
// so a symlink in a parent folder cannot lead out of the allowed area.
package pathsafe

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"dev.agus.bersihin/internal/walk"
	"golang.org/x/text/cases"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

const maxPathLen = 4096

// Guard holds the rules. The zero value allows nothing.
type Guard struct {
	// Home is the user's home folder. Everything allowed lives below it, or
	// on one of Drives.
	Home string
	// Known are the user's own top folders (Desktop, Documents…): neither they
	// nor anything that contains them may be removed on a data drive, and
	// they are never removed themselves.
	Known []string
	// Drives are the mounts of fixed data volumes other than the system one.
	Drives []string
	// SystemRoots are folders nothing below Home may live in (/System, /usr…).
	SystemRoots []string
	// FoldCase compares paths case-insensitively, as the default macOS
	// file systems and NTFS do.
	FoldCase bool
	// Windows applies the rules of a Windows profile (see winRules) instead
	// of a Mac home folder's.
	Windows bool
	// Protected are folders that are never removed, nor anything in them,
	// nor a folder holding one, wherever they are (on Windows: the system
	// folders, AppData\...\Microsoft and the OneDrive folders).
	Protected []string
}

// macSystemRoots are the OS folders of a Mac.
var macSystemRoots = []string{
	"/System", "/Library", "/Applications", "/usr", "/bin", "/sbin", "/etc", "/var", "/private", "/opt", "/cores", "/dev", "/Volumes",
}

// protectedTop are the folders directly in the home folder that must never be
// removed as a whole.
var protectedTop = []string{
	"Desktop", "Documents", "Downloads", "Pictures", "Movies", "Music", "Library", "Applications", "Public",
	"iCloud Drive", "iCloudDrive", "Dropbox", "Google Drive", "Creative Cloud Files", ".Trash", ".local", ".config",
}

// secretDirs are places inside home that hold keys and credentials: nothing
// in or under them is ever touched.
var secretDirs = [][]string{
	{".ssh"}, {".gnupg"}, {"Library", "Keychains"},
	{".aws"}, {".kube"}, {".docker"}, {".netrc"}, {".password-store"},
	{".npmrc"}, {".yarnrc"}, {".yarnrc.yml"}, {".pgpass"}, {".pypirc"}, {".git-credentials"},
	{".azure"}, {".terraform.d"},
	// the whole of ~/.config: tokens and settings of many tools live there
	{".config"}, {".config", "gcloud"}, {".config", "gh"},
	{"Library", "Group Containers"}, {"Library", "Messages"}, {"Library", "Mail"},
	{"Library", "Application Support", "1Password"}, {"Library", "Application Support", "Bitwarden"},
	{"Library", "Application Support", "Keeper"},
	// AI tool, tunnel and store credentials
	{".claude"}, {".codex"}, {".cloudflared"}, {".mcp-auth"}, {".appstoreconnect"}, {".ollama"},
	{".local", "share", "keyrings"}, {".mozilla"}, {".thunderbird"},
	{".gem", "credentials"}, {".m2", "settings.xml"}, {".gradle", "gradle.properties"},
	{".vault-token"}, {".boto"}, {".s3cfg"},
}

// secretGlobs are secret files whose name only starts with a fixed prefix
// ({dir, name prefix}): ~/.cargo/credentials and ~/.cargo/credentials.toml.
var secretGlobs = [][2]string{{".cargo", "credentials"}}

// cloudDirs are sync areas inside home: nothing in or under them is touched,
// because removing a file there removes it from the cloud too.
var cloudDirs = [][]string{
	{"Library", "Mobile Documents"}, {"Library", "CloudStorage"},
}

// driveProtectedTop are the top-level entries of a data volume that belong to
// the OS.
var driveProtectedTop = []string{
	"System", "Library", "Applications", "Users", "private", "Backups.backupdb", "cores", "usr", "bin", "sbin", "etc", "var", "opt",
}

// rules are the names a guard protects below the home folder and on a data
// volume.
type rules struct {
	// protectedTop: entries directly in home never removed as a whole.
	protectedTop []string
	// denyTop: entries directly in home nothing in or under is removed.
	denyTop []string
	// topPrefixes: names directly in home starting with these are kept.
	topPrefixes []string
	secretDirs  [][]string
	secretGlobs [][2]string
	cloudDirs   [][]string
	// driveProtectedTop: top-level entries of a data volume that belong to the OS.
	driveProtectedTop []string
	// drivePrefixes: top-level names of a data volume starting with these
	// belong to the OS ("." always does).
	drivePrefixes []string
}

var macRules = rules{
	protectedTop:      protectedTop,
	denyTop:           []string{"Library"},
	secretDirs:        secretDirs,
	secretGlobs:       secretGlobs,
	cloudDirs:         cloudDirs,
	driveProtectedTop: driveProtectedTop,
}

func (g Guard) rules() *rules {
	if g.Windows {
		return &winRules
	}
	return &macRules
}

var cloudTop = regexp.MustCompile(`(?i)^(OneDrive( - .+)?|(Dropbox|Nextcloud|ownCloud|MEGA)( \(.+\))?|Google Drive|My Drive|iCloud ?Drive|Yandex\.?Disk|Box|Box Sync|pCloud ?Drive|Synology ?Drive|(Creative Cloud Files|Resilio Sync|Seafile|Tresorit|Sync\.com|SugarSync|Egnyte|ShareFile).*)$`)

// packageExt matches the names of macOS packages and bundles: folders that
// are one thing to the user (a Photos library, a VM, an app, a disk image
// band store). Removing a file inside one breaks it.
var packageExt = regexp.MustCompile(`(?i)\.(app|photoslibrary|photolibrary|musiclibrary|tvlibrary|imovielibrary|fcpbundle|logicx|band|aplibrary|lrdata|lrlibrary|bundle|framework|plugin|kext|xcodeproj|xcworkspace|xcarchive|pkg|mpkg|sparsebundle|sparseimage|rtfd|pages|numbers|key|vmwarevm|pvm|utm|scriv|dtBase2|dtSparse|lrcat-data|cocatalog|cocatalogdb|migratedphotolibrary|sketch|playground|nib|prefPane|qlgenerator|mdimporter|saver|wdgt|git|mlpackage|xcframework|textbundle|avd|graffle|abbu|hdd)$`)

// IsPackageName reports whether a folder name is that of a package or bundle
// (or of a bare git store, which ends in .git).
func IsPackageName(name string) bool { return packageExt.MatchString(name) }

// IsPackageDir reports whether the folder at p is a package: by its name, by
// a Contents/Info.plist (a custom bundle type) or because it is a bare git
// repository of any name (HEAD file plus objects and refs folders). It costs
// a few stats at most.
func IsPackageDir(p string) bool {
	if IsPackageName(filepath.Base(p)) {
		return true
	}
	if fi, err := os.Stat(filepath.Join(p, "Contents", "Info.plist")); err == nil && fi.Mode().IsRegular() {
		return true
	}
	// HEAD is a file, or a link (a worktree or a shared store)
	if fi, err := os.Lstat(filepath.Join(p, "HEAD")); err != nil || !(fi.Mode().IsRegular() || fi.Mode()&fs.ModeSymlink != 0) {
		return false
	}
	for _, d := range []string{"objects", "refs"} {
		if fi, err := os.Stat(filepath.Join(p, d)); err != nil || !fi.IsDir() {
			return false
		}
	}
	return true
}

// inPackage reports whether a folder above abs is a package or a git store.
// abs itself may be one: a package is removed as a whole, never from inside.
// The macOS package names are refused on Windows too: a folder called
// "x.app" there is rare, and leaving it alone costs nothing.
func (g Guard) inPackage(abs string) bool {
	for a := filepath.Dir(abs); ; a = filepath.Dir(a) {
		if IsPackageName(g.fold(filepath.Base(a))) || IsPackageDir(a) {
			return true
		}
		if filepath.Dir(a) == a {
			return false
		}
	}
}

// KnownFolders returns the usual user folders below home.
func KnownFolders(home string) []string {
	var out []string
	for _, n := range []string{"Desktop", "Documents", "Downloads", "Pictures", "Movies", "Music"} {
		out = append(out, filepath.Join(home, n))
	}
	return out
}

// NewGuard returns the macOS rules for home and the given data volumes.
func NewGuard(home string, drives []string) Guard {
	return Guard{
		Home:        home,
		Known:       KnownFolders(home),
		Drives:      drives,
		SystemRoots: macSystemRoots,
		FoldCase:    true,
	}
}

// hfsIgnorable are the code points HFS+ (and so some APFS spellings) ignore in
// names, which let "Docu\u200Cments" name "Documents".
var hfsIgnorable = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x200C, Hi: 0x200F, Stride: 1},
		{Lo: 0x202A, Hi: 0x202E, Stride: 1},
		{Lo: 0x206A, Hi: 0x206F, Stride: 1},
		{Lo: 0xFEFF, Hi: 0xFEFF, Stride: 1},
	},
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// fold turns a name into the form the file system compares: decomposed
// (NFD), without ignorable code points and, when FoldCase is set, with full
// Unicode case folding ("Documen\u017Fts" and the Kelvin sign included).
func (g Guard) fold(s string) string {
	if !g.FoldCase {
		return s
	}
	if isASCII(s) {
		return strings.ToLower(s)
	}
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(hfsIgnorable)), cases.Fold(), norm.NFD)
	out, _, err := transform.String(t, s)
	if err != nil {
		return strings.ToLower(s)
	}
	return out
}

func (g Guard) norm(p string) string { return g.fold(filepath.Clean(p)) }

// Same reports whether a and b are the same path, lexically.
func (g Guard) Same(a, b string) bool { return g.norm(a) == g.norm(b) }

// Inside reports whether child is strictly below parent, lexically.
func (g Guard) Inside(parent, child string) bool { return inside(g.norm(parent), g.norm(child)) }

func inside(parent, child string) bool {
	// a folded ".." must never be read as a step up
	for _, p := range []string{parent, child} {
		for seg := range strings.SplitSeq(p, string(filepath.Separator)) {
			if seg == ".." {
				return false
			}
		}
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil || rel == "." || rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func (g Guard) eq(a, b string) bool { return g.fold(a) == g.fold(b) }

func (g Guard) inList(list []string, name string) bool {
	for _, n := range list {
		if g.eq(n, name) {
			return true
		}
	}
	return false
}

// foldsAway reports whether a segment turns into "", "." or ".." once the
// file system's folding (ignorable code points removed) is applied: "..\u200C"
// is such a name, and a folded comparison would treat it as a parent step.
func foldsAway(seg string) bool {
	if seg == "" || seg == "." || isASCII(seg) {
		return false
	}
	g := Guard{FoldCase: true}
	switch g.fold(seg) {
	case "", ".", "..":
		return true
	}
	return false
}

// Clean validates the shape of p like the guard does (absolute, no NUL, no
// ".." and no segment that folds away) and returns it cleaned.
func Clean(p string) (string, bool) { return clean(p) }

// clean validates the shape of p and returns it cleaned. A ".." segment is
// refused outright: the kernel resolves it after following links, a lexical
// Clean before, and the two can name different files.
func clean(p string) (string, bool) {
	if p == "" || len(p) > maxPathLen || strings.ContainsRune(p, 0) || !filepath.IsAbs(p) {
		return "", false
	}
	if filepath.Separator == '\\' && !WindowsShapeOK(p) {
		return "", false
	}
	for seg := range strings.SplitSeq(p, string(filepath.Separator)) {
		if seg == ".." || foldsAway(seg) {
			return "", false
		}
	}
	abs := filepath.Clean(p)
	// "/" or a bare drive (C:\)
	if abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return "", false
	}
	return abs, true
}

// segments returns the (folded) path elements of abs below base (abs must be inside base).
func (g Guard) segments(base, abs string) []string {
	rel, err := filepath.Rel(g.norm(base), g.norm(abs))
	if err != nil {
		return nil
	}
	return strings.Split(rel, string(filepath.Separator))
}

func (g Guard) hasPrefixSegs(parts, prefix []string) bool {
	if len(parts) < len(prefix) {
		return false
	}
	for i, p := range prefix {
		if !g.eq(parts[i], p) {
			return false
		}
	}
	return true
}

// Safe reports whether p, as written, may be moved to the Trash: strictly
// inside the home folder and clear of OS folders, the user's top folders,
// cloud and key stores, or strictly inside a data volume outside its OS
// folders. It looks at the file system only to tell a file named like a sync
// folder from the folder itself; use a Resolver for the rest.
func (g Guard) Safe(p string) bool {
	abs, ok := clean(p)
	if !ok {
		return false
	}
	for _, prot := range g.Protected {
		if g.Same(abs, prot) || g.Inside(prot, abs) || g.Inside(abs, prot) {
			return false
		}
	}
	if g.safeHome(abs) {
		return true
	}
	for _, m := range g.Drives {
		if g.safeDrive(abs, m) {
			return true
		}
	}
	return false
}

func (g Guard) safeHome(abs string) bool {
	home, ok := clean(g.Home)
	if !ok || !g.Inside(home, abs) {
		return false
	}
	for _, sys := range g.SystemRoots {
		if g.Same(abs, sys) || g.Inside(sys, abs) {
			return false
		}
	}
	parts := g.segments(home, abs)
	rl := g.rules()
	if len(parts) == 1 && g.inList(rl.protectedTop, parts[0]) {
		return false
	}
	if len(parts) == 1 {
		for _, pre := range rl.topPrefixes {
			if strings.HasPrefix(g.fold(parts[0]), g.fold(pre)) {
				return false
			}
		}
	}
	// A sync folder is protected with everything below it, at any depth.
	if g.cloudAnywhere(parts, abs) || g.eq(parts[0], "Sync") {
		return false
	}
	// ~/Library (on Windows AppData and the legacy profile links) is deny
	// by default: app data, containers, VM images and browser profiles live
	// there. Junk cleaning does not go through this guard (it deletes inside
	// its own roots), and apps are removed through AppBundle.
	if g.inList(rl.denyTop, parts[0]) {
		return false
	}
	if g.secretGlob(parts) {
		return false
	}
	for _, s := range rl.secretDirs {
		if g.hasPrefixSegs(parts, s) {
			return false
		}
	}
	for _, c := range rl.cloudDirs {
		if g.hasPrefixSegs(parts, c) {
			return false
		}
	}
	if g.secretAnywhere(parts) {
		return false
	}
	// neither a user folder nor a folder holding one (Documents redirected into ~/Work)
	for _, k := range g.Known {
		if g.Same(abs, k) || g.Inside(abs, k) {
			return false
		}
	}
	return true
}

// secretGlob matches the secretGlobs entries directly below home.
func (g Guard) secretGlob(parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	for _, sg := range g.rules().secretGlobs {
		if g.eq(parts[0], sg[0]) && strings.HasPrefix(g.fold(parts[1]), g.fold(sg[1])) {
			return true
		}
	}
	return false
}

// secretAnywhere refuses key stores at any depth (a project's own .ssh folder
// is not worth the risk).
func (g Guard) secretAnywhere(parts []string) bool {
	for i, s := range parts {
		if g.eq(s, ".ssh") || g.eq(s, ".gnupg") || strings.HasSuffix(g.fold(s), ".photoslibrary") {
			return true
		}
		if i > 0 && g.eq(s, "Keychains") && g.eq(parts[i-1], "Library") {
			return true
		}
		// the AppData of a profile anywhere (an old Windows copied to a data
		// drive): its Microsoft folders hold credentials and keys
		if g.Windows && g.eq(s, "AppData") {
			return true
		}
	}
	return false
}

func (g Guard) safeDrive(abs, mount string) bool {
	home, homeOK := clean(g.Home)
	mount, ok := clean(mount)
	if !ok || !homeOK {
		return false
	}
	// A volume that holds (or is) the home folder is the system volume.
	if g.Same(home, mount) || g.Inside(mount, home) || g.Inside(home, mount) {
		return false
	}
	if !g.Inside(mount, abs) {
		return false
	}
	parts := g.segments(mount, abs)
	top := parts[0]
	rl := g.rules()
	// .Trashes, .Spotlight-V100, .fseventsd…
	if strings.HasPrefix(top, ".") || g.inList(rl.driveProtectedTop, top) {
		return false
	}
	// $Recycle.Bin, $WinREAgent…
	for _, pre := range rl.drivePrefixes {
		if strings.HasPrefix(top, pre) {
			return false
		}
	}
	if g.secretAnywhere(parts) {
		return false
	}
	// Documents, Desktop & co. may be redirected to a data volume: never
	// remove them or a folder holding them.
	for _, k := range g.Known {
		if g.Same(abs, k) || g.Inside(abs, k) {
			return false
		}
	}
	if g.cloudAnywhere(parts, abs) {
		return false
	}
	return true
}

// cloudAnywhere matches a sync folder name at any depth ("Work/Dropbox/a").
// The last element is looked at only when it is not a regular file: an
// installer called "Tresorit.dmg" is a file, a folder called "Tresorit" is a
// sync folder. A last element that cannot be examined counts as a folder.
func (g Guard) cloudAnywhere(parts []string, abs string) bool {
	for i, s := range parts {
		if !cloudTop.MatchString(s) {
			continue
		}
		if i == len(parts)-1 {
			if fi, err := os.Lstat(abs); err == nil && fi.Mode().IsRegular() {
				continue
			}
		}
		return true
	}
	return false
}

// Verdict is the outcome of Resolver.Resolve.
type Verdict int

// The verdicts.
const (
	// Blocked: the path is not allowed, or could not be checked.
	Blocked Verdict = iota
	// OK: the path may be moved to the Trash.
	OK
	// Gone: nothing exists at the path any more.
	Gone
)

// Resolver runs the guard on the canonical path as well.
type Resolver struct {
	lexical Guard
	real    Guard
	// Identities of folders that must never be removed (exact) and of
	// folders nothing below may be removed (subtree), read from disk, so a
	// spelling the name checks miss still meets them by inode.
	exact   []os.FileInfo
	subtree []os.FileInfo
	// Real locations of protected items that were moved elsewhere and linked
	// back (~/.config -> ~/dotfiles/config): neither they nor any folder
	// holding them may be removed.
	relocated []string
	// Devices of the real home and of each real data volume; a path on any
	// other device (a network or FUSE mount) is refused.
	homeDev uint64
	drives  []driveDev
}

type driveDev struct {
	mount string
	dev   uint64
}

// NewResolver prepares g. Home, the known folders and the volumes are
// resolved once here; a volume that is not its own real path (a link) is
// dropped.
func NewResolver(g Guard) *Resolver {
	r := &Resolver{}
	var drives []string
	var realDrives []string
	for _, m := range g.Drives {
		if rm, err := filepath.EvalSymlinks(m); err == nil && g.Same(rm, m) {
			drives = append(drives, m)
			realDrives = append(realDrives, rm)
		}
	}
	lex := g
	lex.Drives = drives
	rg := g
	rg.Home = realOr(g.Home)
	rg.Known = nil
	for _, k := range g.Known {
		rg.Known = append(rg.Known, realOr(k))
	}
	rg.Drives = realDrives
	rg.Protected = nil
	for _, p := range g.Protected {
		rg.Protected = append(rg.Protected, realOr(p))
	}
	r.lexical, r.real = lex, rg
	r.exact, r.subtree = identities(rg)
	r.relocated = relocatedLocations(rg)
	r.homeDev = devOf(rg.Home)
	for _, m := range realDrives {
		r.drives = append(r.drives, driveDev{m, devOf(m)})
	}
	return r
}

// devOf returns the device of p, 0 when unknown.
func devOf(p string) uint64 {
	if fi, err := walk.Lstat(p); err == nil {
		return walk.Dev(fi)
	}
	return 0
}

// onLocalDevice reports whether real lives on the device of the home folder
// (when below it) or of the data volume holding it. An unknown device is
// refused.
func (r *Resolver) onLocalDevice(real string) bool {
	dev := devOf(real)
	if dev == 0 {
		return false
	}
	if home, ok := clean(r.real.Home); ok && r.real.Inside(home, real) {
		return r.homeDev != 0 && dev == r.homeDev
	}
	for _, d := range r.drives {
		if r.real.Inside(d.mount, real) {
			return d.dev != 0 && dev == d.dev
		}
	}
	return false
}

// syncMarkers are the entries that mark a folder as shared by Syncthing
// (.stfolder) or Resilio Sync (.sync). Their type does not matter, and they
// are looked at, never followed.
var syncMarkers = []string{".stfolder", ".sync"}

// inSyncthingFolder reports whether real, or a folder above it, holds a sync
// marker.
func inSyncthingFolder(real string) bool {
	for a := real; ; a = filepath.Dir(a) {
		for _, m := range syncMarkers {
			if _, err := os.Lstat(filepath.Join(a, m)); err == nil {
				return true
			}
		}
		if filepath.Dir(a) == a {
			return false
		}
	}
}

func statAll(paths []string) []os.FileInfo {
	var out []os.FileInfo
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil {
			out = append(out, fi)
		}
	}
	return out
}

func identities(g Guard) (exact, subtree []os.FileInfo) {
	home, ok := clean(g.Home)
	if !ok {
		return nil, nil
	}
	rl := g.rules()
	ex := []string{home}
	for _, d := range rl.denyTop {
		ex = append(ex, filepath.Join(home, d))
	}
	ex = append(ex, g.Known...)
	for _, n := range rl.protectedTop {
		ex = append(ex, filepath.Join(home, n))
	}
	for _, d := range rl.denyTop {
		if list, err := os.ReadDir(filepath.Join(home, d)); err == nil {
			for _, e := range list {
				ex = append(ex, filepath.Join(home, d, e.Name()))
			}
		}
	}
	var sub []string
	for _, d := range append(append([][]string{}, rl.secretDirs...), rl.cloudDirs...) {
		sub = append(sub, filepath.Join(append([]string{home}, d...)...))
	}
	// the whole Library (AppData), whatever its spelling
	for _, d := range rl.denyTop {
		sub = append(sub, filepath.Join(home, d))
	}
	sub = append(sub, g.Protected...)
	for _, sg := range rl.secretGlobs {
		if m, err := filepath.Glob(filepath.Join(home, sg[0], sg[1]+"*")); err == nil {
			sub = append(sub, m...)
		}
	}
	return statAll(ex), statAll(sub)
}

// relocatedLocations lists the real paths of protected places below home that
// are reached through a link (their real path differs from the spelled one).
func relocatedLocations(g Guard) []string {
	home, ok := clean(g.Home)
	if !ok {
		return nil
	}
	rl := g.rules()
	var names [][]string
	for _, n := range rl.protectedTop {
		names = append(names, []string{n})
	}
	for _, d := range rl.denyTop {
		names = append(names, []string{d})
	}
	names = append(names, rl.secretDirs...)
	names = append(names, rl.cloudDirs...)
	for _, sg := range rl.secretGlobs {
		if m, err := filepath.Glob(filepath.Join(home, sg[0], sg[1]+"*")); err == nil {
			for _, p := range m {
				names = append(names, []string{strings.TrimPrefix(p, home+string(filepath.Separator))})
			}
		}
	}
	if list, err := os.ReadDir(home); err == nil {
		for _, e := range list {
			if cloudTop.MatchString(e.Name()) {
				names = append(names, []string{e.Name()})
			}
		}
	}
	var out []string
	add := func(p string) {
		if rp, err := filepath.EvalSymlinks(p); err == nil && filepath.Clean(rp) != filepath.Clean(p) {
			out = append(out, rp)
		}
	}
	for _, n := range names {
		add(filepath.Join(append([]string{home}, n...)...))
	}
	for _, k := range g.Known {
		add(k)
	}
	for _, p := range g.Protected {
		add(p)
	}
	return out
}

// protectedByIdentity reports whether real is, or lies below, one of the
// protected folders by inode rather than by name.
func (r *Resolver) protectedByIdentity(real string) bool {
	if fi, err := os.Stat(real); err == nil {
		for _, e := range r.exact {
			if os.SameFile(fi, e) {
				return true
			}
		}
	}
	for a := real; a != "/" && a != "." && a != ""; a = filepath.Dir(a) {
		if fi, err := os.Stat(a); err == nil {
			for _, sdir := range r.subtree {
				if os.SameFile(fi, sdir) {
					return true
				}
			}
		}
		if filepath.Dir(a) == a { // a drive root (C:\) is its own parent
			break
		}
	}
	return false
}

// holdsRelocated reports whether real is, or contains, the real location of
// a protected item.
func (r *Resolver) holdsRelocated(real string) bool {
	for _, p := range r.relocated {
		if r.real.Same(real, p) || r.real.Inside(real, p) {
			return true
		}
	}
	return false
}

func realOr(p string) string {
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		return rp
	}
	return p
}

// Guard returns the lexical rules in use.
func (r *Resolver) Guard() Guard { return r.lexical }

// Resolve returns the canonical path to move to the Trash. Links are never
// accepted: a symlink itself is Blocked, and so is a path whose canonical
// form fails the guard.
func (r *Resolver) Resolve(p string) (string, Verdict) {
	if !r.lexical.Safe(p) || r.lexical.inPackage(filepath.Clean(p)) {
		return "", Blocked
	}
	abs := filepath.Clean(p)
	fi, err := os.Lstat(abs)
	if err != nil {
		return "", missingOrBlocked(err)
	}
	// a link, or a Windows junction or other reparse point (irregular)
	if fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
		return "", Blocked
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", missingOrBlocked(err)
	}
	// EvalSymlinks leaves Windows junctions in place: none may lead there
	if r.real.Windows && reparseBetween(filepath.VolumeName(real)+string(filepath.Separator), real) {
		return "", Blocked
	}
	if !r.real.Safe(real) || r.real.inPackage(real) || r.protectedByIdentity(real) || r.holdsRelocated(real) {
		return "", Blocked
	}
	if !r.onLocalDevice(real) || inSyncthingFolder(real) {
		return "", Blocked
	}
	return real, OK
}

func missingOrBlocked(err error) Verdict {
	if errors.Is(err, fs.ErrNotExist) {
		return Gone
	}
	return Blocked
}

// ContainedIn reports whether target is a real entry strictly inside root: not
// a link, and still inside root once both are resolved. It is the check for
// permanent deletion, which is only ever allowed inside a junk root.
func ContainedIn(root, target string, foldCase bool) bool {
	g := Guard{FoldCase: foldCase}
	rootAbs, ok1 := cleanAbs(root)
	targetAbs, ok2 := cleanAbs(target)
	if !ok1 || !ok2 || !g.Inside(rootAbs, targetAbs) {
		return false
	}
	fi, err := os.Lstat(targetAbs)
	if err != nil || fi.Mode()&fs.ModeSymlink != 0 {
		return false
	}
	realRoot, err1 := filepath.EvalSymlinks(rootAbs)
	realTarget, err2 := filepath.EvalSymlinks(targetAbs)
	if err1 != nil || err2 != nil {
		return false
	}
	if !g.Inside(realRoot, realTarget) {
		return false
	}
	// No link may sit between root and target, even one that stays inside root.
	rel, err := filepath.Rel(g.norm(rootAbs), g.norm(targetAbs))
	if err != nil {
		return false
	}
	if g.norm(realTarget) != g.norm(filepath.Join(realRoot, rel)) {
		return false
	}
	// EvalSymlinks does not resolve a Windows junction: look for one
	return filepath.Separator != '\\' || !reparseBetween(rootAbs, targetAbs)
}

// reparseBetween reports whether p, or a folder above it below top, is a
// link or another reparse point (a junction or mount point reads as
// irregular on Windows), or cannot be examined.
func reparseBetween(top, p string) bool {
	g := Guard{FoldCase: true}
	for a := p; g.Inside(top, a); a = filepath.Dir(a) {
		fi, err := os.Lstat(a)
		if err != nil || fi.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			return true
		}
	}
	return false
}

func cleanAbs(p string) (string, bool) {
	abs, ok := clean(p)
	return abs, ok
}

// AppBundle reports whether p names an application bundle that Bersihin may
// move to the Trash: a real (non-link) *.app below /Applications or
// ~/Applications, not under /System.
func AppBundle(home, p string) bool {
	abs, ok := clean(p)
	if !ok {
		return false
	}
	g := Guard{FoldCase: true}
	if !strings.HasSuffix(strings.ToLower(abs), ".app") {
		return false
	}
	roots := []string{"/Applications", filepath.Join(home, "Applications")}
	in := false
	for _, r := range roots {
		if g.Inside(r, abs) {
			in = true
		}
	}
	if !in || g.Inside("/System", abs) {
		return false
	}
	fi, err := os.Lstat(abs)
	if err != nil || fi.Mode()&fs.ModeSymlink != 0 || !fi.IsDir() {
		return false
	}
	return true
}

// Tilde shows the home folder as ~. The path is cleaned first, and matched
// the way the file system matches names. On Windows it reads ~\x.
func Tilde(home, p string) string {
	if home == "" || !filepath.IsAbs(p) {
		return p
	}
	g := Guard{FoldCase: true}
	p, home = filepath.Clean(p), filepath.Clean(home)
	if g.Same(p, home) {
		return "~"
	}
	if g.Inside(home, p) {
		sep := string(filepath.Separator)
		n := len(strings.Split(home, sep))
		return "~" + sep + strings.Join(strings.Split(p, sep)[n:], sep)
	}
	return p
}

// ExpandTilde turns ~ and ~/x (on Windows also ~\x) into absolute paths
// below home.
func ExpandTilde(home, p string) string {
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, "~"+string(filepath.Separator)) {
		return filepath.Join(home, p[2:])
	}
	return p
}
