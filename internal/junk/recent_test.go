package junk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecentItemsLastUsed(t *testing.T) {
	recent := t.TempDir()
	at := func(name string, when time.Time) {
		p := filepath.Join(recent, name)
		os.WriteFile(p, nil, 0o644)
		os.Chtimes(p, when, when)
	}
	week := time.Now().Add(-7 * 24 * time.Hour).Truncate(time.Second)
	year := time.Now().Add(-365 * 24 * time.Hour).Truncate(time.Second)
	at("Report.PDF.lnk", week)     // opened last week
	at("setup.lnk", year)          // shown without its extension
	at("Holiday Photos.lnk", week) // a folder
	at("notes.txt", week)          // not a shortcut
	at("AutomaticDestinations", week)

	dl := filepath.Join(t.TempDir(), "Downloads")
	paths := []string{
		filepath.Join(dl, "report.pdf"), filepath.Join(dl, "setup.exe"), filepath.Join(dl, "Holiday Photos"),
		filepath.Join(dl, "notes.txt"), filepath.Join(dl, "never.zip"),
	}
	got, err := RecentItemsLastUsed(recent)(context.Background(), paths)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{week.UnixMilli(), year.UnixMilli(), week.UnixMilli(), 0, 0}
	for i, p := range paths {
		if got[p] != want[i] {
			t.Errorf("%s: %d, want %d", filepath.Base(p), got[p], want[i])
		}
	}
	// no Recent folder: no signal, no error, so the files' own dates decide
	got, err = RecentItemsLastUsed(filepath.Join(recent, "missing"))(context.Background(), paths)
	if got != nil || err != nil {
		t.Fatalf("missing folder: %v %v", got, err)
	}
	if RecentItemsLastUsed("") != nil || RecentItemsLastUsed("relative") != nil {
		t.Fatal("a relative folder must give no lookup")
	}
}

func TestRecentItemsKeepAnOpenedOldDownload(t *testing.T) {
	f := winFixture(t, nil)
	recent := filepath.Join(f.outside, "Recent")
	os.MkdirAll(recent, 0o755)
	put(t, f.p("Downloads", "opened.iso"), 500, old)
	put(t, f.p("Downloads", "forgotten.iso"), 700, old)
	lnk := filepath.Join(recent, "opened.iso.lnk")
	os.WriteFile(lnk, nil, 0o644)
	f.sc.LastUsed = RecentItemsLastUsed(recent)
	cats, err := f.sc.Scan(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cats {
		if c.ID == "old-downloads" {
			found = true
			if c.Count != 1 || c.Size != 700 {
				t.Fatalf("old downloads = %+v, want only forgotten.iso", c)
			}
		}
	}
	if !found {
		t.Fatalf("no old downloads in %+v", cats)
	}
}
