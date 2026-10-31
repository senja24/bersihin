// Package icons turns the Finder icon of files, folders and apps into PNG
// data URLs for the page.
package icons

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/walk"
)

const (
	maxPaths    = 200
	maxCache    = 3000
	concurrency = 6
	maxInFlight = 8
	timeout     = 8 * time.Second
	// Size is the icon edge in pixels (the page shows them at about 32 CSS px).
	Size = 64
)

// automounted network folders: reading them may hang or reach a remote host
var autofsNames = []string{
	"/net", "/Network", "/home", "/private/var/automount", "/var/automount",
	"/System/Volumes/Data/net", "/System/Volumes/Data/Network", "/System/Volumes/Data/home",
	"/System/Volumes/Data/private/var/automount", "/System/Volumes/Data/var/automount",
}

var folded = pathsafe.Guard{FoldCase: true}

// Provider renders icons. Icon is Shell.FileIcon: PNG bytes of Size pixels.
type Provider struct {
	Home string
	Icon func(path string, size int) ([]byte, error)
	// LocalMounts returns the mounts of the fixed local volumes (other than "/").
	LocalMounts func(ctx context.Context) []string
	// Timeout per icon; default 8 seconds.
	Timeout time.Duration
	// Exists probes the file system; default: an lstat.
	Exists func(string) bool

	mu    sync.Mutex
	cache map[string]string // path -> data URL, "" = the system had no icon
	order []string
	sem   chan struct{} // limits icon lookups in flight, timed-out ones included
}

// ErrInvalid is returned for a request that is too large.
type ErrInvalid struct{}

func (ErrInvalid) Error() string { return "Invalid request" }

// Candidate returns the absolute local path to draw the icon of (or to show
// in Finder), or "" when the request is refused: empty, huge, NUL, relative, a
// UNC-like "//" path, an automounted network folder, or a folder of a volume
// that is not a fixed local one. Paths are matched the way the file system
// matches names, so "/NET/x" and "/volumes/x" do not slip through.
func (p *Provider) Candidate(ctx context.Context, raw string) string {
	var mounts []string
	return p.candidate(ctx, raw, &mounts)
}

func (p *Provider) candidate(ctx context.Context, raw string, mounts *[]string) string {
	if raw == "" || len(raw) > 4096 || strings.ContainsRune(raw, 0) {
		return ""
	}
	e := pathsafe.ExpandTilde(p.Home, strings.TrimSpace(raw))
	if !filepath.IsAbs(e) || strings.HasPrefix(e, "//") {
		return ""
	}
	// no ".." and no segment that folds away to "." or ".." ("..\u200C")
	abs, ok := pathsafe.Clean(e)
	if !ok || p.denied(ctx, abs, mounts) {
		return ""
	}
	return abs
}

// denied applies the name rules: network and automount folders, the Data
// volume's view of /Volumes, and volumes that are not fixed local ones.
func (p *Provider) denied(ctx context.Context, abs string, mounts *[]string) bool {
	if filepath.Separator == '\\' {
		return p.deniedDrive(ctx, abs, mounts)
	}
	for _, root := range autofsNames {
		if folded.Same(abs, root) || folded.Inside(root, abs) {
			return true
		}
	}
	if folded.Inside("/System/Volumes/Data/Volumes", abs) || folded.Same("/System/Volumes/Data/Volumes", abs) {
		return true
	}
	if folded.Inside("/Volumes", abs) {
		if *mounts == nil {
			*mounts = []string{}
			if p.LocalMounts != nil {
				*mounts = append(*mounts, p.LocalMounts(ctx)...)
			}
		}
		for _, m := range *mounts {
			if m != "/" && (folded.Same(abs, m) || folded.Inside(m, abs)) {
				return false
			}
		}
		return true
	}
	return false
}

// realDenied runs the same rules on the path with the links of its parent
// folders resolved ("/var" is "/private/var"). The last element may itself be
// a link: its icon is the link target's, and nothing is opened. Call it inside
// a time-limited slot: resolving can touch a dead mount.
func (p *Provider) realDenied(ctx context.Context, abs string) bool {
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return true
	}
	var mounts []string
	return p.denied(ctx, filepath.Join(parent, filepath.Base(abs)), &mounts)
}

// limited runs fn in one of a few slots and waits for it, for at most the
// timeout. A call that hangs (a dead network mount, the main thread busy) keeps
// its slot until it returns, so stuck calls cannot pile up. It reports whether
// fn finished in time.
func (p *Provider) limited(fn func()) bool {
	d := p.Timeout
	if d <= 0 {
		d = timeout
	}
	p.mu.Lock()
	if p.sem == nil {
		p.sem = make(chan struct{}, maxInFlight)
	}
	sem := p.sem
	p.mu.Unlock()

	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case sem <- struct{}{}:
	case <-timer.C:
		return false
	}
	done := make(chan struct{})
	go func() {
		defer func() { <-sem }()
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}

// Present reports whether something exists at path (a link counts), asking
// within the same time limit as the icons: a path on a hung mount is "absent".
func (p *Provider) Present(path string) bool {
	var ok bool
	if !p.limited(func() { ok = p.exists(path) && !p.realDenied(context.Background(), path) }) {
		return false
	}
	return ok
}

func (p *Provider) exists(path string) bool {
	if p.Exists != nil {
		return p.Exists(path)
	}
	return walk.Exists(path)
}

type status int

const (
	found    status = iota // url holds the icon
	noIcon                 // the system has none: worth remembering
	missing                // nothing at the path
	timedOut               // worth retrying later
)

// load asks for one icon.
func (p *Provider) load(path string) (string, status) {
	var (
		png     []byte
		err     error
		present bool
	)
	if !p.limited(func() {
		if present = p.exists(path) && !p.realDenied(context.Background(), path); present {
			png, err = p.Icon(path, Size)
		}
	}) {
		return "", timedOut
	}
	switch {
	case !present:
		return "", missing
	case err != nil || len(png) == 0:
		return "", noIcon
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), found
}

func (p *Provider) remember(path, url string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cache == nil {
		p.cache = map[string]string{}
	}
	if _, ok := p.cache[path]; !ok {
		if len(p.order) >= maxCache {
			delete(p.cache, p.order[0])
			p.order = p.order[1:]
		}
		p.order = append(p.order, path)
	}
	p.cache[path] = url
}

// Icons returns real system icons as PNG data URLs, keyed by the path exactly
// as requested. Unknown, missing or refused paths are left out.
func (p *Provider) Icons(ctx context.Context, paths []string) (map[string]string, error) {
	if len(paths) > maxPaths {
		return nil, ErrInvalid{}
	}
	// one lookup per file, whatever spellings asked for it
	var order []string
	keys := map[string][]string{}
	seen := map[string]bool{}
	var mounts []string
	for _, raw := range paths {
		if seen[raw] {
			continue
		}
		seen[raw] = true
		if abs := p.candidate(ctx, raw, &mounts); abs != "" {
			if _, ok := keys[abs]; !ok {
				order = append(order, abs)
			}
			keys[abs] = append(keys[abs], raw)
		}
	}
	out := map[string]string{}
	var mu sync.Mutex
	walk.ForEach(ctx, len(order), concurrency, func(i int) {
		abs := order[i]
		p.mu.Lock()
		url, hit := p.cache[abs]
		p.mu.Unlock()
		if !hit {
			var st status
			url, st = p.load(abs)
			switch st {
			case found, noIcon:
				p.remember(abs, url) // "" is remembered too: no icon, no point asking again
			default:
				return
			}
		}
		if url != "" {
			mu.Lock()
			for _, k := range keys[abs] {
				out[k] = url
			}
			mu.Unlock()
		}
	})
	return out, nil
}

// deniedDrive is denied for Windows: only the drive of the home folder and
// the fixed local drives are asked for icons (pathsafe.Clean has refused UNC
// paths already), so a mapped network drive or a removable one is never
// touched.
func (p *Provider) deniedDrive(ctx context.Context, abs string, mounts *[]string) bool {
	vol := filepath.VolumeName(abs)
	if vol != "" && folded.Same(vol, filepath.VolumeName(p.Home)) {
		return false
	}
	if *mounts == nil {
		*mounts = []string{}
		if p.LocalMounts != nil {
			*mounts = append(*mounts, p.LocalMounts(ctx)...)
		}
	}
	for _, m := range *mounts {
		if folded.Same(abs, m) || folded.Inside(m, abs) {
			return false
		}
	}
	return true
}
