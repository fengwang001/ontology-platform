// Package stage provides the generic bounded-queue pipeline stage skeleton:
// backpressure, in-flight accounting, barrier propagation and graceful stop.
package stage

import (
	"context"
	"sync"
)

// Record is the payload flowing through every stage.
type Record struct {
	Pos   int64  // source-assigned monotonic position
	Key   string // empty key is legal; missing/unparseable key is reported as Bad
	Val   int64
	Bad   bool
	Bytes []byte // raw bytes from the source
}

// Barrier is an in-band control message. When a stage emits one it guarantees
// that every record with Pos < Barrier.Pos has already been forwarded.
type Barrier struct {
	Pos     int64
	Final   bool
	Snapshot map[string]int64
}

// Msg is one queue element: either a record or a barrier.
type Msg struct {
	Rec *Record
	Bar *Barrier
}

// Config constructs a stage. Cap is the per-stage in-flight capacity
// (channel buffer Cap-1 plus the single record held by the worker).
type Config struct {
	Name       string
	Cap        int
	Process    func(context.Context, Record) (*Record, error)
	OnBarrier  func(context.Context, Barrier) ([]Msg, error)
	Blocked    func() // called once per failed non-blocking send (backpressure)
	MaxInflight func(int)
}

// Stage is a bounded, back-pressured pipeline stage.
type Stage struct {
	cfg    Config
	in     chan Msg
	out    chan Msg
	wg     sync.WaitGroup
	stop   sync.Once
	ctx    context.Context
	cancel context.CancelFunc
	inFlight     int
	maxInFlight  int
}

// New creates a stage.
func New(cfg Config) *Stage {
	if cfg.Cap < 1 {
		cfg.Cap = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Stage{
		cfg:    cfg,
		in:     make(chan Msg, cfg.Cap-1),
		out:    make(chan Msg, cfg.Cap-1),
		ctx:    ctx,
		cancel: cancel,
	}
}

// In returns the input channel (the upstream stage sends into it).
func (s *Stage) In() chan<- Msg { return s.in }

// Out returns the output channel (the downstream stage reads it).
func (s *Stage) Out() <-chan Msg { return s.out }

// MaxInFlight reports the historical maximum number of records held by this
// stage (buffer + worker).
func (s *Stage) MaxInFlight() int { return s.maxInFlight }

func (s *Stage) noteIn(n int) {
	s.inFlight += n
	if s.inFlight > s.maxInFlight {
		s.maxInFlight = s.inFlight
	}
	if s.cfg.MaxInflight != nil {
		s.cfg.MaxInflight(s.inFlight)
	}
}

// send delivers to the bounded output channel, recording backpressure.
func (s *Stage) send(m Msg) error {
	select {
	case s.out <- m:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	default:
	}
	if s.cfg.Blocked != nil {
		s.cfg.Blocked()
	}
	select {
	case s.out <- m:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

// Start launches the worker goroutine.
func (s *Stage) Start() {
	s.wg.Add(1)
	go s.run()
}

func (s *Stage) run() {
	defer s.wg.Done()
	defer close(s.out)
	for {
		select {
		case m, ok := <-s.in:
			if !ok {
				return
			}
			if err := s.handle(m); err != nil {
				s.cancel()
				return
			}
		case <-s.ctx.Done():
			return
		}
	}
}

func (s *Stage) handle(m Msg) error {
	if m.Bar != nil {
		if s.cfg.OnBarrier != nil {
			out, err := s.cfg.OnBarrier(s.ctx, *m.Bar)
			if err != nil {
				return err
			}
			for _, o := range out {
				if err := s.send(o); err != nil {
					return err
				}
			}
			return nil
		}
		return s.send(m)
	}
	s.noteIn(1)
	defer s.noteIn(-1)
	rec := m.Rec
	if s.cfg.Process != nil {
		r, err := s.cfg.Process(s.ctx, *rec)
		if err != nil {
			return err
		}
		rec = r
	}
	if rec == nil {
		return nil
	}
	return s.send(Msg{Rec: rec})
}

// CloseInput marks the upstream end complete and waits for the worker to drain.
func (s *Stage) CloseInput() { close(s.in) }

// Stop cancels the stage and waits for its goroutine to exit. Idempotent and
// safe before Start. It does not close In; callers drain via CloseInput.
func (s *Stage) Stop() {
	s.stop.Do(func() { s.cancel() })
	s.wg.Wait()
}
