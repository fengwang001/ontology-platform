// Package op defines the volcano-model operator interface and the driver.
package op

import (
	"context"
	"errors"
	"io"
	"sync"
)

// Row is the unit of data exchanged between operators. Projection and
// sorting never alias payloads across operator boundaries unexpectedly:
// payload is treated as read-only after emission.
type Row struct {
	Key     int
	Payload string
	Seq     int // input ordinal, used for stable sorting
}

// Operator is the three-phase volcano interface. Open prepares resources,
// Next returns the next row or io.EOF, Close releases everything exactly
// once (repeated calls are idempotent).
type Operator interface {
	Open(ctx context.Context) error
	Next(ctx context.Context) (Row, error)
	Close() error
}

// Sentinel errors. The four spill-corruption categories live in package
// sorter; the shared ones are here.
var (
	// ErrClosed is returned by Next when Close races with / precedes it.
	ErrClosed = errors.New("op: operator closed")
	// ErrFailOpen is returned by operators configured to fail Open.
	ErrFailOpen = errors.New("op: injected open failure")
)

// Run drains root to completion (or error) and always closes root exactly
// once. onRow is invoked for every produced row; returning an error from it
// aborts the pull and still closes the tree.
func Run(ctx context.Context, root Operator, onRow func(Row) error) (retErr error) {
	if err := root.Open(ctx); err != nil {
		_ = root.Close()
		return err
	}
	defer func() {
		if cerr := root.Close(); cerr != nil && retErr == nil {
			retErr = cerr
		}
	}()
	for {
		row, err := root.Next(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if onRow != nil {
			if err := onRow(row); err != nil {
				return err
			}
		}
	}
}

// Gate gives one operator safe concurrent Close/Next semantics without making
// the whole tree concurrency-safe: Close marks shutdown and waits for in-flight
// Next calls to leave; Next checks the gate while holding no internal lock.
type Gate struct {
	mu       sync.Mutex
	closed   bool
	inFlight sync.WaitGroup
	stop     chan struct{}
	once     sync.Once
}

// Stop is closed by Shutdown.
func (g *Gate) Stop() <-chan struct{} {
	g.once.Do(func() { g.stop = make(chan struct{}) })
	return g.stop
}

// Enter is called at the start of Next. It returns false when the operator is
// already closed; callers then return ErrClosed.
func (g *Gate) Enter() bool {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return false
	}
	g.inFlight.Add(1)
	g.mu.Unlock()
	return true
}

// Leave marks an in-flight Next as finished.
func (g *Gate) Leave() { g.inFlight.Done() }

// Shutdown closes the stop channel exactly once and waits for in-flight Next
// calls to exit. It reports whether this call performed the shutdown.
func (g *Gate) Shutdown() bool {
	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return false
	}
	g.closed = true
	g.once.Do(func() { g.stop = make(chan struct{}) })
	close(g.stop)
	g.mu.Unlock()
	g.inFlight.Wait()
	return true
}
