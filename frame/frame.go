// Package frame holds the per-frame state of the buffer pool:
// which page it caches, whether it is dirty, and its pin count.
// It also owns the write-back counter and the eviction-candidate rule.
package frame

// NoPage marks an empty frame (no page resident).
const NoPage = -1

// Frame caches at most one page.
type Frame struct {
	PageID int  // resident page, or NoPage when empty
	Dirty  bool // modified since load; must be written back before eviction
	Pin    int  // >0 means in use and not evictable
}

// New returns an empty frame.
func New() Frame { return Frame{PageID: NoPage} }

// Empty reports whether the frame holds no page.
func (f Frame) Empty() bool { return f.PageID == NoPage }

// Evictable reports whether the frame may be evicted: occupied and unpinned.
func (f Frame) Evictable() bool { return !f.Empty() && f.Pin == 0 }

// WriteCounter counts simulated write-backs of dirty pages.
type WriteCounter struct{ n int }

// Add records one write-back.
func (c *WriteCounter) Add() { c.n++ }

// Get returns the accumulated number of write-backs.
func (c *WriteCounter) Get() int { return c.n }
