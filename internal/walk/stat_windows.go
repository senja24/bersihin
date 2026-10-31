//go:build windows

package walk

import (
	"io/fs"
	"os"
	"strings"
	"syscall"
	"unsafe"
)

// File attributes of a cloud file whose data is not on the disk (OneDrive
// "online only", and other cloud sync providers): reading it downloads it.
const (
	fileAttributeOffline            = 0x00001000
	fileAttributeRecallOnOpen       = 0x00040000
	fileAttributeRecallOnDataAccess = 0x00400000

	fileReadAttributes = 0x0080
	fileBasicInfo      = 0 // FILE_INFO_BY_HANDLE_CLASS FileBasicInfo
)

var procGetFileInformationByHandleEx = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFileInformationByHandleEx")

// fileBasicInformation is FILE_BASIC_INFO.
type fileBasicInformation struct {
	CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
	FileAttributes                                          uint32
	_                                                       uint32
}

// idInfo is an lstat result with the identity read through a handle.
type idInfo struct {
	fs.FileInfo
	id Extra
}

// msec turns a FILETIME into epoch milliseconds, 0 for an unset time.
func msec(ft syscall.Filetime) int64 {
	if ft.HighDateTime == 0 && ft.LowDateTime == 0 {
		return 0
	}
	return ft.Nanoseconds() / 1e6
}

// extra reads what os.FileInfo does not expose: access and creation time
// and the cloud placeholder attributes, plus the identity when fi came from
// Lstat. The allocated size is not known (HasBlocks stays false).
func extra(fi fs.FileInfo) (e Extra) {
	if id, ok := fi.(*idInfo); ok {
		e, fi = id.id, id.FileInfo
	}
	d, ok := fi.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return e
	}
	e.ATime = msec(d.LastAccessTime)
	e.BTime = msec(d.CreationTime)
	e.Dataless = d.FileAttributes&(fileAttributeOffline|fileAttributeRecallOnOpen|fileAttributeRecallOnDataAccess) != 0
	return e
}

// longPath gives a long drive letter path the \\?\ form CreateFileW needs
// past MAX_PATH; os does the same for its own calls.
func longPath(p string) string {
	if len(p) < 248 || strings.HasPrefix(p, `\\`) || len(p) < 3 || p[1] != ':' || p[2] != '\\' {
		return p
	}
	return `\\?\` + p
}

// identity opens p itself (a reparse point is opened, not followed) with
// no access to its data, and reads the volume serial number, the file
// index, the link count and the change time. Dev is the serial number with
// bit 32 set, so that it is never 0 (0 means unknown to the callers).
func identity(p string) (Extra, bool) {
	var e Extra
	name, err := syscall.UTF16PtrFromString(longPath(p))
	if err != nil {
		return e, false
	}
	h, err := syscall.CreateFile(name, fileReadAttributes,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return e, false
	}
	defer syscall.CloseHandle(h)
	return handleIdentity(h)
}

// handleIdentity reads the identity of an open file, see identity.
func handleIdentity(h syscall.Handle) (Extra, bool) {
	var e Extra
	var bi syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(h, &bi); err != nil {
		return e, false
	}
	e.Dev = uint64(bi.VolumeSerialNumber) | 1<<32
	e.Ino = uint64(bi.FileIndexHigh)<<32 | uint64(bi.FileIndexLow)
	e.Nlink = uint64(bi.NumberOfLinks)
	var basic fileBasicInformation
	if r, _, _ := procGetFileInformationByHandleEx.Call(uintptr(h), fileBasicInfo, uintptr(unsafe.Pointer(&basic)), unsafe.Sizeof(basic)); r != 0 && basic.ChangeTime > 0 {
		e.CTime = msec(syscall.Filetime{LowDateTime: uint32(basic.ChangeTime), HighDateTime: uint32(basic.ChangeTime >> 32)})
	}
	return e, true
}

// HandleNlink returns the hard link count of an open file, 0 when unknown.
func HandleNlink(f *os.File) uint64 {
	e, ok := handleIdentity(syscall.Handle(f.Fd()))
	if !ok {
		return 0
	}
	return e.Nlink
}

func lstat(p string) (fs.FileInfo, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if id, ok := identity(p); ok {
		return &idInfo{FileInfo: fi, id: id}, nil
	}
	return fi, nil
}

// dirInfo is the folder's lstat with its identity: the walk needs its device.
func dirInfo(p string, _ fs.DirEntry) (fs.FileInfo, error) { return lstat(p) }

// fileInfo is the file's lstat, with its identity only when asked: that
// costs an open per file.
func fileInfo(p string, d fs.DirEntry, identity bool) (fs.FileInfo, error) {
	if identity {
		return lstat(p)
	}
	return d.Info()
}
