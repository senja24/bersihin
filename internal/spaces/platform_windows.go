//go:build windows

package spaces

// appData is the folder of the profile that holds the apps' own data.
const appData = "AppData"

func init() {
	for _, n := range []string{
		"windows", "program files", "program files (x86)", "programdata", "recovery", "perflogs", "boot", "efi",
		"$windows.~bt", "$windows.~ws", "$winreagent", "$sysreset", "$getcurrent", "windowsapps", "wpsystem",
	} {
		systemTop[n] = true
	}
	// the Recycle Bin and volume metadata; a junction (Documents and
	// Settings) is never entered anyway
	for _, n := range []string{"$recycle.bin", "system volume information", "config.msi", "documents and settings"} {
		skipTop[n] = true
	}
}
