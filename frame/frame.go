// Package frame holds the per-frame state of the buffer pool: which page a
// frame caches, whether it is dirty, and its pin count. It depends on no
// other package.
package frame

// EmptyPage marks a frame that currently caches no page.
const EmptyPage = -1

// New returns an empty, clean, unpinned frame.
func New() Frame { return Frame{pageID: EmptyPage} }

// Frame is one buffer-pool frame: (pageId, dirty, pin). Use New; the struct is
// exported only so pools can hold slices of it.
type Frame struct {
	pageID int
	dirty  bool
	pin    int
}

// WriteCounter counts write-backs performed when dirty frames are evicted.
// It is a plain counter; the caller (pool) serializes access.
type WriteCounter struct{ n int }

// PageID returns the cached page id, or EmptyPage when the frame is empty.
func (f *Frame) PageID() int { return f.pageID }

// Empty reports whether the frame caches no page.
func (f *Frame) Empty() bool { return f.pageID == EmptyPage }

// Dirty reports whether the cached page has unwritten modifications.
func (f *Frame) Dirty() bool { return f.dirty }

// Pins returns the current pin count.
func (f *Frame) Pins() int { return f.pin }

// Hold increments the pin count and returns the new value.
func (f *Frame) Hold() int {
	f.pin++
	return f.pin
}

// Release decrements the pin count. It fails without changing anything when
// the frame is already unpinned, so pin can never go negative.
func (f *Frame) Release() bool {
	if f.pin == 0 {
		return false
	}
	f.pin--
	return true
}

// MarkDirty marks the cached page modified.
func (f *Frame) MarkDirty() { f.dirty = true }

// Evictable reports whether the frame is an eviction candidate: it caches a
// page and nobody holds a pin on it.
func (f *Frame) Evictable() bool { return !f.Empty() && f.pin == 0 }

// Load installs pageID into the frame as a freshly loaded, clean page.
func (f *Frame) Load(pageID int) {
	f.pageID = pageID
	f.dirty = false
	f.pin = 0
}

// Flush writes a dirty page back before eviction, counting the write-back.
// A pinned frame is never flushed because only eviction calls Flush.
func (f *Frame) Flush(w *WriteCounter) {
	if f.dirty {
		w.Incr()
		f.dirty = false
	}
}

// Incr records one write-back.
func (w *WriteCounter) Incr() { w.n++ }

// Get returns the cumulative number of write-backs.
func (w *WriteCounter) Get() int { return w.n }
