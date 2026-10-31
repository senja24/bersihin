//go:build !windows

package large

// appData is the folder of the home folder that holds the apps' own data.
const appData = "Library"

// driveSkipPrefix starts the names of a data volume's top folders that
// belong to the OS ("." always does); "." again where there is no other.
const driveSkipPrefix = "."
