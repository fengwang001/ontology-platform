package res

import (
	"context"
	"errors"
	"sync"
	"testing"

	"ontology/op"
)

var (
	errNext   = errors.New("next boom")
	errClose1 = errors.New("close boom 1")
	errClose2 = errors.New("close boom 2")
	errClose3 = errors.New("close boom 3")
	errOpen   = errors.New("open boom")
)

// fakeOp is a configurable operator used only in tests.
type fakeOp struct {
	tr       *Tracker
	child    op.Operator
	opened   bool
	once     sync.Once
	closeErr error
	next     func() (op.Row, bool, error)
	block    chan struct{}
	released chan struct{}
	mu       sync.Mutex
	failOpen bool
}

func (f *fakeOp) Open(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOpen {
		return errOpen
	}
	if f.child != nil {
		if err := f.child.Open(ctx); err != nil {
			return err
		}
	}
	f.opened = true
	f.tr.Opened()
	return nil
}

func (f *fakeOp) Next(ctx context.Context) (op.Row, bool, error) {
	if f.block != nil {
		<-f.block
		close(f.released)
		return op.Row{}, false, op.ErrClosed
	}
	if f.next != nil {
		return f.next()
	}
	return op.Row{}, false, nil
}

func (f *fakeOp) Close() error {
	var err error
	f.once.Do(func() {
		f.mu.Lock()
		f.opened = false
		f.mu.Unlock()
		f.tr.Closed()
		if f.block != nil {
			close(f.block)
			<-f.released
		}
		if f.child != nil {
			err = f.child.Close()
		}
		if f.closeErr != nil {
			err = errors.Join(f.closeErr, err)
		}
	})
	return err
}

func chain(tr *Tracker, n int) *fakeOp {
	var root op.Operator
	var top *fakeOp
	for i := 0; i < n; i++ {
		o := &fakeOp{tr: tr, child: root}
		root = o
		top = o
	}
	return top
}

func TestThreePaths(t *testing.T) {
	cases := []struct {
		name    string
		wantErr error
		run     func(*Tracker) error
	}{
		{"normal", nil, func(tr *Tracker) error {
			_, err := Run(context.Background(), chain(tr, 3))
			return err
		}},
		{"next-error", errNext, func(tr *Tracker) error {
			top := chain(tr, 3)
			top.next = func() (op.Row, bool, error) { return op.Row{}, false, errNext }
			_, err := Run(context.Background(), top)
			return err
		}},
		{"close-error", errClose1, func(tr *Tracker) error {
			top := chain(tr, 3)
			top.closeErr = errClose1
			_, err := Run(context.Background(), top)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewTracker()
			err := tc.run(tr)
			if tc.wantErr == nil && err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
			if tr.Opens() != tr.Closes() {
				t.Fatalf("opens=%d closes=%d", tr.Opens(), tr.Closes())
			}
			if tr.LiveSpills() != 0 {
				t.Fatalf("live spills = %d", tr.LiveSpills())
			}
		})
	}
}

func TestCloseAggregationAndIdempotence(t *testing.T) {
	tr := NewTracker()
	c := &fakeOp{tr: tr, closeErr: errClose3}
	b := &fakeOp{tr: tr, child: c, closeErr: errClose2}
	a := &fakeOp{tr: tr, child: b, closeErr: errClose1}
	if err := a.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := a.Close()
	for _, target := range []error{errClose1, errClose2, errClose3} {
		if !errors.Is(err, target) {
			t.Fatalf("missing %v in %v", target, err)
		}
	}
	if err2 := a.Close(); err2 != nil {
		t.Fatalf("repeat close must be noop, got %v", err2)
	}
	if tr.Opens() != 3 || tr.Closes() != 3 {
		t.Fatalf("opens=%d closes=%d", tr.Opens(), tr.Closes())
	}
}

func TestOpenFailureRollback(t *testing.T) {
	tr := NewTracker()
	good := &fakeOp{tr: tr}
	bad := &fakeOp{tr: tr, child: good, failOpen: true}
	if err := bad.Open(context.Background()); !errors.Is(err, errOpen) {
		t.Fatalf("want errOpen, got %v", err)
	}
	if tr.Opens() != 0 || tr.Closes() != 0 {
		t.Fatalf("no successful opens expected, got %d/%d", tr.Opens(), tr.Closes())
	}

	tr2 := NewTracker()
	var opened []op.Operator
	o1 := &fakeOp{tr: tr2}
	o2 := &fakeOp{tr: tr2}
	o3 := &fakeOp{tr: tr2, failOpen: true}
	if err := OpenChild(context.Background(), &opened, o1); err != nil {
		t.Fatal(err)
	}
	if err := OpenChild(context.Background(), &opened, o2); err != nil {
		t.Fatal(err)
	}
	if err := OpenChild(context.Background(), &opened, o3); !errors.Is(err, errOpen) {
		t.Fatalf("want errOpen rollback, got %v", err)
	}
	if !tr2.Balanced() {
		t.Fatalf("unbalanced after rollback: %d/%d", tr2.Opens(), tr2.Closes())
	}
}

func TestConcurrentTrees(t *testing.T) {
	tr := NewTracker()
	var wg sync.WaitGroup
	const trees = 50
	for i := 0; i < trees; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := Run(context.Background(), chain(tr, 4)); err != nil {
				t.Error(err)
			}
		}}()
	}
	wg.Wait()
	if !tr.Balanced() {
		t.Fatalf("opens=%d closes=%d live=%d", tr.Opens(), tr.Closes(), tr.LiveSpills())
	}
	if tr.Opens() != trees*4 {
		t.Fatalf("opens=%d want %d", tr.Opens(), trees*4)
	}
}

func TestConcurrentCloseNext(t *testing.T) {
	tr := NewTracker()
started := make(chan struct{})
	nextDone := make(chan struct{})
	f := &fakeOp{tr: tr, block: make(chan struct{}), released: make(chan struct{})}
	if err := f.Open(context.Background()); err != nil {
		t.Fatal(err)
	}
	var (
		nextErr error
		panicked bool
	)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
			}
			close(nextDone)
		}()
		close(started)
		_, _, nextErr = f.Next(context.Background())
	}()
	<-started
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	<-nextDone
	if panicked {
		t.Fatal("Next/Close concurrent panic")
	}
	if !errors.Is(nextErr, op.ErrClosed) {
		t.Fatalf("want ErrClosed, got %v", nextErr)
	}
	if !tr.Balanced() {
		t.Fatal("unbalanced")
	}
}
