package junk

import (
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"sync"
	"time"

	"dev.agus.bersihin/internal/parse"
	"dev.agus.bersihin/internal/sys"
	"dev.agus.bersihin/internal/walk"
)

// MdlsLastUsed returns a Scanner.LastUsed that asks Spotlight (mdls) for the
// "last used" date of files, 50 files per call. A call that fails, or prints
// something that is not a date or "(null)", fails the whole lookup, so that
// the files are kept rather than judged on a date nobody could read. Where
// there is no mdls at all (not a Mac) the lookup finds no dates and succeeds.
// Nil run gives nil: no extra signal.
func MdlsLastUsed(run sys.Runner) func(ctx context.Context, paths []string) (map[string]int64, error) {
	if run == nil {
		return nil
	}
	return func(ctx context.Context, paths []string) (map[string]int64, error) {
		dates := map[string]int64{}
		var (
			mu     sync.Mutex
			bad    error
			absent bool
		)
		const per = 50
		n := (len(paths) + per - 1) / per
		walk.ForEach(ctx, n, 2, func(i int) {
			part := paths[i*per : min((i+1)*per, len(paths))]
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			res, err := run.Run(cctx, "/usr/bin/mdls", append([]string{"-raw", "-name", "kMDItemLastUsedDate"}, part...)...)
			vals, unparsed := parse.MdlsDatesChecked(res.Stdout, len(part))
			mu.Lock()
			defer mu.Unlock()
			if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
				absent = true
				return
			}
			if err != nil || vals == nil || unparsed > 0 {
				bad = errors.New("last used dates could not be read")
				return
			}
			for j, p := range part {
				dates[p] = vals[j]
			}
		})
		if absent && bad == nil {
			return nil, nil
		}
		if bad == nil {
			bad = ctx.Err()
		}
		if bad == nil && len(dates) != len(paths) {
			bad = errors.New("last used dates could not be read")
		}
		return dates, bad
	}
}
