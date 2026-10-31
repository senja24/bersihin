package junk

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeBin is a Recycle Bin that records what happened, in order, into log.
type fakeBin struct {
	size     BinSize
	queryErr error
	emptyErr error
	keep     int64 // items that stay after Empty (in use)
	log      *[]string
}

func (b *fakeBin) Query() (BinSize, error) { return b.size, b.queryErr }
func (b *fakeBin) Empty() error {
	*b.log = append(*b.log, "empty")
	if b.emptyErr != nil {
		return b.emptyErr
	}
	if b.keep > 0 {
		b.size = BinSize{Size: b.keep * 10, Items: b.keep}
	} else {
		b.size = BinSize{}
	}
	return nil
}

func binFixture(t *testing.T) (*fixture, *fakeBin, *[]string) {
	f := winFixture(t, nil)
	var log []string
	bin := &fakeBin{size: BinSize{Size: 5000, Items: 3}, log: &log}
	f.sc.RecycleBin = bin
	move := f.sc.Trasher.Move
	f.sc.Trasher.Move = func(p string) error { log = append(log, "move"); return move(p) }
	return f, bin, &log
}

func TestRecycleBinScanned(t *testing.T) {
	f, _, _ := binFixture(t)
	cats, st, err := f.sc.ScanWithState(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range cats {
		if c.ID == "recycle-bin" {
			found = true
			if c.Size != 5000 || c.Count != 3 || c.Safe || !c.Irreversible || c.Name != "Recycle Bin" {
				t.Fatalf("category = %+v", c)
			}
		}
	}
	if !found || st.RecycleBin == nil || *st.RecycleBin != (BinSize{5000, 3}) {
		t.Fatalf("found %v, state %+v", found, st.RecycleBin)
	}
}

func TestRecycleBinNotListedWhenEmptyOrUnread(t *testing.T) {
	for _, bin := range []*fakeBin{{}, {queryErr: errors.New("no shell")}} {
		f := winFixture(t, nil)
		bin.log = new([]string)
		f.sc.RecycleBin = bin
		cats, _ := f.sc.Scan(context.Background(), nil)
		for _, c := range cats {
			if c.ID == "recycle-bin" {
				t.Fatalf("listed: %+v", c)
			}
		}
	}
}

func TestRecycleBinEmptied(t *testing.T) {
	f, _, log := binFixture(t)
	f.sc.RecycleSeen = &BinSize{5000, 3}
	res, err := f.sc.Clean(context.Background(), []string{"recycle-bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Freed != 5000 || res.Removed != 3 || len(res.Skipped) != 0 || strings.Join(*log, ",") != "empty" {
		t.Fatalf("%+v, log %v", res, *log)
	}
	// already empty: nothing to do, no call
	res, _ = f.sc.Clean(context.Background(), []string{"recycle-bin"}, nil)
	if res.Removed != 0 || len(*log) != 1 {
		t.Fatalf("%+v, log %v", res, *log)
	}
}

func TestRecycleBinChangedSinceScanStays(t *testing.T) {
	f, bin, log := binFixture(t)
	f.sc.RecycleSeen = &BinSize{5000, 3}
	bin.size = BinSize{Size: 6000, Items: 4} // something was deleted after the scan
	res, _ := f.sc.Clean(context.Background(), []string{"recycle-bin"}, nil)
	if len(*log) != 0 || res.Removed != 0 || len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], "changed since the scan") {
		t.Fatalf("%+v, log %v", res, *log)
	}
	// fewer items than seen (the user restored one) may be emptied
	bin.size = BinSize{Size: 4000, Items: 2}
	res, _ = f.sc.Clean(context.Background(), []string{"recycle-bin"}, nil)
	if res.Removed != 2 || len(*log) != 1 {
		t.Fatalf("%+v, log %v", res, *log)
	}
}

func TestRecycleBinFailures(t *testing.T) {
	f, bin, _ := binFixture(t)
	bin.emptyErr = errors.New("busy")
	res, _ := f.sc.Clean(context.Background(), []string{"recycle-bin"}, nil)
	if res.Failed != 1 || res.Removed != 0 || !strings.Contains(strings.Join(res.Skipped, "|"), "did not empty") {
		t.Fatalf("%+v", res)
	}
	bin.emptyErr, bin.keep = nil, 1
	res, _ = f.sc.Clean(context.Background(), []string{"recycle-bin"}, nil)
	if res.Removed != 2 || res.Freed != 4990 || !strings.Contains(strings.Join(res.Skipped, "|"), "1 item stayed") {
		t.Fatalf("%+v", res)
	}
	bin.queryErr = errors.New("gone")
	res, _ = f.sc.Clean(context.Background(), []string{"recycle-bin"}, nil)
	if res.Failed != 1 || !strings.Contains(strings.Join(res.Skipped, "|"), "could not be read") {
		t.Fatalf("%+v", res)
	}
}

func TestRecycleBinEmptiedBeforeOldDownloadsGoIn(t *testing.T) {
	f, _, log := binFixture(t)
	put(t, f.p("Downloads", "old.zip"), 900, old)
	res, err := f.sc.Clean(context.Background(), []string{"old-downloads", "recycle-bin"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(*log, ",") != "empty,move" || res.Removed != 4 {
		t.Fatalf("log %v, %+v", *log, res)
	}
}
