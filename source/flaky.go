package source

import "sync"

// Flaky wraps a Source to inject failures deterministically:
//
//   - every read is forced to return at most MaxChunk bytes (short reads);
//   - after FailAfter successful read calls, the next read returns Err;
//   - the wrapped source can be mutated between calls so its length changes
//     in the middle of an assembly.
type Flaky struct {
	Inner Source
	// MaxChunk caps bytes returned by one ReadAt call. <=0 means no cap.
	MaxChunk int
	// FailAfter is the number of successful reads before Err is injected.
	// Zero means never fail. The counter is per Flaky instance.
	FailAfter int
	// Err is returned on the injected failing read.
	Err error

	mu       sync.Mutex
	readNum  int
}

// Size reports the wrapped source's current size.
func (f *Flaky) Size() int64 { return f.Inner.Size() }

// ReadAt implements Source with the injected chunk limit and failure.
func (f *Flaky) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	f.readNum++
	if f.FailAfter > 0 && f.readNum > f.FailAfter {
		err := f.Err
		f.mu.Unlock()
		return 0, err
	}
	f.mu.Unlock()

	if f.MaxChunk > 0 && len(p) > f.MaxChunk {
		p = p[:f.MaxChunk]
	}
	return f.Inner.ReadAt(p, off)
}
