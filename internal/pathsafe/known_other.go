//go:build !windows

package pathsafe

func shellKnownFolders() []string { return nil }
