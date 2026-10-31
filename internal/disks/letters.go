package disks

import "strings"

// Drive types as GetDriveTypeW reports them.
const (
	DriveRemovable = 2
	DriveFixed     = 3
	DriveRemote    = 4
	DriveCDROM     = 5
	DriveRAMDisk   = 6
)

// LetterVolumes turns the drive letter mask of GetLogicalDrives (bit 0 is
// A:) into the volumes worth listing: fixed local drives only, so no
// removable, network, optical or RAM disk. The drive of home is the system
// volume. kind returns the drive type of a root such as `C:\`, label its
// volume label ("" when it has none). Pure, so it is tested on any OS.
func LetterVolumes(mask uint32, home string, kind func(root string) uint32, label func(root string) string) []Volume {
	sys := ""
	if len(home) >= 2 && home[1] == ':' {
		sys = strings.ToUpper(home[:1])
	}
	var out []Volume
	for i := range 26 {
		if mask&(1<<i) == 0 {
			continue
		}
		letter := string(rune('A' + i))
		root := letter + `:\`
		if kind(root) != DriveFixed {
			continue
		}
		name := strings.TrimSpace(label(root))
		if name == "" {
			name = "Local Disk"
		}
		out = append(out, Volume{Mount: root, Label: name + " (" + letter + ":)", System: letter == sys})
	}
	return out
}

// SystemRoot is the root of the drive holding home (`C:\`), "" when home
// does not start with a drive letter.
func SystemRoot(home string) string {
	if len(home) >= 2 && home[1] == ':' && (home[0]|0x20) >= 'a' && (home[0]|0x20) <= 'z' {
		return strings.ToUpper(home[:1]) + `:\`
	}
	return ""
}
