package recheck

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/snapshot"
)

var base = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func write(t *testing.T, p, content string, mtime time.Time) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// realHash hashes the file's content like dupes.FullHash (injected here to
// keep this package free of the scanner).
func realHash(_ context.Context, p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return sum(string(b)), nil
}

func large(t *testing.T, dir string) (*snapshot.Snap, []string) {
	t.Helper()
	var files []model.LargeFile
	var paths []string
	for i, name := range []string{"a", "b", "c"} {
		p := filepath.Join(dir, name)
		write(t, p, strings.Repeat("x", 10*(i+1)), base)
		files = append(files, model.LargeFile{Path: p, Size: int64(10 * (i + 1)), Modified: base.UnixMilli()})
		paths = append(paths, p)
	}
	return snapshot.Large("scan", files), paths
}

func TestLargeUnchangedPasses(t *testing.T) {
	snap, paths := large(t, t.TempDir())
	r := Check(context.Background(), snap, paths, nil, nil)
	if !slices.Equal(r.Allow, paths) || r.Failed != 0 || len(r.Lines) != 0 {
		t.Fatalf("r = %+v", r)
	}
}

func TestLargeChanges(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, p string)
	}{
		{"size changed", func(t *testing.T, p string) { write(t, p, "different length!!", base) }},
		{"mtime changed", func(t *testing.T, p string) { os.Chtimes(p, base.Add(time.Second), base.Add(time.Second)) }},
		{"mtime changed by one millisecond", func(t *testing.T, p string) {
			os.Chtimes(p, base.Add(time.Millisecond), base.Add(time.Millisecond))
		}},
		{"deleted", func(t *testing.T, p string) { os.Remove(p) }},
		{"replaced by a symlink", func(t *testing.T, p string) {
			other := filepath.Join(filepath.Dir(p), "target")
			write(t, other, strings.Repeat("x", 10), base)
			os.Remove(p)
			os.Symlink(other, p)
		}},
		{"replaced by a directory", func(t *testing.T, p string) { os.Remove(p); os.Mkdir(p, 0o755) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap, paths := large(t, t.TempDir())
			c.mutate(t, paths[0])
			r := Check(context.Background(), snap, paths, nil, nil)
			if !slices.Equal(r.Allow, paths[1:]) {
				t.Fatalf("allowed %v, want only %v", r.Allow, paths[1:])
			}
			if r.Failed != 1 || len(r.Lines) != 1 || r.Lines[0] != "1 file changed since the scan" {
				t.Fatalf("r = %+v", r)
			}
		})
	}
}

func TestLargePathNotInTheScanIsRefused(t *testing.T) {
	snap, paths := large(t, t.TempDir())
	stray := filepath.Join(t.TempDir(), "other")
	write(t, stray, "x", base)
	r := Check(context.Background(), snap, append([]string{stray, "/etc/hosts"}, paths[:1]...), nil, nil)
	if !slices.Equal(r.Allow, paths[:1]) || r.Failed != 2 {
		t.Fatalf("r = %+v", r)
	}
	if !slices.Contains(r.Lines, "2 items were not part of that scan") {
		t.Fatalf("lines = %v", r.Lines)
	}
	// the spelling must match exactly
	r = Check(context.Background(), snap, []string{paths[0] + "/", filepath.Dir(paths[0]) + "/./a"}, nil, nil)
	if len(r.Allow) != 0 || r.Failed != 2 {
		t.Fatalf("respelled paths passed: %+v", r)
	}
}

func TestDuplicatePathsAreOneRequest(t *testing.T) {
	snap, paths := large(t, t.TempDir())
	r := Check(context.Background(), snap, []string{paths[0], paths[0]}, nil, nil)
	if !slices.Equal(r.Allow, paths[:1]) || r.Failed != 0 {
		t.Fatalf("r = %+v", r)
	}
}

// dupes builds a snapshot of one group of identical files.
func dupeGroup(t *testing.T, dir string, names ...string) (*snapshot.Snap, []string) {
	t.Helper()
	var files []model.DuplicateFile
	var paths []string
	for _, n := range names {
		p := filepath.Join(dir, n)
		write(t, p, "same content", base)
		files = append(files, model.DuplicateFile{Path: p, Modified: base.UnixMilli()})
		paths = append(paths, p)
	}
	return snapshot.Dupes("scan", []model.DuplicateGroup{{Hash: sum("same content"), Size: int64(len("same content")), Files: files}}), paths
}

func TestDupesKeptCopyVerified(t *testing.T) {
	snap, p := dupeGroup(t, t.TempDir(), "keep", "x", "y")
	var hashed []string
	hash := func(ctx context.Context, path string) (string, error) {
		hashed = append(hashed, path)
		return realHash(ctx, path)
	}
	r := Check(context.Background(), snap, p[1:], hash, nil)
	if !slices.Equal(r.Allow, p[1:]) || r.Failed != 0 {
		t.Fatalf("r = %+v", r)
	}
	if !slices.Equal(hashed, p[:1]) {
		t.Fatalf("hashed %v, want only the kept copy", hashed)
	}
}

func TestDupesAllMembersRequestedIsRefused(t *testing.T) {
	snap, p := dupeGroup(t, t.TempDir(), "a", "b")
	r := Check(context.Background(), snap, p, realHash, nil)
	if len(r.Allow) != 0 || r.Failed != 2 {
		t.Fatalf("r = %+v", r)
	}
	if !slices.Contains(r.Lines, "2 files not moved: no copy would have been left") {
		t.Fatalf("lines = %v", r.Lines)
	}
}

func TestDupesKeptCopyProblems(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, kept string)
	}{
		{"missing", func(t *testing.T, kept string) { os.Remove(kept) }},
		{"size changed", func(t *testing.T, kept string) { write(t, kept, "edited content!", base) }},
		{"mtime changed", func(t *testing.T, kept string) { os.Chtimes(kept, base.Add(time.Hour), base.Add(time.Hour)) }},
		{"rewritten in place with the same size and mtime", func(t *testing.T, kept string) {
			write(t, kept, "other conten", base) // 12 bytes, like "same content"
		}},
		{"replaced by a symlink", func(t *testing.T, kept string) {
			other := kept + ".real"
			write(t, other, "same content", base)
			os.Remove(kept)
			os.Symlink(other, kept)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			snap, p := dupeGroup(t, t.TempDir(), "keep", "x", "y")
			c.mutate(t, p[0])
			r := Check(context.Background(), snap, p[1:], realHash, nil)
			if len(r.Allow) != 0 || r.Failed != 2 {
				t.Fatalf("a copy was trashed without a verified kept copy: %+v", r)
			}
		})
	}
}

func TestDupesSameSizeRewriteIsCaughtByTheHash(t *testing.T) {
	snap, p := dupeGroup(t, t.TempDir(), "keep", "x")
	write(t, p[0], "other conten", base)
	fi, _ := os.Lstat(p[0])
	if fi.Size() != int64(len("same content")) || fi.ModTime().UnixMilli() != base.UnixMilli() {
		t.Fatal("the rewrite must look unchanged to a stamp check")
	}
	r := Check(context.Background(), snap, p[1:], realHash, nil)
	if len(r.Allow) != 0 {
		t.Fatalf("hash did not catch the rewrite: %+v", r)
	}
	// without a hash function nothing passes at all
	if r := Check(context.Background(), snap, p[1:], nil, nil); len(r.Allow) != 0 {
		t.Fatalf("no hash function, yet allowed: %+v", r)
	}
}

func TestDupesFirstKeptBadSecondGood(t *testing.T) {
	snap, p := dupeGroup(t, t.TempDir(), "k1", "k2", "x")
	write(t, p[0], "tampered!!!!", base)
	var hashed []string
	hash := func(ctx context.Context, path string) (string, error) {
		hashed = append(hashed, path)
		return realHash(ctx, path)
	}
	r := Check(context.Background(), snap, p[2:], hash, nil)
	if !slices.Equal(r.Allow, p[2:]) || r.Failed != 0 {
		t.Fatalf("r = %+v", r)
	}
	if !slices.Equal(hashed, p[:2]) {
		t.Fatalf("hashed %v, want both kept copies in order", hashed)
	}
}

func TestDupesRequestedFileChangedIsSkippedAlone(t *testing.T) {
	snap, p := dupeGroup(t, t.TempDir(), "keep", "x", "y")
	write(t, p[1], "edited since", base.Add(time.Minute))
	r := Check(context.Background(), snap, p[1:], realHash, nil)
	if !slices.Equal(r.Allow, p[2:]) || r.Failed != 1 {
		t.Fatalf("r = %+v", r)
	}
}

func TestDupesPathFromAnotherGroupOrNotInScan(t *testing.T) {
	snap, p := dupeGroup(t, t.TempDir(), "keep", "x")
	r := Check(context.Background(), snap, []string{p[1], "/etc/hosts"}, realHash, nil)
	if !slices.Equal(r.Allow, p[1:]) || r.Failed != 1 {
		t.Fatalf("r = %+v", r)
	}
}

func TestDupesCancelledMidHash(t *testing.T) {
	snap, p := dupeGroup(t, t.TempDir(), "keep", "x", "y")
	ctx, cancel := context.WithCancel(context.Background())
	hash := func(ctx context.Context, path string) (string, error) {
		cancel()
		return "", ctx.Err()
	}
	r := Check(ctx, snap, p[1:], hash, nil)
	if !r.Cancelled || len(r.Allow) != 0 || r.Failed != 2 {
		t.Fatalf("r = %+v", r)
	}
}

func TestDupesReportsProgress(t *testing.T) {
	snap, p := dupeGroup(t, t.TempDir(), "keep", "x")
	var phases []string
	rep := func(phase string, done, total int, current string) { phases = append(phases, phase) }
	Check(context.Background(), snap, p[1:], realHash, rep)
	if len(phases) == 0 || phases[0] != "Checking copies" {
		t.Fatalf("phases = %v", phases)
	}
}

func TestWrongSnapshotKindAllowsNothing(t *testing.T) {
	snap := snapshot.Apps("scan", nil)
	r := Check(context.Background(), snap, []string{"/a"}, realHash, nil)
	if len(r.Allow) != 0 || r.Failed != 1 {
		t.Fatalf("r = %+v", r)
	}
}
