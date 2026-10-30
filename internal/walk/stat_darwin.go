//go:build darwin

package walk

import (
	"io/fs"
	"syscall"
)

// sfDataless is SF_DATALESS from <sys/stat.h> (golang.org/x/sys/unix has the
// same value, 0x40000000): the file is an iCloud placeholder without local data.
const sfDataless = 0x40000000

// extra reads what os.FileInfo does not expose: identity, allocated blocks,
// access and birth time (epoch milliseconds).
func extra(fi fs.FileInfo) (e Extra) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return e
	}
	e.Dev, e.Ino, e.Blocks, e.Nlink = uint64(st.Dev), st.Ino, st.Blocks, uint64(st.Nlink)
	e.ATime = st.Atimespec.Sec*1000 + st.Atimespec.Nsec/1e6
	e.BTime = st.Birthtimespec.Sec*1000 + st.Birthtimespec.Nsec/1e6
	e.CTime = st.Ctimespec.Sec*1000 + st.Ctimespec.Nsec/1e6
	e.HasBlocks = true
	e.Dataless = st.Flags&sfDataless != 0
	return e
}
