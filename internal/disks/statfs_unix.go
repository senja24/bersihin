//go:build unix

package disks

import "syscall"

func statfs(p string) (total, free int64, err error) {
	var s syscall.Statfs_t
	if err := syscall.Statfs(p, &s); err != nil {
		return 0, 0, err
	}
	bsize := uint64(s.Bsize)
	return int64(uint64(s.Blocks) * bsize), int64(uint64(s.Bavail) * bsize), nil
}
