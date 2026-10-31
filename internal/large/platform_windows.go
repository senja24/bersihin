//go:build windows

package large

// appData is the folder of the profile that holds the apps' own data.
const appData = "AppData"

// driveSkipPrefix: $Recycle.Bin, $WinREAgent and the other OS folders.
const driveSkipPrefix = "$"

func init() {
	for _, n := range []string{
		"windows", "program files", "program files (x86)", "programdata", "system volume information",
		"recovery", "perflogs", "boot", "efi", "config.msi", "msocache", "windowsapps", "wpsystem",
		"documents and settings", "onedrivetemp",
	} {
		driveSkipTop[n] = true
	}
}
