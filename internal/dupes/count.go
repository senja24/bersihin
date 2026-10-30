package dupes

import "sync/atomic"

// countdown returns a func that counts calls: 1, 2, 3… It is safe for concurrent use.
func countdown(int) func() int {
	var n atomic.Int64
	return func() int { return int(n.Add(1)) }
}
