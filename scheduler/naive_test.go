package scheduler

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

type naiveTask struct {
	id, nice, sp, sleep, prio, tsLeft, sleepStart int
	state                                         State
}

type naiveScheduler struct {
	limit, now, total, expiredTs int
	tasks                        map[int]*naiveTask
	active, expired              [40][]int
	cur                          *naiveTask
}

func newNaive(limit int) *naiveScheduler {
	return &naiveScheduler{limit: limit, tasks: make(map[int]*naiveTask)}
}

func naiveIndex(prio int) int { return prio - minPrio }

func (n *naiveScheduler) dispatch() {
	index := n.firstActive()
	if index < 0 {
		if !n.hasQueued(&n.expired) {
			n.cur = nil
			return
		}
		n.active, n.expired = n.expired, n.active
		n.expiredTs = 0
		index = n.firstActive()
	}
	id := n.active[index][0]
	n.active[index] = n.active[index][1:]
	n.cur = n.tasks[id]
	n.cur.state = StateRunning
}

func (n *naiveScheduler) firstActive() int {
	for index := range n.active {
		if len(n.active[index]) > 0 {
			return index
		}
	}
	return -1
}

func (n *naiveScheduler) hasQueued(group *[40][]int) bool {
	for _, ids := range group {
		if len(ids) > 0 {
			return true
		}
	}
	return false
}

func (n *naiveScheduler) count(group *[40][]int) int {
	total := 0
	for _, ids := range group {
		total += len(ids)
	}
	return total
}

func (n *naiveScheduler) arrive(t *naiveTask) {
	t.state = StateQueued
	if n.cur == nil {
		index := naiveIndex(t.prio)
		n.active[index] = append(n.active[index], t.id)
		n.dispatch()
		return
	}
	if t.prio < n.cur.prio {
		old := n.cur
		old.state = StateQueued
		oldIndex := naiveIndex(old.prio)
		n.active[oldIndex] = append([]int{old.id}, n.active[oldIndex]...)
		index := naiveIndex(t.prio)
		n.active[index] = append(n.active[index], t.id)
		n.cur = nil
		n.dispatch()
		return
	}
	index := naiveIndex(t.prio)
	n.active[index] = append(n.active[index], t.id)
}

func (n *naiveScheduler) spawn(id, nice int) error {
	if id < 0 || nice < minNice || nice > maxNice {
		return ErrInvalidArgument
	}
	if _, exists := n.tasks[id]; exists {
		return ErrTaskExists
	}
	if n.total >= n.limit {
		return ErrSchedulerFull
	}
	sp := 120 + nice
	t := &naiveTask{
		id: id, nice: nice, sp: sp,
		prio:   DynamicPrio(sp, 0),
		tsLeft: TimeSlice(sp),
		state:  StateQueued,
	}
	n.tasks[id] = t
	n.total++
	n.arrive(t)
	return nil
}

func (n *naiveScheduler) expireCurrent() {
	t := n.cur
	t.prio = DynamicPrio(t.sp, t.sleep)
	t.tsLeft = TimeSlice(t.sp)
	expiredCount := n.count(&n.expired)
	nr := 1 + n.count(&n.active) + expiredCount
	starving := expiredCount > 0 && n.now-n.expiredTs >= 100*nr
	group := &n.expired
	if Bonus(t.sleep) >= 7 && !starving {
		group = &n.active
	}
	if group == &n.expired && expiredCount == 0 {
		n.expiredTs = n.now
	}
	t.state = StateQueued
	index := naiveIndex(t.prio)
	group[index] = append(group[index], t.id)
	n.cur = nil
	n.dispatch()
}

func (n *naiveScheduler) tick() {
	n.now++
	if n.cur == nil {
		return
	}
	n.cur.tsLeft--
	if n.cur.sleep > 0 {
		n.cur.sleep--
	}
	if n.cur.tsLeft == 0 {
		n.expireCurrent()
	}
}

func (n *naiveScheduler) sleep() error {
	if n.cur == nil {
		return ErrNoCurrentTask
	}
	n.cur.sleepStart = n.now
	n.cur.state = StateSleeping
	n.cur = nil
	n.dispatch()
	return nil
}

func (n *naiveScheduler) wake(id int) error {
	t, exists := n.tasks[id]
	if !exists {
		return ErrTaskNotFound
	}
	if t.state != StateSleeping {
		return ErrTaskNotSleeping
	}
	t.sleep = min(maxSleep, t.sleep+(n.now-t.sleepStart))
	t.prio = DynamicPrio(t.sp, t.sleep)
	n.arrive(t)
	return nil
}

func (n *naiveScheduler) fork(id, child int) error {
	if id < 0 || child < 0 {
		return ErrInvalidArgument
	}
	if n.cur == nil {
		return ErrNoCurrentTask
	}
	if id != n.cur.id {
		return ErrCurrentTaskMismatch
	}
	if _, exists := n.tasks[child]; exists {
		return ErrTaskExists
	}
	if n.total >= n.limit {
		return ErrSchedulerFull
	}

	parent := n.cur
	oldTime := parent.tsLeft
	childSleep := parent.sleep / 2
	kid := &naiveTask{
		id: child, nice: parent.nice, sp: parent.sp,
		sleep:  childSleep,
		prio:   DynamicPrio(parent.sp, childSleep),
		tsLeft: (oldTime + 1) / 2,
		state:  StateQueued,
	}
	parent.tsLeft = oldTime / 2
	if parent.tsLeft == 0 {
		n.expireCurrent()
	}
	n.tasks[child] = kid
	n.total++
	n.arrive(kid)
	return nil
}

func queuesNaive(group *[40][]int) map[int][]int {
	result := make(map[int][]int)
	for index, ids := range group {
		if len(ids) > 0 {
			result[minPrio+index] = append([]int(nil), ids...)
		}
	}
	return result
}

func equalQueueMaps(got, want map[int][]int) bool {
	if len(got) != len(want) {
		return false
	}
	for prio, wantIDs := range want {
		gotIDs := got[prio]
		if len(gotIDs) != len(wantIDs) {
			return false
		}
		for index := range wantIDs {
			if gotIDs[index] != wantIDs[index] {
				return false
			}
		}
	}
	return true
}

type randomLog struct {
	text string
}

func (l *randomLog) appendf(format string, args ...any) {
	l.text += fmt.Sprintf(format, args...)
}

func compareWithNaive(t *testing.T, real *Scheduler, naive *naiveScheduler, log *randomLog, step int) {
	t.Helper()
	if real.Now() != naive.now {
		t.Fatalf("now mismatch at step %d: real=%d naive=%d\n%s", step, real.Now(), naive.now, log.text)
	}
	realCurrent, realOK := real.Current()
	naiveCurrent, naiveOK := 0, false
	if naive.cur != nil {
		naiveCurrent, naiveOK = naive.cur.id, true
	}
	if realCurrent != naiveCurrent || realOK != naiveOK {
		t.Fatalf("current mismatch at step %d: real=(%d,%v) naive=(%d,%v)\n%s", step, realCurrent, realOK, naiveCurrent, naiveOK, log.text)
	}
	if real.ExpiredTs() != naive.expiredTs {
		t.Fatalf("expiredTs mismatch at step %d: real=%d naive=%d\n%s", step, real.ExpiredTs(), naive.expiredTs, log.text)
	}
	realQueues := real.Queues()
	if !equalQueueMaps(realQueues.Active, queuesNaive(&naive.active)) ||
		!equalQueueMaps(realQueues.Expired, queuesNaive(&naive.expired)) {
		t.Fatalf("queues mismatch at step %d\nreal active=%+v expired=%+v\nnaive active=%+v expired=%+v\n%s",
			step, realQueues.Active, realQueues.Expired, queuesNaive(&naive.active), queuesNaive(&naive.expired), log.text)
	}
	for id, want := range naive.tasks {
		got, err := real.State(id)
		if err != nil {
			t.Fatalf("real State(%d): %v\n%s", id, err, log.text)
		}
		if got.ID != want.id || got.Nice != want.nice || got.SP != want.sp ||
			got.State != want.state || got.Prio != want.prio ||
			got.TsLeft != want.tsLeft || got.Sleep != want.sleep {
			t.Fatalf("task %d mismatch at step %d\nreal=%+v\nnaive id=%d nice=%d sp=%d state=%s prio=%d tsLeft=%d sleep=%d\n%s",
				id, step, got, want.id, want.nice, want.sp, want.state, want.prio, want.tsLeft, want.sleep, log.text)
		}
	}
}

func errorsEqual(a, b error) bool {
	return a == b || (a != nil && b != nil && a.Error() == b.Error())
}

func TestRandomizedAgainstNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewPCG(0x514544554c455231, 0x4e4149564553494d))
	for sequence := 0; sequence < 2000; sequence++ {
		limit := 1 + rng.IntN(8)
		real, err := New(limit)
		if err != nil {
			t.Fatal(err)
		}
		naive := newNaive(limit)
		log := &randomLog{}
		log.appendf("sequence=%d limit=%d\n", sequence, limit)
		compareWithNaive(t, real, naive, log, 0)

		for step := 1; step <= 40; step++ {
			known := make([]int, 0, len(naive.tasks))
			for id := range naive.tasks {
				known = append(known, id)
			}
			sleeping := make([]int, 0)
			for _, id := range known {
				if naive.tasks[id].state == StateSleeping {
					sleeping = append(sleeping, id)
				}
			}

			choice := rng.IntN(100)
			var realErr, naiveErr error
			switch {
			case choice < 34:
				id := sequence*1000 + step*17 + rng.IntN(18)
				if rng.IntN(8) == 0 {
					id = -rng.IntN(3)
				}
				nice := rng.IntN(41) - 20
				if rng.IntN(10) == 0 {
					nice = rng.IntN(50) - 30
				}
				log.appendf("step %d input=Spawn(id=%d,nice=%d)\n", step, id, nice)
				realErr = real.Spawn(id, nice)
				naiveErr = naive.spawn(id, nice)
			case choice < 68:
				log.appendf("step %d input=Tick()\n", step)
				realNow := real.Tick()
				naive.tick()
				log.appendf("step %d output Tick(real)=%d, now(naive)=%d; criterion=both advance and dispatch\n", step, realNow, naive.now)
			case choice < 78:
				log.appendf("step %d input=Sleep()\n", step)
				realErr = real.Sleep()
				naiveErr = naive.sleep()
			case choice < 90:
				id := sequence*1000 + step*17 + rng.IntN(18)
				if len(known) > 0 && rng.IntN(3) != 0 {
					id = known[rng.IntN(len(known))]
				}
				log.appendf("step %d input=Wake(id=%d)\n", step, id)
				realErr = real.Wake(id)
				naiveErr = naive.wake(id)
			default:
				child := sequence*1000 + step*31 + rng.IntN(50)
				id := child
				if rng.IntN(8) == 0 {
					id = -rng.IntN(2)
				}
				if rng.IntN(8) == 0 {
					child = -rng.IntN(2)
				}
				if naive.cur != nil && rng.IntN(2) == 0 {
					id = naive.cur.id
				}
				log.appendf("step %d input=Fork(id=%d,child=%d)\n", step, id, child)
				realErr = real.Fork(id, child)
				naiveErr = naive.fork(id, child)
			}

			if !errorsEqual(realErr, naiveErr) {
				t.Fatalf("error mismatch at sequence %d step %d: real=%v naive=%v\n%s", sequence, step, realErr, naiveErr, log.text)
			}
			log.appendf("step %d output error=(%v); criterion=first-specified error and no state change on rejection\n", step, realErr)
			compareWithNaive(t, real, naive, log, step)
			_ = sleeping
		}
	}
}
