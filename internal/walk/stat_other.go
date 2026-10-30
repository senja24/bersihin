//go:build linux

package walk

import (
	"io/fs"
	"syscall"
)

// extra reads what os.FileInfo does not expose. On Linux (for tests)
// there is no birth time.
func extra(fi fs.FileInfo) (e Extra) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return e
	}
	// HasBlocks stays false: the "no local blocks" test is for macOS iCloud placeholders only.
	e.Dev, e.Ino, e.Blocks, e.Nlink = uint64(st.Dev), uint64(st.Ino), int64(st.Blocks), uint64(st.Nlink)
	e.ATime = int64(st.Atim.Sec)*1000 + int64(st.Atim.Nsec)/1e6
	e.CTime = int64(st.Ctim.Sec)*1000 + int64(st.Ctim.Nsec)/1e6
	return e
}
