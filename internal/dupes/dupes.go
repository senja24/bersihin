// Package dupes finds byte-identical files: group by size, then by a hash of
// the first and last 64 KiB, then by a hash of the whole content.
package dupes

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"dev.agus.bersihin/internal/model"
	"dev.agus.bersihin/internal/pathsafe"
	"dev.agus.bersihin/internal/progress"
	"dev.agus.bersihin/internal/walk"
)

const (
	// MinSize is the smallest file considered.
	MinSize   = 1024 * 1024
	edge      = 64 * 1024
	maxFiles  = 400_000
	maxGroups = 200
)

// Hidden folders, dependency trees and macOS packages (Photos/Music libraries,
// apps) hold files the user does not manage directly; removing a "duplicate"
// inside them breaks the package.
var skipDirs = map[string]bool{"node_modules": true, "__pycache__": true, "bower_components": true}

// Entry is a candidate file.
type Entry struct {
	Path     string
	Size     int64
	Modified int64
}

// Roots returns the folders searched: the user's own top folders, without any
// that sits inside another.
func Roots(home string) []string {
	var all []string
	for _, n := range []string{"Desktop", "Documents", "Downloads", "Pictures", videos, "Music"} {
		all = append(all, filepath.Join(home, n))
	}
	var out []string
	for _, p := range all {
		nested := false
		for _, o := range all {
			if o != p && strings.HasPrefix(strings.ToLower(p), strings.ToLower(o)+string(filepath.Separator)) {
				nested = true
			}
		}
		if !nested && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

// eligible: big enough and really stored here (a macOS iCloud placeholder
// has a size but no local data, and reading it would download it).
func eligible(f walk.File) bool {
	return f.Size >= MinSize && !f.IsDataless()
}

// Collect walks the roots (never following links) and returns the files that
// can be duplicates of something: at least MinSize, not hidden, not inside a
// package or dependency folder, really stored on this disk (macOS iCloud
// placeholders have no local blocks, and reading them would download them),
// and once per inode (a hard link or a file reached through two roots is not a
// second copy). seen reports progress.
func Collect(ctx context.Context, roots []string, seen func(n int, dir string)) []Entry {
	var files []Entry
	inodes := map[[2]uint64]bool{}
	n := 0
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	for _, root := range roots {
		walk.Walk(wctx, root, walk.Options{
			SkipDir: func(_, name string, _ int) bool {
				return strings.HasPrefix(name, ".") || skipDirs[strings.ToLower(name)] || pathsafe.IsPackageName(name)
			},
			OnFile: func(f walk.File) {
				if strings.HasPrefix(f.Name, ".") {
					return
				}
				n++
				if !eligible(f) {
					return
				}
				if f.Ino == 0 {
					// Windows: the walk reads no file index, ask for this file's
					if fi, err := walk.Lstat(f.Path); err == nil {
						f.Dev, f.Ino = walk.Dev(fi), walk.Ino(fi)
					}
				}
				if f.Ino != 0 {
					key := [2]uint64{f.Dev, f.Ino}
					if inodes[key] {
						return
					}
					inodes[key] = true
				}
				files = append(files, Entry{Path: f.Path, Size: f.Size, Modified: f.MTime})
				if len(files) >= maxFiles {
					cancel()
				}
			},
			OnTick: func(_ int, dir string) {
				if seen != nil {
					seen(n, dir)
				}
			},
		})
	}
	return files
}

type group[K comparable] struct {
	key     K
	entries []Entry
}

func groupBy[K comparable](entries []Entry, keys []K, ok []bool) []group[K] {
	m := map[K][]Entry{}
	var order []K
	for i, e := range entries {
		if !ok[i] {
			continue
		}
		if _, seen := m[keys[i]]; !seen {
			order = append(order, keys[i])
		}
		m[keys[i]] = append(m[keys[i]], e)
	}
	var out []group[K]
	for _, k := range order {
		if len(m[k]) > 1 {
			out = append(out, group[K]{k, m[k]})
		}
	}
	return out
}

func flatten[K comparable](groups []group[K]) []Entry {
	var out []Entry
	for _, g := range groups {
		out = append(out, g.entries...)
	}
	return out
}

// PartialHash hashes the first and last 64 KiB of a file.
func PartialHash(path string, size int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha1.New()
	head := make([]byte, edge)
	n, err := f.ReadAt(head, 0)
	if err != nil && err != io.EOF {
		return "", err
	}
	h.Write(head[:n])
	tail := make([]byte, edge)
	n, err = f.ReadAt(tail, max(0, size-edge))
	if err != nil && err != io.EOF {
		return "", err
	}
	h.Write(tail[:n])
	return hex.EncodeToString(h.Sum(nil)), nil
}

// FullHash is the SHA-256 of the whole content. It stops early when ctx ends.
func FullHash(ctx context.Context, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return hashOf(ctx, f)
}

// ErrNotRegular: the path is a link, a folder or was swapped while opening.
var ErrNotRegular = errors.New("not a regular file")

// FullHashRegular is FullHash for a path that must be a regular file itself:
// a link is never followed, and a file swapped between the check and the open
// is refused (the opened file must be the one the Lstat saw).
func FullHashRegular(ctx context.Context, path string) (string, error) {
	li, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !li.Mode().IsRegular() {
		return "", ErrNotRegular
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	oi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !oi.Mode().IsRegular() || !os.SameFile(li, oi) {
		return "", ErrNotRegular
	}
	return hashOf(ctx, f)
}

func hashOf(ctx context.Context, f *os.File) (string, error) {
	h := sha256.New()
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Find narrows entries down to groups of identical files, biggest waste first.
func Find(ctx context.Context, entries []Entry, rep progress.Reporter) ([]model.DuplicateGroup, error) {
	// Only files sharing an exact size can be duplicates.
	sizes := make([]int64, len(entries))
	oks := make([]bool, len(entries))
	for i, e := range entries {
		sizes[i], oks[i] = e.Size, true
	}
	cands := flatten(groupBy(entries, sizes, oks))

	partial := make([]string, len(cands))
	pok := make([]bool, len(cands))
	done := countdown(len(cands))
	walk.ForEach(ctx, len(cands), 6, func(i int) {
		h, err := PartialHash(cands[i].Path, cands[i].Size)
		if err == nil {
			partial[i], pok[i] = fmt.Sprintf("%d:%s", cands[i].Size, h), true
		}
		rep.Call("Comparing files", done(), len(cands), cands[i].Path)
	})
	if ctx.Err() != nil {
		return nil, progress.ErrCancelled
	}
	full := flatten(groupBy(cands, partial, pok))

	hashes := make([]string, len(full))
	hok := make([]bool, len(full))
	done = countdown(len(full))
	walk.ForEach(ctx, len(full), 4, func(i int) {
		h, err := FullHash(ctx, full[i].Path)
		if err == nil {
			hashes[i], hok[i] = h, true
		}
		rep.Call("Checking contents", done(), len(full), full[i].Path)
	})
	if ctx.Err() != nil {
		return nil, progress.ErrCancelled
	}

	// equal SHA-256 and equal size: byte-identical files
	groups := []model.DuplicateGroup{}
	for _, g := range groupBy(full, hashes, hok) {
		dg := model.DuplicateGroup{Hash: g.key, Size: g.entries[0].Size}
		for _, e := range g.entries {
			dg.Files = append(dg.Files, model.DuplicateFile{Path: e.Path, Modified: e.Modified})
		}
		slices.SortStableFunc(dg.Files, func(a, b model.DuplicateFile) int {
			switch {
			case a.Modified > b.Modified:
				return -1
			case a.Modified < b.Modified:
				return 1
			}
			return strings.Compare(a.Path, b.Path)
		})
		groups = append(groups, dg)
	}
	waste := func(g model.DuplicateGroup) int64 { return g.Size * int64(len(g.Files)-1) }
	slices.SortStableFunc(groups, func(a, b model.DuplicateGroup) int {
		switch {
		case waste(a) > waste(b):
			return -1
		case waste(a) < waste(b):
			return 1
		}
		return strings.Compare(a.Hash, b.Hash)
	})
	if len(groups) > maxGroups {
		groups = groups[:maxGroups]
	}
	return groups, nil
}
