// Package snapshot remembers what each scan returned to the page, so that a
// destructive call can be checked against it: the ids or paths must come from
// that scan, the scan must be recent, and files must be unchanged since.
package snapshot

import (
	"errors"
	"sync"
	"time"

	"dev.agus.bersihin/internal/config"
	"dev.agus.bersihin/internal/model"
)

// Errors Get returns. The page matches ErrExpired's text (src/lib/confirm.ts).
var (
	// ErrExpired: the scan is unknown, evicted or older than config.ScanTTL.
	ErrExpired = errors.New("Scan expired: scan again")
	// ErrDuplicate: a snapshot with this id is already stored.
	ErrDuplicate = errors.New("Invalid request: run id in use")
	// ErrKind: the id belongs to another kind of scan.
	ErrKind = errors.New("Invalid request")
)

// Stamp is what a scan saw of a file: size in bytes, modification time in epoch
// milliseconds (the units of model.LargeFile.Modified and walk.Times).
type Stamp struct{ Size, MTime int64 }

// Seen is what a junk scan saw of one old download (a top-level entry of
// Downloads): which entry it was, and the newest time of use it found in it, in
// epoch milliseconds. A clean moves the entry only if it is still that entry
// and nothing in it got newer.
type Seen struct {
	// Dev and Ino identify the entry; Ino 0 means unknown and is not compared.
	Dev, Ino uint64
	// Dir: the entry is a folder (Size and MTime are then 0).
	Dir bool
	// Size and MTime are those of a file, CTime its status change time (0
	// when unknown): a file deleted and replaced by one of the same size and
	// times, possibly on a reused inode, still has a different CTime.
	Size, MTime, CTime int64
	// Newest is the latest time the entry was changed, created or used.
	Newest int64
	// Bytes and Files are the total size and number of files inside a folder
	// (Dir only). A clean keeps a folder whose total changed since the scan.
	Bytes int64
	Files int
}

// Member is one copy in a duplicate group.
type Member struct {
	Path  string
	MTime int64
}

// Group is a duplicate group as it was sent to the page.
type Group struct {
	Hash    string
	Size    int64
	Members []Member
}

// Snap is the remembered result of one scan. It is never changed after Put.
type Snap struct {
	ID   string
	Task model.ScanTask
	// At is the moment the scan took its reading of the clock (junk cutoffs
	// are computed from it).
	At time.Time

	JunkIDs map[string]bool
	// TrashEntries are the top-level entries of the Trash the junk scan saw
	// (name -> device, inode); a clean empties only those.
	TrashEntries map[string][2]uint64
	// Downloads are the old downloads the junk scan offered (path -> what it
	// saw); a clean moves only those.
	Downloads map[string]Seen
	Large     map[string]Stamp
	Dupes     []Group
	DupeAt    map[string]int // path -> index into Dupes
	Apps      map[string]bool

	stored time.Time
}

// Junk remembers the categories a junk scan returned, taken at time at.
func Junk(id string, at time.Time, cats []model.JunkCategory) *Snap {
	s := &Snap{ID: id, Task: model.TaskJunk, At: at, JunkIDs: map[string]bool{}}
	for _, c := range cats {
		s.JunkIDs[c.ID] = true
	}
	return s
}

// Large remembers the files a large file scan returned.
func Large(id string, files []model.LargeFile) *Snap {
	s := &Snap{ID: id, Task: model.TaskLarge, Large: make(map[string]Stamp, len(files))}
	for _, f := range files {
		s.Large[f.Path] = Stamp{Size: f.Size, MTime: f.Modified}
	}
	return s
}

// Dupes remembers the groups a duplicate scan returned.
func Dupes(id string, groups []model.DuplicateGroup) *Snap {
	s := &Snap{ID: id, Task: model.TaskDupes, DupeAt: map[string]int{}}
	for i, g := range groups {
		out := Group{Hash: g.Hash, Size: g.Size}
		for _, f := range g.Files {
			out.Members = append(out.Members, Member{Path: f.Path, MTime: f.Modified})
			s.DupeAt[f.Path] = i
		}
		s.Dupes = append(s.Dupes, out)
	}
	return s
}

// Apps remembers the apps an app list returned.
func Apps(id string, apps []model.InstalledApp) *Snap {
	s := &Snap{ID: id, Task: model.TaskApps, Apps: make(map[string]bool, len(apps))}
	for _, a := range apps {
		s.Apps[a.ID] = true
	}
	return s
}

// Store holds the most recent snapshots. The zero value works (real clock).
type Store struct {
	// Now is the clock; default time.Now.
	Now func() time.Time

	mu    sync.Mutex
	snaps map[string]*Snap
	order []string // oldest first
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Store) drop(id string) {
	delete(s.snaps, id)
	for i, v := range s.order {
		if v == id {
			s.order = append(s.order[:i], s.order[i+1:]...)
			break
		}
	}
}

// purge removes expired snapshots. The caller holds mu.
func (s *Store) purge(now time.Time) {
	for _, id := range append([]string(nil), s.order...) {
		if !now.Before(s.snaps[id].stored.Add(config.ScanTTL)) {
			s.drop(id)
		}
	}
}

// Put remembers snap and forgets the oldest snapshots beyond
// config.MaxSnapshots. It refuses an id that is already stored: a scan's
// results are never replaced under tokens or pages that still use its id.
func (s *Store) Put(snap *Snap) error {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snaps == nil {
		s.snaps = map[string]*Snap{}
	}
	s.purge(now)
	if _, ok := s.snaps[snap.ID]; ok {
		return ErrDuplicate
	}
	snap.stored = now
	s.snaps[snap.ID] = snap
	s.order = append(s.order, snap.ID)
	for len(s.order) > config.MaxSnapshots {
		s.drop(s.order[0])
	}
	return nil
}

// Has reports whether a snapshot with this id is stored and not expired.
func (s *Store) Has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge(s.now())
	_, ok := s.snaps[id]
	return ok
}

// Get returns the snapshot of this id if it is of the given kind and recent.
func (s *Store) Get(id string, task model.ScanTask) (*Snap, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purge(s.now())
	snap, ok := s.snaps[id]
	if !ok {
		return nil, ErrExpired
	}
	if snap.Task != task {
		return nil, ErrKind
	}
	return snap, nil
}
