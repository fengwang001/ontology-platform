package parking

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

func testConfig(public, monthly int) Config {
	return Config{
		PublicSpots: public, MonthlySpots: monthly,
		FreeMinutes: 10, BillingUnit: 15, DailyCap: 5,
		UnitFee: 1, OvertimeFee: 3, GraceDays: 1, EvictionTime: 30,
	}
}

func mustRegister(t *testing.T, m *Manager, now int, plate string, days int) Lease {
	t.Helper()
	lease, err := m.RegisterMonthly(now, plate, 0, days*minutesPerDay)
	if err != nil {
		t.Fatalf("RegisterMonthly(%s): %v", plate, err)
	}
	return lease
}

func mustEnter(t *testing.T, m *Manager, now int, plate string) EntryResult {
	t.Helper()
	result, err := m.Enter(now, plate)
	if err != nil {
		t.Fatalf("Enter(%s): %v", plate, err)
	}
	return result
}

func mustExit(t *testing.T, m *Manager, now int, plate string) ExitResult {
	t.Helper()
	result, err := m.Exit(now, plate)
	if err != nil {
		t.Fatalf("Exit(%s): %v", plate, err)
	}
	return result
}

func TestFreeDurationAndBillingRounding(t *testing.T) {
	m, _ := NewManager(testConfig(1, 0))
	mustEnter(t, m, 0, "V")
	if got := mustExit(t, m, 10, "V").Fee; got != 0 {
		t.Fatalf("exact F minutes fee = %d, want 0", got)
	}
	mustEnter(t, m, 20, "V")
	if got := mustExit(t, m, 36, "V").Fee; got != 1 {
		t.Fatalf("one minute into unit fee = %d, want 1", got)
	}
	mustEnter(t, m, 40, "V")
	if got := mustExit(t, m, 130, "V").Fee; got != 5 {
		t.Fatalf("daily capped fee = %d, want 5", got)
	}
}

func TestCrossDayCap(t *testing.T) {
	m, _ := NewManager(testConfig(1, 0))
	mustEnter(t, m, minutesPerDay-20, "V")
	got := mustExit(t, m, 2*minutesPerDay+20, "V").Fee
	if want := int64(8); got != want {
		t.Fatalf("global free time with cross-day caps = %d, want %d", got, want)
	}
}

func TestSharedMidnightEndpointsAndPriority(t *testing.T) {
	m, _ := NewManager(testConfig(1, 2))
	lease := mustRegister(t, m, 0, "A", 2)
	if err := m.SetShare(0, "A", Interval{StartMinute: 23 * 60, EndMinute: 60}); err != nil {
		t.Fatal(err)
	}
	result := mustEnter(t, m, 23*60, "V")
	if result.SpotID != 1 {
		t.Fatalf("public priority = spot %d, want 1", result.SpotID)
	}
	night := mustEnter(t, m, minutesPerDay, "N")
	if night.SpotID != lease.SpotID {
		t.Fatalf("midnight endpoint share = %d, want %d", night.SpotID, lease.SpotID)
	}
	mustExit(t, m, minutesPerDay+1, "N")
	if _, err := m.Enter(minutesPerDay+60, "X"); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("end-exclusive share err = %v, want ErrNoSpace", err)
	}
}

func TestEvictionWaitAndAutomaticReturn(t *testing.T) {
	m, _ := NewManager(testConfig(0, 1))
	lease := mustRegister(t, m, 0, "A", 2)
	if err := m.SetShare(0, "A", Interval{0, 23*60 + 59}); err != nil {
		t.Fatal(err)
	}
	visitor := mustEnter(t, m, 0, "V")
	if visitor.SpotID != lease.SpotID {
		t.Fatalf("visitor spot = %d", visitor.SpotID)
	}
	owner := mustEnter(t, m, 10, "A")
	if !owner.Waiting || owner.EvictionID != "V" {
		t.Fatalf("owner result = %+v, want waiting for V", owner)
	}
	if got := mustExit(t, m, 40, "V").Fee; got != 0 {
		t.Fatalf("30 free eviction minutes + 10 prior = %d, want 0", got)
	}
	if _, err := m.Enter(41, "A"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("owner after automatic return err = %v, want duplicate entry", err)
	}
	mustExit(t, m, 42, "A")
	mustEnter(t, m, 50, "V2")
	owner2 := mustEnter(t, m, 60, "A")
	if !owner2.Waiting {
		t.Fatal("second owner should wait without public spots")
	}
	if got := mustExit(t, m, 91, "V2").Fee; got != 3 {
		t.Fatalf("single overtime minute fee = %d, want 3", got)
	}
	mustExit(t, m, 92, "A")
}

func TestShareEndOvertime(t *testing.T) {
	m, _ := NewManager(testConfig(0, 1))
	mustRegister(t, m, 0, "A", 2)
	if err := m.SetShare(0, "A", Interval{0, 60}); err != nil {
		t.Fatal(err)
	}
	mustEnter(t, m, 0, "V")
	if got := mustExit(t, m, 61, "V").Fee; got != 7 {
		t.Fatalf("one minute after share end = %d, want 7", got)
	}
	if _, err := m.Enter(62, "X"); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("closed share entry = %v", err)
	}
}

func TestLeaseExpiryAndGraceRenewal(t *testing.T) {
	m, _ := NewManager(testConfig(0, 1))
	lease := mustRegister(t, m, 0, "A", 1)
	if err := m.SetShare(0, "A", Interval{0, 60}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Enter(minutesPerDay-1, "A"); err != nil {
		t.Fatalf("last active minute enter: %v", err)
	}
	mustExit(t, m, minutesPerDay-1, "A")
	if _, err := m.Enter(minutesPerDay, "A"); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("expired owner as visitor err = %v", err)
	}
	renewed, err := m.RenewMonthly(2*minutesPerDay-1, "A", 3*minutesPerDay)
	if err != nil || renewed.SpotID != lease.SpotID {
		t.Fatalf("last grace day renewal = %+v, %v", renewed, err)
	}
	stale, _ := NewManager(testConfig(0, 1))
	stale.RegisterMonthly(0, "B", 0, minutesPerDay)
	if _, err := stale.RenewMonthly(2*minutesPerDay+1, "B", 4*minutesPerDay); !errors.Is(err, ErrLease) {
		t.Fatalf("after grace renewal = %v", err)
	}
}

func TestErrorOrderAndRejectedOperationNoTrace(t *testing.T) {
	m, _ := NewManager(testConfig(1, 0))
	mustEnter(t, m, 10, "SEED")
	if _, err := m.Enter(10, ""); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid arg = %v", err)
	}
	if _, err := m.Enter(5, "V"); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("clock rollback = %v", err)
	}
	if _, err := m.Enter(10, "SEED"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("duplicate enter = %v", err)
	}
	mustExit(t, m, 10, "SEED")
	if _, err := m.Exit(10, "Z"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown plate = %v", err)
	}
	if _, err := m.Exit(10, "SEED"); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("duplicate exit = %v", err)
	}
	if err := m.SetShare(10, "Z", Interval{0, 1}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-owner share = %v", err)
	}
}

func TestConcurrentEntriesDoNotShareSpot(t *testing.T) {
	m, _ := NewManager(testConfig(100, 0))
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan int, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			plate := "V" + string(rune('a'+i/26)) + string(rune('a'+i%26))
			result, err := m.Enter(0, plate)
			if err == nil {
				results <- result.SpotID
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	seen := map[int]bool{}
	for spot := range results {
		if seen[spot] {
			t.Fatalf("spot %d allocated twice", spot)
		}
		seen[spot] = true
	}
	if len(seen) != 100 {
		t.Fatalf("allocated %d unique spots, want 100", len(seen))
	}
}

type naiveReg struct {
	plate    string
	spot     int
	start    int
	end      int
	share    Interval
	has      bool
	shareSet bool
}

type naiveCar struct {
	plate    string
	spot     int
	entered  int
	monthly  bool
	waiting  bool
	public   bool
	owner    string
	evictAt  int
	overFrom int
	shareEnd int
}

type naiveWorld struct {
	cfg   Config
	now   int
	pub   map[int]bool
	reg   map[int]*naiveReg
	plate map[string]*naiveReg
	cars  map[string]*naiveCar
	known map[string]bool
	occ   map[int]*naiveCar
	wait  []string
	logs  []string
}

func newNaiveWorld(cfg Config) *naiveWorld {
	w := &naiveWorld{
		cfg: cfg,
		pub: map[int]bool{}, reg: map[int]*naiveReg{},
		plate: map[string]*naiveReg{}, cars: map[string]*naiveCar{},
		known: map[string]bool{}, occ: map[int]*naiveCar{},
	}
	for id := 1; id <= cfg.PublicSpots; id++ {
		w.pub[id] = true
	}
	for id := cfg.PublicSpots + 1; id <= cfg.PublicSpots+cfg.MonthlySpots; id++ {
		w.reg[id] = &naiveReg{spot: id}
	}
	return w
}

func naiveActiveShare(r *naiveReg, now int) bool {
	if r == nil || !r.has || !r.shareSet || now < r.start || now >= r.end {
		return false
	}
	return intervalActive(r.share, now)
}

func naiveNextEnd(now int, in Interval) int {
	day := now / minutesPerDay * minutesPerDay
	if in.StartMinute > in.EndMinute && now%minutesPerDay < in.EndMinute {
		return day + in.EndMinute
	}
	if in.StartMinute < in.EndMinute && now%minutesPerDay < in.EndMinute {
		return day + in.EndMinute
	}
	return day + minutesPerDay + in.EndMinute
}

func (w *naiveWorld) logf(format string, args ...any) {
	w.logs = append(w.logs, fmt.Sprintf(format, args...))
}

func (w *naiveWorld) settle(now int) {
	w.now = now
	for plate, r := range w.plate {
		if now > r.end+w.cfg.GraceDays*minutesPerDay && w.cars[plate] == nil {
			delete(w.plate, plate)
			r.plate = ""
			r.has = false
		}
	}
	for _, c := range w.cars {
		r := w.reg[c.spot]
		if !c.monthly && !c.public && c.owner == "" && r != nil && r.has &&
			c.overFrom == 0 && c.shareEnd > 0 && now >= c.shareEnd {
			c.overFrom = c.shareEnd
			w.logf("t=%d %s share ended, overtime starts", now, c.plate)
		}
		if !c.monthly && c.owner != "" && c.overFrom == 0 && now >= c.evictAt {
			c.overFrom = c.evictAt
			w.logf("t=%d %s eviction deadline passed", now, c.plate)
		}
	}
	for _, r := range w.plate {
		if r.has && now >= r.end {
			if c := w.occ[r.spot]; c != nil && !c.monthly && c.owner == "" {
				c.overFrom = r.end
			}
		}
	}
}

func (w *naiveWorld) register(now int, plate string, end int) (int, error) {
	if now < w.now || plate == "" || end <= now || end%minutesPerDay != 0 {
		return 0, ErrInvalidArgument
	}
	if old := w.plate[plate]; old != nil && now <= old.end+w.cfg.GraceDays*minutesPerDay {
		return 0, ErrInvalidState
	}
	if w.cars[plate] != nil {
		return 0, ErrInvalidState
	}
	spotID := 0
	for id := w.cfg.PublicSpots + 1; id <= w.cfg.PublicSpots+w.cfg.MonthlySpots; id++ {
		if r := w.reg[id]; r.plate == "" {
			spotID = id
			break
		}
	}
	if spotID == 0 {
		return 0, ErrNoSpace
	}
	r := w.reg[spotID]
	r.plate = plate
	r.start = 0
	r.end = end
	r.has = true
	w.plate[plate] = r
	w.settle(now)
	w.logf("t=%d register %s -> %d", now, plate, spotID)
	return spotID, nil
}

func (w *naiveWorld) setShare(now int, plate string, in Interval) error {
	if err := validateInterval(in); err != nil {
		return err
	}
	if now < w.now {
		return ErrClockRollback
	}
	r := w.plate[plate]
	if r == nil {
		return ErrNotFound
	}
	w.settle(now)
	if now < r.start || now >= r.end {
		return ErrLease
	}
	r.share = in
	r.shareSet = true
	if c := w.occ[r.spot]; c != nil && !c.monthly && c.owner == "" {
		c.shareEnd = naiveNextEnd(now, in)
		c.overFrom = 0
	}
	w.logf("t=%d share %s [%d,%d)", now, plate, in.StartMinute, in.EndMinute)
	return nil
}

func (w *naiveWorld) findSpot(now int, public bool) int {
	best := 0
	for spotID := 1; spotID <= w.cfg.PublicSpots+w.cfg.MonthlySpots; spotID++ {
		if w.occ[spotID] != nil {
			continue
		}
		if public && w.pub[spotID] {
			return spotID
		}
		if !public {
			r := w.reg[spotID]
			if r != nil && naiveActiveShare(r, now) {
				return spotID
			}
		}
	}
	return best
}

func (w *naiveWorld) enter(now int, plate string) (EntryResult, error) {
	if plate == "" {
		return EntryResult{}, ErrInvalidArgument
	}
	if now < w.now {
		return EntryResult{}, ErrClockRollback
	}
	if w.cars[plate] != nil {
		return EntryResult{}, ErrInvalidState
	}
	w.settle(now)
	if r := w.plate[plate]; r != nil && now >= r.start && now < r.end {
		w.known[plate] = true
		if w.occ[r.spot] == nil {
			c := &naiveCar{plate: plate, spot: r.spot, entered: now, monthly: true}
			w.cars[plate] = c
			w.occ[r.spot] = c
			return EntryResult{SpotID: r.spot}, nil
		}
		v := w.occ[r.spot]
		v.owner = plate
		v.evictAt = now + w.cfg.EvictionTime
		v.overFrom = 0
		owner := &naiveCar{plate: plate, entered: now, monthly: true}
		w.cars[plate] = owner
		if spotID := w.findSpot(now, true); spotID != 0 {
			owner.spot = spotID
			owner.public = true
			w.occ[spotID] = owner
			return EntryResult{SpotID: spotID, EvictionID: v.plate}, nil
		}
		owner.waiting = true
		w.wait = append(w.wait, plate)
		return EntryResult{Waiting: true, EvictionID: v.plate}, nil
	}
	if spotID := w.findSpot(now, true); spotID != 0 {
		w.known[plate] = true
		c := &naiveCar{plate: plate, spot: spotID, entered: now, public: true}
		w.cars[plate] = c
		w.occ[spotID] = c
		return EntryResult{SpotID: spotID}, nil
	}
	spotID := w.findSpot(now, false)
	if spotID == 0 {
		return EntryResult{}, ErrNoSpace
	}
	w.known[plate] = true
	c := &naiveCar{plate: plate, spot: spotID, entered: now, shareEnd: naiveNextEnd(now, w.reg[spotID].share)}
	w.cars[plate] = c
	w.occ[spotID] = c
	return EntryResult{SpotID: spotID}, nil
}

func (w *naiveWorld) placeWaiting(spotID int, now int) {
	if len(w.wait) == 0 {
		return
	}
	plate := w.wait[0]
	w.wait = w.wait[1:]
	owner := w.cars[plate]
	if owner != nil {
		owner.waiting = false
		owner.public = true
		owner.spot = spotID
		w.occ[spotID] = owner
	}
}

func (w *naiveWorld) exit(now int, plate string) (ExitResult, error) {
	if plate == "" {
		return ExitResult{}, ErrInvalidArgument
	}
	if now < w.now {
		return ExitResult{}, ErrClockRollback
	}
	c := w.cars[plate]
	if c == nil {
		if w.known[plate] {
			return ExitResult{}, ErrInvalidState
		}
		return ExitResult{}, ErrNotFound
	}
	w.settle(now)
	spotID := c.spot
	if c.monthly {
		if c.waiting {
			w.removeWait(plate)
		} else {
			delete(w.occ, c.spot)
			if c.public {
				w.placeWaiting(c.spot, now)
			}
		}
		delete(w.cars, plate)
		return ExitResult{SpotID: spotID}, nil
	}
	fee := w.fee(c, now)
	delete(w.occ, c.spot)
	delete(w.cars, plate)
	if c.public {
		w.placeWaiting(c.spot, now)
	} else if c.owner != "" {
		owner := w.cars[c.owner]
		if owner != nil && owner.monthly {
			r := w.plate[c.owner]
			if r != nil {
				if owner.waiting {
					w.removeWait(owner.plate)
				} else if owner.public {
					delete(w.occ, owner.spot)
				}
				owner.waiting = false
				owner.public = false
				owner.spot = r.spot
				w.occ[r.spot] = owner
			}
		}
	}
	w.logf("t=%d exit %s fee=%d", now, plate, fee)
	return ExitResult{SpotID: spotID, Fee: fee}, nil
}

func (w *naiveWorld) removeWait(plate string) {
	for i, item := range w.wait {
		if item == plate {
			w.wait = append(w.wait[:i], w.wait[i+1:]...)
		}
	}
}

func (w *naiveWorld) fee(c *naiveCar, now int) int64 {
	overStart := 0
	if c.overFrom > 0 {
		overStart = c.overFrom
	}
	freeEviction := 0
	if c.owner != "" {
		length := now - c.evictAt + w.cfg.EvictionTime
		if now < c.evictAt {
			length = now - (c.evictAt - w.cfg.EvictionTime)
		}
		freeEviction = min(length, w.cfg.EvictionTime)
	}
	overtime := []intervalMinutes(nil)
	if overStart > 0 {
		overtime = append(overtime, intervalMinutes{start: overStart, end: now})
	}
	return calcFee(w.cfg, c.entered, now, freeEviction, overtime)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func TestRandomSequenceMatchesNaiveModel(t *testing.T) {
	cfg := testConfig(2, 2)
	manager, _ := NewManager(cfg)
	naive := newNaiveWorld(cfg)
	rng := rand.New(rand.NewSource(1494))
	plates := []string{"A", "B", "V", "W", "X"}
	now := 0

	for step := 0; step < 240; step++ {
		plate := plates[rng.Intn(len(plates))]
		now += rng.Intn(4)
		op := rng.Intn(10)
		var detail string

		switch op {
		case 0, 1:
			end := (1 + rng.Intn(3)) * minutesPerDay
			wantSpot, wantErr := naive.register(now, plate, end)
			got, gotErr := manager.RegisterMonthly(now, plate, 0, end)
			detail = fmt.Sprintf("register %s", plate)
			if !errors.Is(gotErr, wantErr) || got.SpotID != wantSpot {
				t.Fatalf("step %d %s: got (%d,%v), want (%d,%v)\n%s", step, detail, got.SpotID, gotErr, wantSpot, wantErr, naive.logs[len(naive.logs)-1])
			}
		case 2, 3:
			start := rng.Intn(23 * 60)
			length := 1 + rng.Intn(minutesPerDay-1)
			in := Interval{StartMinute: start, EndMinute: (start + length) % minutesPerDay}
			if in.StartMinute == in.EndMinute {
				in.EndMinute = (in.EndMinute + 1) % minutesPerDay
			}
			wantErr := naive.setShare(now, plate, in)
			gotErr := manager.SetShare(now, plate, in)
			detail = fmt.Sprintf("share %s [%d,%d)", plate, in.StartMinute, in.EndMinute)
			if !errors.Is(gotErr, wantErr) {
				t.Fatalf("step %d %s: got %v want %v", step, detail, gotErr, wantErr)
			}
		case 4, 5, 6:
			want, wantErr := naive.enter(now, plate)
			got, gotErr := manager.Enter(now, plate)
			detail = fmt.Sprintf("enter %s", plate)
			if !errors.Is(gotErr, wantErr) || got.Waiting != want.Waiting || got.SpotID != want.SpotID || got.EvictionID != want.EvictionID {
				t.Fatalf("step %d %s: got %+v,%v want %+v,%v", step, detail, got, gotErr, want, wantErr)
			}
		default:
			want, wantErr := naive.exit(now, plate)
			got, gotErr := manager.Exit(now, plate)
			detail = fmt.Sprintf("exit %s", plate)
			if !errors.Is(gotErr, wantErr) || got.SpotID != want.SpotID || got.Fee != want.Fee {
				t.Fatalf("step %d %s: got %+v,%v want %+v,%v", step, detail, got, gotErr, want, wantErr)
			}
		}

		logs := manager.Logs()
		last := logs[len(logs)-1]
		t.Logf("step=%d now=%d %s accepted=%v detail=%s", step, now, last.Action, !last.Rejected, detail)
		for _, line := range naive.logs {
			t.Logf("naive %s", line)
		}
		naive.logs = nil
	}
}
