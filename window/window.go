// Package window is a fixed-capacity ring buffer of recent bytes.
package window

type Window struct {
	cap   int
	buf   []byte
	start int
	size  int
}

func New(capacity int) *Window { return &Window{} }

func (w *Window) Push(b byte)    {}
func (w *Window) Len() int       { return 0 }
func (w *Window) Cap() int       { return 0 }
func (w *Window) At(dist int) byte { return 0 }

func (w *Window) Write(p []byte) int { return len(p) }
