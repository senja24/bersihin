package junk

import "dev.agus.bersihin/internal/model"

// BinSize is what the Recycle Bin holds, over every drive.
type BinSize struct {
	Size, Items int64
}

// RecycleBin is the Windows Recycle Bin: SHQueryRecycleBinW and
// SHEmptyRecycleBinW of every drive, in recyclebin_windows.go.
type RecycleBin interface {
	Query() (BinSize, error)
	Empty() error
}

func (s *Scanner) bin() RecycleBin {
	if s.RecycleBin != nil {
		return s.RecycleBin
	}
	return systemBin
}

func (s *Scanner) binSize() (BinSize, bool) {
	b := s.bin()
	if b == nil {
		return BinSize{}, false
	}
	got, err := b.Query()
	if err != nil || got.Size < 0 || got.Items < 0 {
		return BinSize{}, false
	}
	return got, true
}

// emptyBin empties the Recycle Bin, unless it holds more than the scan saw:
// then something was deleted since, which the user has not seen offered, and
// it is left as it is.
func (s *Scanner) emptyBin(res *model.CleanResult) {
	before, ok := s.binSize()
	switch {
	case !ok:
		res.Failed++
		res.Skipped = append(res.Skipped, "The Recycle Bin could not be read")
		return
	case before.Items == 0:
		return
	case s.RecycleSeen != nil && (before.Items > s.RecycleSeen.Items || before.Size > s.RecycleSeen.Size):
		res.Skipped = append(res.Skipped, "The Recycle Bin changed since the scan, so it was left as it is. Scan again to empty it")
		return
	}
	if err := s.bin().Empty(); err != nil {
		res.Failed++
		res.Skipped = append(res.Skipped, "Windows did not empty the Recycle Bin")
		return
	}
	after, ok := s.binSize()
	if !ok {
		after = BinSize{}
	}
	res.Freed += max(before.Size-after.Size, 0)
	res.Removed += int(max(before.Items-after.Items, 0))
	if after.Items > 0 {
		res.Skipped = append(res.Skipped, files(int(after.Items), "item", "items")+" stayed in the Recycle Bin")
	}
}
