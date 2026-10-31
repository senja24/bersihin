package junk

import (
	"context"
	"os"
	"path/filepath"
	"strings"
)

// RecentItemsLastUsed returns a Scanner.LastUsed for Windows that reads the
// shortcuts File Explorer keeps in the Recent Items folder (dir, that is
// %APPDATA%\Microsoft\Windows\Recent): opening a file or folder through the
// shell writes "<name>.lnk" there, dated when it was opened. NTFS often does
// not update access times, so this is the one sign that a download was opened
// and never changed.
//
// It only ever keeps more: a shortcut is matched by name alone (with and
// without the extension), so a same-named file opened elsewhere keeps this
// one too. Where Recent Items are turned off or the folder cannot be read,
// there is no extra signal and the dates of the file itself decide, as before.
func RecentItemsLastUsed(dir string) func(ctx context.Context, paths []string) (map[string]int64, error) {
	if dir == "" || !filepath.IsAbs(dir) {
		return nil
	}
	return func(ctx context.Context, paths []string) (map[string]int64, error) {
		list, err := os.ReadDir(dir)
		if err != nil {
			return nil, nil
		}
		opened := map[string]int64{}
		for _, e := range list {
			name := e.Name()
			if !strings.EqualFold(filepath.Ext(name), ".lnk") || e.IsDir() {
				continue
			}
			fi, err := e.Info()
			if err != nil {
				continue
			}
			k := strings.ToLower(strings.TrimSuffix(name, filepath.Ext(name)))
			opened[k] = max(opened[k], fi.ModTime().UnixMilli())
		}
		dates := make(map[string]int64, len(paths))
		for _, p := range paths {
			base := strings.ToLower(filepath.Base(p))
			stem := strings.TrimSuffix(base, filepath.Ext(base))
			dates[p] = max(opened[base], opened[stem])
		}
		return dates, ctx.Err()
	}
}
