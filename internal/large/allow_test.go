package large

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"dev.agus.bersihin/internal/testenv"
)

// Files the Trash guard refuses are not listed, and the list is still filled
// to the limit from the files that remain, even though the refused ones are
// the biggest.
func TestScanAllowFiltersAndStillFillsTheLimit(t *testing.T) {
	home := t.TempDir()
	for i := range 40 {
		put(t, filepath.Join(home, "Refused", fmt.Sprintf("big%02d.bin", i)), (900+i)*mb)
	}
	for i := range maxResults + 20 {
		put(t, filepath.Join(home, "Movies", fmt.Sprintf("f%03d.bin", i)), 2*mb+i*1024)
	}
	calls := 0
	s := &Scanner{
		Home: home, TempDir: filepath.Join(home, "no-tmp"),
		Allow: func(p string) bool { calls++; return !strings.Contains(p, "Refused") },
	}
	got, err := s.Scan(context.Background(), mb, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxResults {
		t.Fatalf("listed %d files, want %d", len(got), maxResults)
	}
	for _, f := range got {
		if strings.Contains(f.Path, "Refused") {
			t.Fatalf("refused file listed: %s", f.Path)
		}
	}
	if calls == 0 {
		t.Fatal("Allow was never asked")
	}
}

// The filter also runs when the walk found far more files than the limit
// (the running trim), without losing allowed files to refused ones.
func TestScanAllowAcrossRunningTrim(t *testing.T) {
	old := trimAt
	trimAt = 40
	t.Cleanup(func() { trimAt = old })
	home := t.TempDir()
	n := maxResults * 2
	for i := range n {
		dir := "Movies"
		if i%2 == 0 {
			dir = "Refused"
		}
		put(t, filepath.Join(home, dir, fmt.Sprintf("f%05d.bin", i)), mb+i*512)
	}
	s := &Scanner{
		Home: home, TempDir: filepath.Join(home, "no-tmp"),
		Allow: func(p string) bool { return !strings.Contains(p, "Refused") },
	}
	got, err := s.Scan(context.Background(), mb, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxResults {
		t.Fatalf("listed %d files, want %d", len(got), maxResults)
	}
	for _, f := range got {
		if strings.Contains(f.Path, "Refused") {
			t.Fatalf("refused file listed: %s", f.Path)
		}
	}
	// the biggest allowed file (the highest odd index) leads
	if want := fmt.Sprintf("f%05d.bin", n-1); got[0].Name != want {
		t.Fatalf("first = %s, want %s", got[0].Name, want)
	}
}

// The files Allow refuses are counted, once each, so that the page can say
// they were left out.
func TestScanCountedReportsRefusedFiles(t *testing.T) {
	testenv.MacLayout(t)
	old := trimAt
	trimAt = 40
	t.Cleanup(func() { trimAt = old })
	home := t.TempDir()
	for i := range 7 {
		put(t, filepath.Join(home, "Refused", fmt.Sprintf("r%d.bin", i)), (900+i)*mb)
	}
	for i := range 90 { // more than trimAt: the running trim runs too
		put(t, filepath.Join(home, "Movies", fmt.Sprintf("f%03d.bin", i)), 2*mb+i*1024)
	}
	s := &Scanner{
		Home: home, TempDir: filepath.Join(home, "no-tmp"),
		Allow: func(p string) bool { return !strings.Contains(p, "/Refused/") },
	}
	got, hidden, err := s.ScanCounted(context.Background(), mb, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 90 || hidden != 7 {
		t.Fatalf("listed %d files, hidden %d; want 90 and 7", len(got), hidden)
	}
	_, none, _ := (&Scanner{Home: home, TempDir: filepath.Join(home, "no-tmp")}).ScanCounted(context.Background(), mb, nil)
	if none != 0 {
		t.Fatalf("hidden = %d without an Allow rule", none)
	}
}

// A refused file that would have been cut by size anyway is not counted as
// left out: only those that would have made the list are.
func TestScanCountedIgnoresRefusedFilesBelowTheCut(t *testing.T) {
	old := trimAt
	trimAt = 40
	t.Cleanup(func() { trimAt = old })
	home := t.TempDir()
	// directly in home: found before the subfolder is read, so the running
	// trim meets them while the list is still short
	for i := range 10 {
		put(t, filepath.Join(home, fmt.Sprintf("refused-small%d.bin", i)), mb+i*1024)
	}
	for i := range 3 {
		put(t, filepath.Join(home, fmt.Sprintf("refused-big%d.bin", i)), (900+i)*mb)
	}
	for i := range maxResults + 40 {
		put(t, filepath.Join(home, "Movies", fmt.Sprintf("f%03d.bin", i)), 5*mb+i*1024)
	}
	s := &Scanner{
		Home: home, TempDir: filepath.Join(home, "no-tmp"),
		Allow: func(p string) bool { return !strings.Contains(filepath.Base(p), "refused") },
	}
	got, hidden, err := s.ScanCounted(context.Background(), mb, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != maxResults || hidden != 3 {
		t.Fatalf("listed %d files, hidden %d; want %d and 3", len(got), hidden, maxResults)
	}
}
