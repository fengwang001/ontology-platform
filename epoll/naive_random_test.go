package epoll

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

func actualQueues(t *testing.T, p *Poll, e int) [][]int {
	t.Helper()
	result := make([][]int, e)
	for ep := 0; ep < e; ep++ {
		queue, err := p.Queue(ep)
		if err != nil {
			t.Fatal(err)
		}
		if len(queue) == 0 {
			queue = nil
		}
		result[ep] = queue
	}
	return result
}

func actualWatches(t *testing.T, p *Poll, e int) [][]Watch {
	t.Helper()
	result := make([][]Watch, e)
	for ep := 0; ep < e; ep++ {
		watches, err := p.Watches(ep)
		if err != nil {
			t.Fatal(err)
		}
		if len(watches) == 0 {
			watches = nil
		}
		result[ep] = watches
	}
	return result
}

func sameError(got, want error) bool {
	return errors.Is(got, want)
}

func assertNaiveState(t *testing.T, p *Poll, m *naiveModel, seed int64, step int) {
	t.Helper()
	gotQueues := actualQueues(t, p, m.e)
	wantQueues := m.snapshotQueues()
	if !reflect.DeepEqual(gotQueues, wantQueues) {
		t.Fatalf("seed=%d step=%d decision=queue mismatch got=%v want=%v", seed, step, gotQueues, wantQueues)
	}

	gotWatches := actualWatches(t, p, m.e)
	wantWatches := m.snapshotWatches()
	if !reflect.DeepEqual(gotWatches, wantWatches) {
		t.Fatalf("seed=%d step=%d decision=watch mismatch got=%#v want=%#v", seed, step, gotWatches, wantWatches)
	}
}

func TestRandomNaiveComparison(t *testing.T) {
	const groups = 2000
	const actions = 40

	for seed := int64(1); seed <= groups; seed++ {
		rng := rand.New(rand.NewSource(seed))
		e := 1 + rng.Intn(4)
		w := 1 + rng.Intn(5)
		u := 1 + rng.Intn(12)
		p, err := New(w, e, u)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaiveModel(w, e, u)

		for step := 0; step < actions; step++ {
			ep := rng.Intn(e + 1)
			fd := rng.Intn(8) - 1
			op := rng.Intn(9)
			input := ""

			switch op {
			case 0:
				interest := rng.Intn(17)
				flags := rng.Intn(10)
				input = fmt.Sprintf("Add ep=%d fd=%d interest=%d flags=%d", ep, fd, interest, flags)
				got := p.Add(ep, fd, interest, flags)
				want := m.add(ep, fd, interest, flags)
				if !sameError(got, want) {
					t.Fatalf("seed=%d step=%d input=%s output=%v want=%v decision=error mismatch", seed, step, input, got, want)
				}
			case 1:
				interest := rng.Intn(17)
				flags := rng.Intn(10)
				input = fmt.Sprintf("Mod ep=%d fd=%d interest=%d flags=%d", ep, fd, interest, flags)
				got := p.Mod(ep, fd, interest, flags)
				want := m.mod(ep, fd, interest, flags)
				if !sameError(got, want) {
					t.Fatalf("seed=%d step=%d input=%s output=%v want=%v decision=error mismatch", seed, step, input, got, want)
				}
			case 2:
				input = fmt.Sprintf("Del ep=%d fd=%d", ep, fd)
				got := p.Del(ep, fd)
				want := m.del(ep, fd)
				if !sameError(got, want) {
					t.Fatalf("seed=%d step=%d input=%s output=%v want=%v decision=error mismatch", seed, step, input, got, want)
				}
			case 3:
				state := rng.Intn(17)
				input = fmt.Sprintf("SetState fd=%d state=%d", fd, state)
				got := p.SetState(fd, state)
				want := m.setState(fd, state)
				if !sameError(got, want) {
					t.Fatalf("seed=%d step=%d input=%s output=%v want=%v decision=error mismatch", seed, step, input, got, want)
				}
			case 4:
				input = fmt.Sprintf("Close fd=%d", fd)
				gotRemoved, got := p.Close(fd)
				wantRemoved, want := m.close(fd)
				if !sameError(got, want) || gotRemoved != wantRemoved {
					t.Fatalf("seed=%d step=%d input=%s output=(%d,%v) want=(%d,%v) decision=close mismatch", seed, step, input, gotRemoved, got, wantRemoved, want)
				}
			case 5:
				max := 1 + rng.Intn(7)
				input = fmt.Sprintf("Wait ep=%d max=%d", ep, max)
				gotEvents, got := p.Wait(ep, max)
				wantEvents, want := m.wait(ep, max)
				if !sameError(got, want) || !reflect.DeepEqual(gotEvents, wantEvents) {
					t.Fatalf("seed=%d step=%d input=%s output=(%#v,%v) want=(%#v,%v) decision=wait mismatch", seed, step, input, gotEvents, got, wantEvents, want)
				}
			case 6:
				input = fmt.Sprintf("State fd=%d", fd)
				gotState, got := p.State(fd)
				wantState := m.states[fd]
				var want error
				if fd < 0 {
					want = ErrInvalid
				}
				if !sameError(got, want) || gotState != wantState {
					t.Fatalf("seed=%d step=%d input=%s output=(%d,%v) want=(%d,%v) decision=state mismatch", seed, step, input, gotState, got, wantState, want)
				}
			case 7:
				input = fmt.Sprintf("Queue ep=%d", ep)
				if ep >= e {
					if _, err := p.Queue(ep); !errors.Is(err, ErrInvalid) {
						t.Fatalf("seed=%d step=%d input=%s output=%v decision=invalid queue", seed, step, input, err)
					}
				}
			default:
				input = fmt.Sprintf("Watches ep=%d", ep)
				if ep >= e {
					if _, err := p.Watches(ep); !errors.Is(err, ErrInvalid) {
						t.Fatalf("seed=%d step=%d input=%s output=%v decision=invalid watches", seed, step, input, err)
					}
				}
			}

			t.Logf("seed=%d step=%d input=%s output=matched decision=snapshot queues=%v watches=%#v", seed, step, input, m.snapshotQueues(), m.snapshotWatches())
			assertNaiveState(t, p, m, seed, step)
		}
	}
}
