package op

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

// recOp records Open/Close counts and supports injected faults.
type recOp struct {
	opens, closes atomic.Int32
	nextErr       error
	closeErr      error
	openErr       error
	gate          Gate
	rows          atomic.Int32
	closeOnce     sync.Once
}

func (r *recOp) Open(context.Context) error {
	r.opens.Add(1)
	return r.openErr
}

func (r *recOp) Next(ctx context.Context) (Row, error) {
	if !r.gate.Enter() {
		return Row{}, ErrClosed
	}
	defer r.gate.Leave()
	if r.nextErr != nil {
		return Row{}, r.nextErr
	}
	if r.rows.Load() >= 3 {
		return Row{}, io.EOF
	}
	return Row{Seq: int(r.rows.Add(1))}, nil
}

func (r *recOp) Close() error {
	var err error
	r.closeOnce.Do(func() {
		r.closes.Add(1)
		r.gate.Shutdown()
		err = r.closeErr
	})
	return err
}

func TestRunLifecycle(t *testing.T) {
	errNext := errors.New("boom")
	errClose := errors.New("close boom")
	cases := []struct {
		name     string
		nextErr  error
		closeErr error
		openErr  error
		wantErr  error
		wantRows int
	}{
		{"normal", nil, nil, nil, nil, 3},
		{"next error", errNext, nil, nil, errNext, 0},
		{"close error", nil, errClose, nil, errClose, 3},
		{"open error", nil, nil, ErrFailOpen, ErrFailOpen, 0},
		{"both errors", errNext, errClose, nil, errNext, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &recOp{nextErr: tc.nextErr, closeErr: tc.closeErr, openErr: tc.openErr}
			var got int
			err := Run(context.Background(), r, func(Row) error { got++; return nil })
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.wantRows {
				t.Fatalf("rows = %d, want %d", got, tc.wantRows)
			}
			if r.opens.Load() != r.closes.Load() {
				t.Fatalf("opens=%d closes=%d", r.opens.Load(), r.closes.Load())
			}
			if err := r.Close(); err != nil {
				t.Fatalf("second close must be idempotent-nil-or-same, got %v", err)
			}
			if r.closes.Load() != 1 {
				t.Fatalf("close called %d times, want 1", r.closes.Load())
			}
		})
	}
}

func TestGateConcurrentCloseNext(t *testing.T) {
	cases := []int{1, 2, 8}
	for _, goroutines := range cases {
		t.Run(fmt.Sprintf("%d goroutines", goroutines), func(t *testing.T) {
			r := &recOp{}
			r.gate.Stop()
			var wg sync.WaitGroup
			var panics atomic.Int32
			fire := make(chan struct{})
			for i := 0; i < goroutines; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer func() {
						if recover() != nil {
							panics.Add(1)
						}
					}()
					<-fire
					_, _ = r.Next(context.Background())
				}()
			}
			close(fire)
			_ = r.Close()
			wg.Wait()
			if panics.Load() != 0 {
				t.Fatalf("panics: %d", panics.Load())
			}
		})
	}
}
