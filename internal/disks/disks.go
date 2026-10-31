// Package disks lists the fixed local volumes and their sizes.
package disks

import (
	"context"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/parse"
	"dev.agus.bersihin/internal/sys"
	"dev.agus.bersihin/internal/walk"
)

const (
	inventoryTTL = 30 * time.Second
	failureTTL   = 2 * time.Second
	systemLabel  = "Macintosh HD"
)

var skipFS = regexp.MustCompile(`(?i)^(smbfs|nfs|afpfs|webdav|cifs|ftp|autofs|devfs|fdesc|nullfs|osxfuse|macfuse|lifs)$`)
var timeMachine = regexp.MustCompile(`(?i)time machine|backups? of |com\.apple\.timemachine`)

// Lister lists volumes. Create it with New; Run == nil gives the system
// volume only.
type Lister struct {
	// Run runs /sbin/mount and /usr/bin/hdiutil.
	Run sys.Runner
	// Home is the home folder: it lives on the Data volume, so the system
	// volume's size is read there.
	Home string
	// Label names the system volume.
	Label string
	// SystemMount is where the system volume is mounted, default "/" (on
	// Windows the home folder's drive, as C:\).
	SystemMount string
	// Drives, when set, lists the volumes instead of the mount table (on
	// Windows, the drive letters). ok false marks a failed probe.
	Drives func(ctx context.Context) (vols []Volume, ok bool)

	// Stat returns the size of the volume holding path. Default: statfs.
	Stat func(path string) (total, free int64, err error)
	// IsRealDir and Exists probe the file system. Defaults: walk's.
	IsRealDir func(string) bool
	Exists    func(string) bool
	Now       func() time.Time

	mu    sync.Mutex
	at    time.Time
	ttl   time.Duration
	vols  []volume
	valid bool
}

type volume struct {
	mount, label string
	system       bool
}

// Volume is one entry of Lister.Drives.
type Volume struct {
	Mount, Label string
	System       bool
}

func (l *Lister) systemMount() string {
	if l.SystemMount != "" {
		return l.SystemMount
	}
	return "/"
}

func (l *Lister) stat(p string) (int64, int64, error) {
	if l.Stat != nil {
		return l.Stat(p)
	}
	return statfs(p)
}

func (l *Lister) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Lister) label() string {
	if l.Label != "" {
		return l.Label
	}
	return systemLabel
}

func (l *Lister) isRealDir(p string) bool {
	if l.IsRealDir != nil {
		return l.IsRealDir(p)
	}
	return walk.IsRealDir(p)
}

func (l *Lister) exists(p string) bool {
	if l.Exists != nil {
		return l.Exists(p)
	}
	return walk.Exists(p)
}

// volumes returns the inventory, cached for 30 seconds; a failed probe (the
// system volume alone) is cached for 2 seconds so a broken mount command is
// not run on every call. The lock covers only the cache: the commands run
// without it, so a slow `mount` never blocks other callers.
func (l *Lister) volumes(ctx context.Context) []volume {
	l.mu.Lock()
	if l.valid && l.now().Sub(l.at) < l.ttl {
		v := l.vols
		l.mu.Unlock()
		return v
	}
	l.mu.Unlock()

	vols, ok := l.probe(ctx)
	ttl := inventoryTTL
	if !ok {
		ttl = failureTTL
	}
	l.mu.Lock()
	l.vols, l.at, l.ttl, l.valid = vols, l.now(), ttl, true
	l.mu.Unlock()
	return vols
}

func (l *Lister) probe(ctx context.Context) ([]volume, bool) {
	if l.Drives != nil {
		list, ok := l.Drives(ctx)
		var vols []volume
		for _, v := range list {
			vols = append(vols, volume{mount: v.Mount, label: v.Label, system: v.System})
		}
		if len(vols) == 0 {
			vols = []volume{{mount: l.systemMount(), label: l.label(), system: true}}
		}
		return vols, ok
	}
	vols := []volume{{mount: "/", label: l.label(), system: true}}
	if l.Run == nil {
		return vols, true
	}
	res, err := l.Run.Run(ctx, "/sbin/mount")
	if err != nil {
		return vols, false
	}
	// Without the list of mounted disk images a writable image could pass for
	// a fixed volume: a failed hdiutil makes the whole probe a failure.
	hd, err := l.Run.Run(ctx, "/usr/bin/hdiutil", "info")
	if err != nil {
		return vols, false
	}
	images := parse.HdiutilMounts(hd.Stdout)
	for _, e := range parse.Mount(res.Stdout) {
		if v, ok := l.dataVolume(e, images); ok {
			vols = append(vols, v)
		}
	}
	return vols, true
}

// dataVolume decides whether a mount is a fixed local volume worth listing:
// directly below /Volumes, a real device, local, writable, visible, not a
// disk image, not Time Machine.
func (l *Lister) dataVolume(e parse.MountEntry, images map[string]bool) (volume, bool) {
	rest, ok := strings.CutPrefix(e.Mount, "/Volumes/")
	if !ok || rest == "" || strings.Contains(rest, "/") {
		return volume{}, false
	}
	if !strings.HasPrefix(e.Device, "/dev/") || skipFS.MatchString(e.FSType()) {
		return volume{}, false
	}
	if !e.Has("local") || e.Has("read-only") || e.Has("nobrowse") || e.Has("quarantine") {
		return volume{}, false
	}
	name := path.Base(e.Mount)
	if images[e.Mount] || strings.HasPrefix(name, ".") || timeMachine.MatchString(name) {
		return volume{}, false
	}
	if !l.isRealDir(e.Mount) || l.exists(path.Join(e.Mount, "Backups.backupdb")) {
		return volume{}, false
	}
	return volume{mount: e.Mount, label: name}, true
}

func (l *Lister) measure(v volume) (model.DiskInfo, bool) {
	p := v.mount
	if v.system && l.Home != "" {
		p = l.Home // the Data volume shares the APFS container with "/"
	}
	total, free, err := l.stat(p)
	if err != nil || total <= 0 {
		return model.DiskInfo{}, false
	}
	return model.DiskInfo{Mount: v.mount, Label: v.label, Total: total, Free: free, Used: max(0, total-free), System: v.system}, true
}

// List returns every fixed local volume, the system volume first.
func (l *Lister) List(ctx context.Context) ([]model.DiskInfo, error) {
	var out []model.DiskInfo
	seen := map[string]bool{}
	for _, v := range l.volumes(ctx) {
		if seen[v.mount] {
			continue
		}
		seen[v.mount] = true
		if d, ok := l.measure(v); ok {
			out = append(out, d)
		}
	}
	slices.SortStableFunc(out, func(a, b model.DiskInfo) int {
		if a.System != b.System {
			if a.System {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(a.Mount), strings.ToLower(b.Mount))
	})
	return out, nil
}

// System returns the volume the home folder lives on.
func (l *Lister) System(context.Context) (model.DiskInfo, error) {
	p := l.Home
	if p == "" {
		p = "/"
	}
	total, free, err := l.stat(p)
	if err != nil {
		return model.DiskInfo{}, err
	}
	return model.DiskInfo{Mount: l.systemMount(), Label: l.label(), Total: total, Free: free, Used: max(0, total-free), System: true}, nil
}

// DataMounts returns the mounts of fixed volumes other than the system one.
func (l *Lister) DataMounts(ctx context.Context) []string {
	list, _ := l.List(ctx)
	var out []string
	for _, d := range list {
		if !d.System && d.Mount != l.systemMount() {
			out = append(out, d.Mount)
		}
	}
	return out
}
