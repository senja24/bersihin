// Package spaces builds the space map: the biggest folders of the home folder
// or of a whole volume, two levels deep.
package spaces

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/walk"
)

const (
	maxEntries   = 2_000_000
	maxChildren  = 12
	looseFileMin = 256 * 1024 * 1024
	oldFile      = 90 * 24 * time.Hour
)

// allocatedOf is the size a file counts for: what it takes on disk, so that
// iCloud placeholders count nothing. Tests swap it.
var allocatedOf = func(f walk.File) int64 { return f.Allocated() }

// Path segments that mark regenerable data inside ~/Library (approximate on purpose).
var cacheSegment = regexp.MustCompile(`(?i)(^|/)(cache|caches|code cache|gpucache|cache_data|cache2|cachestorage|scriptcache|shadercache|grshadercache|temp|tmp|crashdumps|logs|npm-cache|deriveddata)(/|$)`)

var (
	codeNames = regexp.MustCompile(`(?i)^(source|src|dev|develop|development|projects?|code|repos?|git|github|workspace|workspaces)$`)
	gameNames = regexp.MustCompile(`(?i)^(games?|steam|steamlibrary|epic games|gog games|riot games|ea games|ubisoft)$`)
)

// Top-level folders of a volume that belong to the OS.
var systemTop = map[string]bool{
	"system": true, "library": true, "private": true, "usr": true, "bin": true, "sbin": true, "cores": true,
	"opt": true, "etc": true, "var": true, "tmp": true,
}

// Never walked: volume metadata and mirrors of other volumes.
var skipTop = map[string]bool{
	".trashes": true, ".spotlight-v100": true, ".fseventsd": true, ".documentrevisions-v100": true, ".temporaryitems": true,
	".vol": true, ".mobilebackups": true, "volumes": true, "dev": true, "home": true, "net": true, "network": true,
}

// DiskSource provides the volumes.
type DiskSource interface {
	System(ctx context.Context) (model.DiskInfo, error)
	List(ctx context.Context) ([]model.DiskInfo, error)
}

// Scanner builds space maps.
type Scanner struct {
	Home  string
	Disks DiskSource
	Now   func() time.Time
}

var errNoDrive = errors.New("That drive is not available")

func (s *Scanner) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

var folded = pathsafe.Guard{FoldCase: true}

// run is the state of one scan: entry budget, step counter and reporter.
type run struct {
	s      *Scanner
	rep    progress.Reporter
	budget int
	done   int
	total  int
	cutoff int64
}

func (r *run) step(phase, current string) { r.rep.Call(phase, r.done, r.total, current) }

func (s *Scanner) iconFor(name string, isDir bool) model.IconName {
	if !isDir {
		return model.IconFile
	}
	switch strings.ToLower(name) {
	case "desktop":
		return model.IconFolder
	case "documents":
		return model.IconDoc
	case "downloads":
		return model.IconDown
	case "pictures":
		return model.IconPhoto
	case "movies":
		return model.IconVideo
	case "music":
		return model.IconMusic
	case "library":
		return model.IconLayers
	case "icloud drive", "dropbox", "google drive":
		return model.IconCloud
	}
	switch {
	case strings.HasPrefix(strings.ToLower(name), "onedrive"):
		return model.IconCloud
	case codeNames.MatchString(name):
		return model.IconCode
	case gameNames.MatchString(name):
		return model.IconGame
	}
	return model.IconFolder
}

type bucket struct {
	size, reclaimable int64
	isDir             bool
}

func (s *Scanner) toChildren(parent string, buckets map[string]*bucket) []model.SpaceNode {
	var all []model.SpaceNode
	for name, b := range buckets {
		if b.size <= 0 {
			continue
		}
		kind := model.SpaceFile
		if b.isDir {
			kind = model.SpaceFolder
		}
		all = append(all, model.SpaceNode{
			Name: name, Path: filepath.Join(parent, name), Size: b.size, Kind: kind,
			Icon: s.iconFor(name, b.isDir), Reclaimable: b.reclaimable,
		})
	}
	sortNodes(all)
	if len(all) <= maxChildren {
		return all
	}
	shown, rest := all[:maxChildren:maxChildren], all[maxChildren:]
	var size, rec int64
	for _, n := range rest {
		size += n.Size
		rec += n.Reclaimable
	}
	return append(shown, model.SpaceNode{
		Name: fmt.Sprintf("%s more items", commas(len(rest))), Path: parent, Size: size,
		Kind: model.SpaceOther, Icon: model.IconMore, Reclaimable: rec,
	})
}

func sortNodes(n []model.SpaceNode) {
	slices.SortStableFunc(n, func(a, b model.SpaceNode) int {
		switch {
		case a.Size > b.Size:
			return -1
		case a.Size < b.Size:
			return 1
		}
		return strings.Compare(a.Name, b.Name)
	})
}

func commas(n int) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

func sum(nodes []model.SpaceNode, f func(model.SpaceNode) int64) (t int64) {
	for _, n := range nodes {
		t += f(n)
	}
	return t
}

func sizeOf(n model.SpaceNode) int64        { return n.Size }
func reclaimableOf(n model.SpaceNode) int64 { return n.Reclaimable }

type measureOpts struct {
	label string
	icon  model.IconName
	kind  model.SpaceKind
	// exclude is a folder inside dir to leave out (the home folder when measuring /Users)
	exclude string
	// skip leaves extra subtrees out entirely
	skip func(p string) bool
}

// measure walks one folder; its direct children become the drill-down level.
// Nil when empty or out of budget.
func (r *run) measure(ctx context.Context, dir string, o measureOpts) *model.SpaceNode {
	if r.budget <= 0 || ctx.Err() != nil {
		return nil
	}
	phase := "Measuring " + o.label
	r.step(phase, "")
	s := r.s
	isDownloads := folded.Same(dir, filepath.Join(s.Home, "Downloads"))
	isLibrary := folded.Same(dir, filepath.Join(s.Home, "Library"))
	buckets := map[string]*bucket{}
	var total int64
	prefix := strings.TrimSuffix(dir, string(filepath.Separator)) + string(filepath.Separator)

	st := walk.Walk(ctx, dir, walk.Options{
		MaxEntries: r.budget,
		SkipDir: func(p, _ string, _ int) bool {
			return (o.exclude != "" && folded.Same(p, o.exclude)) || (o.skip != nil && o.skip(p))
		},
		OnDir: func(p string, depth int) {
			if depth == 1 {
				buckets[filepath.Base(p)] = &bucket{isDir: true}
			}
		},
		OnFile: func(f walk.File) {
			rel := strings.TrimPrefix(f.Path, prefix)
			key, _, nested := strings.Cut(rel, string(filepath.Separator))
			b := buckets[key]
			if b == nil {
				b = &bucket{isDir: nested}
				buckets[key] = b
			}
			size := allocatedOf(f)
			b.size += size
			total += size
			switch {
			case isDownloads && f.Newest() < r.cutoff:
				b.reclaimable += size
			case isLibrary && cacheSegment.MatchString(filepath.ToSlash(rel)):
				b.reclaimable += size
			}
		},
		OnTick: func(_ int, current string) { r.step(phase, pathsafe.Tilde(s.Home, current)) },
	})
	r.budget -= st.Files + st.Dirs
	r.done++
	if total <= 0 || ctx.Err() != nil {
		return nil
	}
	var rec int64
	for _, b := range buckets {
		rec += b.reclaimable
	}
	kind := o.kind
	if kind == "" {
		kind = model.SpaceFolder
	}
	return &model.SpaceNode{
		Name: o.label, Path: dir, Size: total, Kind: kind, Icon: o.icon, Reclaimable: rec,
		Children: s.toChildren(dir, buckets),
	}
}

type topLevel struct {
	folders []string
	files   []fileSize
}

type fileSize struct {
	name string
	size int64
}

func readTop(dir string, isDriveRoot bool) topLevel {
	var out topLevel
	list, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, d := range list {
		if isDriveRoot && skipTop[strings.ToLower(d.Name())] {
			continue
		}
		switch {
		case d.Type()&os.ModeSymlink != 0:
			// never followed
		case d.IsDir():
			out.folders = append(out.folders, d.Name())
		case d.Type().IsRegular():
			if fi, err := d.Info(); err == nil && fi.Mode().IsRegular() {
				out.files = append(out.files, fileSize{d.Name(), walk.Allocated(fi)})
			}
		}
	}
	return out
}

// looseNodes: big loose files stand alone; the rest are grouped as "Loose files".
func (s *Scanner) looseNodes(dir string, files []fileSize) []model.SpaceNode {
	var out []model.SpaceNode
	small := map[string]*bucket{}
	var total int64
	for _, f := range files {
		switch {
		case f.size >= looseFileMin:
			out = append(out, model.SpaceNode{Name: f.name, Path: filepath.Join(dir, f.name), Size: f.size, Kind: model.SpaceFile, Icon: model.IconFile})
		case f.size > 0:
			small[f.name] = &bucket{size: f.size}
			total += f.size
		}
	}
	if total > 0 {
		out = append(out, model.SpaceNode{
			Name: "Loose files", Path: dir, Size: total, Kind: model.SpaceFile, Icon: model.IconFile,
			Children: s.toChildren(dir, small),
		})
	}
	return out
}

// homeChildren is the home folder's own breakdown: one node per top-level folder plus loose files.
func (r *run) homeChildren(ctx context.Context, top topLevel) []model.SpaceNode {
	var children []model.SpaceNode
	for _, name := range top.folders {
		dir := filepath.Join(r.s.Home, name)
		if n := r.measure(ctx, dir, measureOpts{label: name, icon: r.s.iconFor(name, true)}); n != nil {
			children = append(children, *n)
		}
	}
	return append(children, r.s.looseNodes(r.s.Home, top.files)...)
}

// remainder is whatever the walk could not see (OS metadata, protected folders) as one bubble.
func remainder(disk *model.DiskInfo, measured int64, name string) *model.SpaceNode {
	if disk == nil || disk.Used-measured <= 0 {
		return nil
	}
	return &model.SpaceNode{Name: name, Path: disk.Mount, Size: disk.Used - measured, Kind: model.SpaceOther, Icon: model.IconChip}
}

func (r *run) scanHome(ctx context.Context) model.SpaceNode {
	s := r.s
	top := readTop(s.Home, false)
	r.total = len(top.folders)
	children := r.homeChildren(ctx, top)

	var disk *model.DiskInfo
	if s.Disks != nil {
		if d, err := s.Disks.System(ctx); err == nil {
			disk = &d
		}
	}
	if rest := remainder(disk, sum(children, sizeOf), "System & apps"); rest != nil {
		children = append(children, *rest)
	}
	sortNodes(children)
	r.done = r.total
	r.step("Done", "")
	return model.SpaceNode{
		Name: "Home", Path: s.Home, Size: sum(children, sizeOf), Kind: model.SpaceFolder, Icon: model.IconUser,
		Reclaimable: sum(children, reclaimableOf), Children: children,
	}
}

func (r *run) scanDrive(ctx context.Context, disk model.DiskInfo) model.SpaceNode {
	s := r.s
	mount := disk.Mount
	hasHome := folded.Inside(mount, s.Home)
	top := readTop(mount, true)
	var homeTop *topLevel
	if hasHome {
		t := readTop(s.Home, false)
		homeTop = &t
	}
	r.total = len(top.folders)
	if homeTop != nil {
		r.total += len(homeTop.folders)
	}

	var children []model.SpaceNode
	// the user's own files first, so a budget cut lands on system folders instead
	if homeTop != nil {
		kids := r.homeChildren(ctx, *homeTop)
		if size := sum(kids, sizeOf); size > 0 {
			sortNodes(kids)
			children = append(children, model.SpaceNode{
				Name: "Your files", Path: s.Home, Size: size, Kind: model.SpaceFolder, Icon: model.IconUser,
				Reclaimable: sum(kids, reclaimableOf), Children: kids,
			})
		}
	}

	isSystem := func(n string) bool { return systemTop[strings.ToLower(n)] }
	ordered := slices.Clone(top.folders)
	slices.SortStableFunc(ordered, func(a, b string) int { return btoi(isSystem(a)) - btoi(isSystem(b)) })
	// macOS mirrors the Data volume under /System/Volumes; counting it again would double everything
	var skip func(string) bool
	if disk.System {
		skip = func(p string) bool { return folded.Same(p, "/System/Volumes") }
	}
	for _, name := range ordered {
		dir := filepath.Join(mount, name)
		system := isSystem(name)
		holdsHome := hasHome && folded.Inside(dir, s.Home)
		icon := s.iconFor(name, true)
		kind := model.SpaceFolder
		switch {
		case system:
			icon, kind = model.IconChip, model.SpaceOther
		case name == "Applications":
			icon = model.IconApps
		case holdsHome:
			icon = model.IconFolder
		}
		o := measureOpts{label: name, icon: icon, kind: kind, skip: skip}
		if holdsHome {
			o.exclude = s.Home
		}
		if n := r.measure(ctx, dir, o); n != nil {
			children = append(children, *n)
		}
	}
	children = append(children, s.looseNodes(mount, top.files)...)

	label := "Hidden & reserved"
	if disk.System {
		label = "System & hidden"
	}
	if rest := remainder(&disk, sum(children, sizeOf), label); rest != nil {
		children = append(children, *rest)
	}
	sortNodes(children)
	r.done = r.total
	r.step("Done", "")
	return model.SpaceNode{
		Name: disk.Label, Path: mount, Size: sum(children, sizeOf), Kind: model.SpaceFolder, Icon: model.IconLayers,
		Reclaimable: sum(children, reclaimableOf), Children: children,
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Scan breaks down the home folder (root == "") or a whole fixed volume (its
// mount, as listed by the disks) by size, two levels deep.
func (s *Scanner) Scan(ctx context.Context, root string, rep progress.Reporter) (model.SpaceNode, error) {
	r := &run{s: s, rep: rep, budget: maxEntries, cutoff: s.now().Add(-oldFile).UnixMilli()}
	var node model.SpaceNode
	if root == "" {
		node = r.scanHome(ctx)
	} else {
		if s.Disks == nil || len(root) > 4096 || strings.ContainsRune(root, 0) {
			return model.SpaceNode{}, errNoDrive
		}
		list, err := s.Disks.List(ctx)
		if err != nil {
			return model.SpaceNode{}, err
		}
		i := slices.IndexFunc(list, func(d model.DiskInfo) bool { return d.Mount == root })
		if i < 0 {
			return model.SpaceNode{}, errNoDrive
		}
		node = r.scanDrive(ctx, list[i])
	}
	if ctx.Err() != nil {
		return model.SpaceNode{}, progress.ErrCancelled
	}
	return node, nil
}
