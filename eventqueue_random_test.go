package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type simWatch struct {
	ep       int
	fd       int
	interest int
	flags    int
	eff      int
	queued   bool
	disabled bool
}

type naiveQueue struct {
	w       int
	e       int
	u       int
	states  map[int]int
	watches map[[2]int]*simWatch
	files   map[int][]*simWatch
	queues  [][]*simWatch
}

type operation struct {
	name     string
	ep       int
	fd       int
	interest int
	flags    int
	state    int
	max      int
}

func TestRandomAgainstNaiveSimulation(t *testing.T) {
	var log strings.Builder

	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		w := []int{1, 1, 2, 3, 5, 100}[rng.Intn(6)]
		e := 1 + rng.Intn(4)
		u := []int{1, 2, 3, 5, 100}[rng.Intn(5)]

		q, err := NewEventQueue(w, e, u)
		if err != nil {
			t.Fatalf("seed %d constructor error: %v", seed, err)
		}
		ref := newNaiveQueue(w, e, u)
		writeLog(&log, seed, "construct", w, e, u)

		for step := 0; step < 70; step++ {
			op := randomOperation(rng, e)
			writeLog(&log, seed, "step", step, op)
			compareOperation(t, q, ref, op, &log, seed)
			compareFullState(t, q, ref, &log, seed)
			writeLog(&log, seed, "step", step, "judgment return-value/state/queues/watches matched")
		}
	}

	if testing.Verbose() {
		t.Log(log.String())
	}
}

func newNaiveQueue(w, e, u int) *naiveQueue {
	return &naiveQueue{
		w:       w,
		e:       e,
		u:       u,
		states:  map[int]int{},
		watches: map[[2]int]*simWatch{},
		files:   map[int][]*simWatch{},
		queues:  make([][]*simWatch, e),
	}
}

func randomOperation(rng *rand.Rand, e int) operation {
	op := operation{
		ep:       rng.Intn(e),
		fd:       rng.Intn(7),
		interest: rng.Intn(16),
		flags:    rng.Intn(8),
		state:    rng.Intn(16),
		max:      1 + rng.Intn(4),
	}
	if rng.Intn(12) == 0 {
		op.ep = e
	}
	if rng.Intn(10) == 0 {
		op.fd = -1
	}
	if rng.Intn(10) == 0 {
		op.interest = 16
	}
	if rng.Intn(10) == 0 {
		op.flags = 8
	}
	if rng.Intn(10) == 0 {
		op.state = 16
	}

	switch rng.Intn(7) {
	case 0:
		op.name = "add"
	case 1:
		op.name = "mod"
	case 2:
		op.name = "del"
	case 3:
		op.name = "close"
	case 4:
		op.name = "set"
	case 5:
		op.name = "wait"
	default:
		op.name = "state"
	}
	return op
}

func compareOperation(t *testing.T, q *EventQueue, ref *naiveQueue, op operation, log *strings.Builder, seed int64) {
	t.Helper()

	var gotValue, wantValue any
	var gotErr, wantErr error

	switch op.name {
	case "add":
		gotErr = q.Add(op.ep, op.fd, op.interest, op.flags)
		wantErr = ref.add(op.ep, op.fd, op.interest, op.flags)
	case "mod":
		gotErr = q.Mod(op.ep, op.fd, op.interest, op.flags)
		wantErr = ref.mod(op.ep, op.fd, op.interest, op.flags)
	case "del":
		gotErr = q.Del(op.ep, op.fd)
		wantErr = ref.del(op.ep, op.fd)
	case "close":
		gotValue, gotErr = q.Close(op.fd)
		wantValue, wantErr = ref.close(op.fd)
	case "set":
		gotErr = q.SetState(op.fd, op.state)
		wantErr = ref.set(op.fd, op.state)
	case "wait":
		gotValue, gotErr = q.Wait(op.ep, op.max)
		wantValue, wantErr = ref.wait(op.ep, op.max)
	case "state":
		gotValue, gotErr = q.State(op.fd)
		wantValue, wantErr = ref.state(op.fd)
	}

	writeLog(log, seed, op.name+" result", gotValue, gotErr, wantValue, wantErr)
	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("seed %d %s error = %v, want %v\n%s", seed, op.name, gotErr, wantErr, log.String())
	}
	if !reflect.DeepEqual(normalizeEvents(gotValue), normalizeEvents(wantValue)) {
		t.Fatalf("seed %d %s value = %#v, want %#v\n%s", seed, op.name, gotValue, wantValue, log.String())
	}
}

func normalizeEvents(value any) any {
	events, ok := value.([]Event)
	if !ok || len(events) > 0 {
		return value
	}
	return []Event(nil)
}

func writeLog(builder *strings.Builder, seed int64, label string, values ...any) {
	fmt.Fprintf(builder, "seed=%d %s", seed, label)
	for _, value := range values {
		fmt.Fprintf(builder, " %#v", value)
	}
	builder.WriteByte('\n')
}

func (n *naiveQueue) state(fd int) (int, error) {
	if fd < 0 {
		return 0, ErrInvalid
	}
	return n.states[fd], nil
}

func (n *naiveQueue) add(ep, fd, interest, flags int) error {
	if !n.validInstance(ep) || fd < 0 || interest < 0 || interest > 15 ||
		flags < 0 || flags&^(ET|OneShot|Excl) != 0 {
		return ErrInvalid
	}
	if flags&Excl != 0 && (flags&OneShot != 0 || interest&(Err|Hup) != 0) {
		return ErrInvalid
	}

	key := [2]int{ep, fd}
	if _, ok := n.watches[key]; ok {
		return ErrExists
	}
	if n.instanceCount(ep) >= n.w {
		return ErrNoSpace
	}
	if len(n.watches) >= n.u {
		return ErrTooMany
	}

	watch := &simWatch{
		ep:       ep,
		fd:       fd,
		interest: interest,
		flags:    flags,
		eff:      interest | Err | Hup,
	}
	n.watches[key] = watch
	n.files[fd] = append(n.files[fd], watch)
	if n.states[fd]&watch.eff != 0 {
		n.appendQueue(watch)
	}
	return nil
}

func (n *naiveQueue) mod(ep, fd, interest, flags int) error {
	if !n.validInstance(ep) || fd < 0 || interest < 0 || interest > 15 ||
		flags < 0 || flags&^(ET|OneShot|Excl) != 0 {
		return ErrInvalid
	}
	if flags&Excl != 0 && (flags&OneShot != 0 || interest&(Err|Hup) != 0) {
		return ErrInvalid
	}

	watch := n.watches[[2]int{ep, fd}]
	if watch == nil {
		return ErrNoEnt
	}
	if watch.flags&Excl != flags&Excl {
		return ErrExclChange
	}

	watch.interest = interest
	watch.flags = flags
	watch.eff = interest | Err | Hup
	watch.disabled = false
	if !watch.queued && n.states[fd]&watch.eff != 0 {
		n.appendQueue(watch)
	}
	return nil
}

func (n *naiveQueue) del(ep, fd int) error {
	if !n.validInstance(ep) || fd < 0 {
		return ErrInvalid
	}
	watch := n.watches[[2]int{ep, fd}]
	if watch == nil {
		return ErrNoEnt
	}
	n.removeWatch(watch)
	return nil
}

func (n *naiveQueue) close(fd int) (int, error) {
	if fd < 0 {
		return 0, ErrInvalid
	}

	removed := len(n.files[fd])
	for _, watch := range n.files[fd] {
		n.removeWatch(watch)
	}
	n.states[fd] = 0
	return removed, nil
}

func (n *naiveQueue) set(fd, state int) error {
	if fd < 0 || state < 0 || state > 15 {
		return ErrInvalid
	}

	old := n.states[fd]
	n.states[fd] = state

	candidates := append([]*simWatch(nil), n.files[fd]...)
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ep != candidates[j].ep {
			return candidates[i].ep < candidates[j].ep
		}
		return candidates[i].fd < candidates[j].fd
	})

	var exclusive *simWatch
	for _, watch := range candidates {
		if watch.disabled || watch.queued {
			continue
		}

		triggered := false
		if watch.flags&ET == 0 {
			triggered = state&watch.eff != 0
		} else {
			triggered = state&^old&watch.eff != 0
		}
		if !triggered {
			continue
		}

		if watch.flags&Excl == 0 {
			n.appendQueue(watch)
		} else if exclusive == nil || watch.ep < exclusive.ep {
			exclusive = watch
		}
	}
	if exclusive != nil {
		n.appendQueue(exclusive)
	}
	return nil
}

func (n *naiveQueue) wait(ep, max int) ([]Event, error) {
	if !n.validInstance(ep) || max < 1 || max > 1_000_000 {
		return nil, ErrInvalid
	}

	events := make([]Event, 0)
	ready := []*simWatch{}
	for len(n.queues[ep]) > 0 && len(events) < max {
		watch := n.queues[ep][0]
		n.queues[ep] = n.queues[ep][1:]
		watch.queued = false

		revents := n.states[watch.fd] & watch.eff
		if revents == 0 {
			continue
		}

		events = append(events, Event{Fd: watch.fd, Revents: revents})
		if watch.flags&OneShot != 0 {
			watch.disabled = true
		} else if watch.flags&ET == 0 {
			ready = append(ready, watch)
		}
	}

	for _, watch := range ready {
		n.appendQueue(watch)
	}
	return events, nil
}

func compareFullState(t *testing.T, q *EventQueue, ref *naiveQueue, log *strings.Builder, seed int64) {
	t.Helper()

	fds := make(map[int]struct{})
	for fd := range ref.states {
		fds[fd] = struct{}{}
	}
	for fd := range ref.files {
		fds[fd] = struct{}{}
	}
	for fd := range fds {
		got, gotErr := q.State(fd)
		want, wantErr := ref.state(fd)
		if got != want || !errors.Is(gotErr, wantErr) {
			t.Fatalf("seed %d state[%d] = (%d,%v), want (%d,%v)\n%s", seed, fd, got, gotErr, want, wantErr, log.String())
		}
	}

	for ep := 0; ep < ref.e; ep++ {
		gotQueue, err := q.Queue(ep)
		if err != nil {
			t.Fatal(err)
		}
		if len(gotQueue) == 0 {
			gotQueue = nil
		}
		if wantQueue := ref.queue(ep); !reflect.DeepEqual(gotQueue, wantQueue) {
			t.Fatalf("seed %d queue[%d] = %v, want %v\n%s", seed, ep, gotQueue, wantQueue, log.String())
		}

		gotWatches, err := q.Watches(ep)
		if err != nil {
			t.Fatal(err)
		}
		if wantWatches := ref.watchesFor(ep); !reflect.DeepEqual(gotWatches, wantWatches) {
			t.Fatalf("seed %d watches[%d] = %#v, want %#v\n%s", seed, ep, gotWatches, wantWatches, log.String())
		}
	}
}

func (n *naiveQueue) validInstance(ep int) bool {
	return ep >= 0 && ep < n.e
}

func (n *naiveQueue) instanceCount(ep int) int {
	count := 0
	for _, watch := range n.watches {
		if watch.ep == ep {
			count++
		}
	}
	return count
}

func (n *naiveQueue) appendQueue(watch *simWatch) {
	if watch.queued {
		return
	}
	n.queues[watch.ep] = append(n.queues[watch.ep], watch)
	watch.queued = true
}

func (n *naiveQueue) removeWatch(watch *simWatch) {
	delete(n.watches, [2]int{watch.ep, watch.fd})

	fileWatches := n.files[watch.fd]
	for i, candidate := range fileWatches {
		if candidate == watch {
			n.files[watch.fd] = append(append([]*simWatch{}, fileWatches[:i]...), fileWatches[i+1:]...)
			break
		}
	}

	queue := n.queues[watch.ep]
	for i, candidate := range queue {
		if candidate == watch {
			n.queues[watch.ep] = append(append([]*simWatch{}, queue[:i]...), queue[i+1:]...)
			break
		}
	}
	watch.queued = false
}

func (n *naiveQueue) queue(ep int) []int {
	result := make([]int, 0, len(n.queues[ep]))
	for _, watch := range n.queues[ep] {
		result = append(result, watch.fd)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (n *naiveQueue) watchesFor(ep int) []Watch {
	result := []Watch{}
	for _, watch := range n.watches {
		if watch.ep != ep {
			continue
		}
		result = append(result, Watch{
			Fd:       watch.fd,
			Interest: watch.interest,
			Flags:    watch.flags,
			Eff:      watch.eff,
			Queued:   watch.queued,
			Disabled: watch.disabled,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Fd < result[j].Fd
	})
	return result
}
