//go:build !windows

package junk

// systemBin: there is no Recycle Bin off Windows.
var systemBin RecycleBin
