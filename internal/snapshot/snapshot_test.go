package snapshot

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"dev.agus.bersihin/internal/model"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newStore() (*Store, *clock) {
	c := &clock{t: time.Unix(2_000_000, 0)}
	return &Store{Now: c.now}, c
}

func id(i int) string { return fmt.Sprintf("scan-id-%08d", i) }

func TestPutGet(t *testing.T) {
	s, _ := newStore()
	s.Put(Large(id(1), []model.LargeFile{{Path: "/a", Size: 5, Modified: 7}}))
	got, err := s.Get(id(1), model.TaskLarge)
	if err != nil {
		t.Fatal(err)
	}
	if got.Large["/a"] != (Stamp{Size: 5, MTime: 7}) {
		t.Fatalf("snap = %+v", got)
	}
	if !s.Has(id(1)) || s.Has(id(2)) {
		t.Fatal("Has is wrong")
	}
}

func TestUnknownAndWrongKind(t *testing.T) {
	s, _ := newStore()
	s.Put(Large(id(1), nil))
	if _, err := s.Get(id(2), model.TaskLarge); !errors.Is(err, ErrExpired) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.Get(id(1), model.TaskDupes); !errors.Is(err, ErrKind) {
		t.Fatalf("wrong kind: %v", err)
	}
	if _, err := s.Get("", model.TaskLarge); !errors.Is(err, ErrExpired) {
		t.Fatalf("empty: %v", err)
	}
}

func TestTTL(t *testing.T) {
	s, c := newStore()
	s.Put(Junk(id(1), c.t, []model.JunkCategory{{ID: "mac-logs"}}))
	c.t = c.t.Add(10*time.Minute - time.Second)
	if _, err := s.Get(id(1), model.TaskJunk); err != nil {
		t.Fatalf("before the TTL: %v", err)
	}
	c.t = c.t.Add(time.Second)
	if _, err := s.Get(id(1), model.TaskJunk); !errors.Is(err, ErrExpired) {
		t.Fatalf("at the TTL: %v", err)
	}
	if s.Has(id(1)) {
		t.Fatal("expired snapshot still stored")
	}
}

func TestTTLCountsFromStoringNotFromScanStart(t *testing.T) {
	s, c := newStore()
	at := c.t
	c.t = c.t.Add(9 * time.Minute) // a long scan
	s.Put(Junk(id(1), at, nil))
	c.t = c.t.Add(5 * time.Minute)
	if _, err := s.Get(id(1), model.TaskJunk); err != nil {
		t.Fatalf("14 minutes after the scan began, 5 after it ended: %v", err)
	}
	got, _ := s.Get(id(1), model.TaskJunk)
	if !got.At.Equal(at) {
		t.Fatalf("At = %v, want the scan's own reading %v", got.At, at)
	}
}

func TestEvictsOldestAtThirteen(t *testing.T) {
	s, c := newStore()
	for i := 1; i <= 13; i++ {
		c.t = c.t.Add(time.Second)
		s.Put(Apps(id(i), nil))
	}
	if s.Has(id(1)) {
		t.Fatal("the oldest of 13 should be gone")
	}
	for i := 2; i <= 13; i++ {
		if !s.Has(id(i)) {
			t.Fatalf("snapshot %d evicted", i)
		}
	}
}

func TestPutRefusesAStoredID(t *testing.T) {
	s, c := newStore()
	if err := s.Put(Large(id(1), []model.LargeFile{{Path: "/a", Size: 1}})); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(Large(id(1), []model.LargeFile{{Path: "/b", Size: 1}})); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v", err)
	}
	got, _ := s.Get(id(1), model.TaskLarge)
	if _, ok := got.Large["/a"]; !ok || len(got.Large) != 1 {
		t.Fatalf("the stored snapshot was replaced: %+v", got)
	}
	// once it has expired the id is free again
	c.t = c.t.Add(11 * time.Minute)
	if err := s.Put(Large(id(1), nil)); err != nil {
		t.Fatalf("expired id: %v", err)
	}
}

func TestDupesIndex(t *testing.T) {
	s, _ := newStore()
	s.Put(Dupes(id(1), []model.DuplicateGroup{
		{Hash: "h1", Size: 9, Files: []model.DuplicateFile{{Path: "/a", Modified: 1}, {Path: "/b", Modified: 2}}},
		{Hash: "h2", Size: 8, Files: []model.DuplicateFile{{Path: "/c", Modified: 3}, {Path: "/d", Modified: 4}}},
	}))
	got, err := s.Get(id(1), model.TaskDupes)
	if err != nil {
		t.Fatal(err)
	}
	if got.DupeAt["/a"] != 0 || got.DupeAt["/d"] != 1 || len(got.DupeAt) != 4 {
		t.Fatalf("index = %v", got.DupeAt)
	}
	if got.Dupes[1].Members[0] != (Member{Path: "/c", MTime: 3}) || got.Dupes[1].Hash != "h2" || got.Dupes[1].Size != 8 {
		t.Fatalf("group = %+v", got.Dupes[1])
	}
}

func TestJunkIDsAndApps(t *testing.T) {
	s, c := newStore()
	s.Put(Junk(id(1), c.t, []model.JunkCategory{{ID: "a"}, {ID: "b"}}))
	s.Put(Apps(id(2), []model.InstalledApp{{ID: "mac:1"}}))
	j, _ := s.Get(id(1), model.TaskJunk)
	a, _ := s.Get(id(2), model.TaskApps)
	if !j.JunkIDs["a"] || !j.JunkIDs["b"] || j.JunkIDs["c"] || !a.Apps["mac:1"] {
		t.Fatalf("j = %+v a = %+v", j, a)
	}
}
