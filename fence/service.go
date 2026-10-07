package fence

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// vehicle is the internal mutable vehicle record.
type vehicle struct {
	state   VehicleState
	fenceID string
}

// Service is the geofence dispatch service. A single mutex serializes every
// operation, so concurrent calls are linearizable and the result equals
// some serial execution. All state transitions happen only after every
// error check of the operation passed, hence rejected operations change
// nothing, including the clock.
type Service struct {
	mu       sync.Mutex
	cfg      Config
	loc      *time.Location
	fences   map[string]*Fence
	index    *spatialIndex
	counts   map[string]int // vehicles currently attributed per fence, O(1)
	vehicles map[string]*vehicle
	tasks    map[string]*Task
	order    []string // task IDs in creation order
	seq      int
	rewards  map[rewardKey]bool
	ledger   []LedgerEntry
	lastTS   int64
	hasTS    bool
}

// NewService validates the configuration and returns an empty service.
func NewService(cfg Config) (*Service, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("invalid timezone %q: %w", cfg.Timezone, err)
	}
	if cfg.EvacuationDen <= 0 {
		return nil, fmt.Errorf("EvacuationDen must be positive")
	}
	if cfg.EvacuationNum < 0 {
		return nil, fmt.Errorf("EvacuationNum must be >= 0")
	}
	if cfg.ClaimTimeoutSec < 0 {
		return nil, fmt.Errorf("ClaimTimeoutSec must be >= 0")
	}
	if cfg.OutsideFee < 0 || cfg.RewardAmount < 0 {
		return nil, fmt.Errorf("fees to charge must be >= 0")
	}
	return &Service{
		cfg:      cfg,
		loc:      loc,
		fences:   make(map[string]*Fence),
		index:    buildIndex(nil),
		counts:   make(map[string]int),
		vehicles: make(map[string]*vehicle),
		tasks:    make(map[string]*Task),
		rewards:  make(map[rewardKey]bool),
	}, nil
}

// checkClock enforces the monotone clock: an operation's timestamp must not
// be smaller than the last accepted operation's timestamp.
func (s *Service) checkClock(ts int64) error {
	if s.hasTS && ts < s.lastTS {
		return codeError(ErrClockRollback, "timestamp %d < last accepted %d", ts, s.lastTS)
	}
	return nil
}

func (s *Service) accept(ts int64) {
	s.lastTS = ts
	s.hasTS = true
}

// RegisterFence validates and registers a fence. Constraint violations
// against existing fences are rejected with ErrFenceConstraint.
func (s *Service) RegisterFence(f Fence, ts int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if f.ID == "" {
		return codeError(ErrInvalidArgument, "fence ID is empty")
	}
	if f.Type != Operating && f.Type != NoParking && f.Type != Reward {
		return codeError(ErrInvalidArgument, "unknown fence type %d", f.Type)
	}
	if f.Capacity < 0 {
		return codeError(ErrInvalidArgument, "negative capacity %d", f.Capacity)
	}
	if err := validateSimplePolygon(f.Vertices); err != nil {
		return codeError(ErrInvalidArgument, "fence %s: %v", f.ID, err)
	}
	if _, dup := s.fences[f.ID]; dup {
		return codeError(ErrInvalidArgument, "fence %s already registered", f.ID)
	}
	if err := s.checkClock(ts); err != nil {
		return err
	}
	parent, err := s.checkFenceConstraints(&f)
	if err != nil {
		return err
	}

	stored := f
	stored.Vertices = append([]Point(nil), f.Vertices...)
	stored.parentID = parent
	stored.minX, stored.minY, stored.maxX, stored.maxY = bbox(stored.Vertices)
	s.fences[stored.ID] = &stored
	s.index = buildIndex(s.fences)
	s.accept(ts)
	return nil
}

// checkFenceConstraints verifies the nesting and non-intersection rules and
// returns the containing operating fence ID for inner fences.
func (s *Service) checkFenceConstraints(f *Fence) (string, error) {
	if f.Type == Operating {
		for _, g := range s.fences {
			if !polygonsConflict(f.Vertices, g.Vertices) {
				continue
			}
			// A new operating fence may fully contain existing inner
			// fences; any other contact (overlap, touch, or nesting
			// with another operating fence) is rejected.
			if g.Type != Operating && containsPolygon(f.Vertices, g.Vertices) {
				continue
			}
			return "", codeError(ErrFenceConstraint,
				"operating fence %s conflicts with fence %s", f.ID, g.ID)
		}
		return "", nil
	}
	// Inner fences must lie completely inside exactly one operating fence
	// (operating fences are disjoint, so at most one can contain it).
	parent := ""
	for _, g := range s.fences {
		if g.Type == Operating && containsPolygon(g.Vertices, f.Vertices) {
			parent = g.ID
		}
	}
	if parent == "" {
		return "", codeError(ErrFenceConstraint,
			"inner fence %s is not completely inside any operating fence", f.ID)
	}
	for _, g := range s.fences {
		if g.Type == Operating {
			continue
		}
		if polygonsConflict(f.Vertices, g.Vertices) {
			return "", codeError(ErrFenceConstraint,
				"inner fence %s conflicts with inner fence %s", f.ID, g.ID)
		}
	}
	return parent, nil
}

// locate attributes a point to at most one fence per type via the grid
// index and resolves the attribution by priority reward > no-parking >
// operating. It also returns the number of candidate fences actually
// ray-cast, which is used to verify sub-linear query scaling.
func (s *Service) locate(p Point) (Attribution, int) {
	checked := 0
	var reward, noPark, oper string
	for _, id := range s.index.candidates(p) {
		f := s.fences[id]
		checked++
		if !pointInPolygon(f.Vertices, p) {
			continue
		}
		switch f.Type {
		case Reward:
			reward = id
		case NoParking:
			noPark = id
		case Operating:
			oper = id
		}
	}
	switch {
	case reward != "":
		return Attribution{Type: Reward, FenceID: reward}, checked
	case noPark != "":
		return Attribution{Type: NoParking, FenceID: noPark}, checked
	case oper != "":
		return Attribution{Type: Operating, FenceID: oper}, checked
	default:
		return Attribution{Type: Outside}, checked
	}
}

// dayKey splits timestamps into natural days in the configured timezone.
func (s *Service) dayKey(ts int64) string {
	y, m, d := time.Unix(ts, 0).In(s.loc).Date()
	return fmt.Sprintf("%04d-%02d-%02d", y, int(m), d)
}

// AddVehicle registers a vehicle in the idle state.
func (s *Service) AddVehicle(id string, ts int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == "" {
		return codeError(ErrInvalidArgument, "vehicle ID is empty")
	}
	if _, dup := s.vehicles[id]; dup {
		return codeError(ErrInvalidArgument, "vehicle %s already registered", id)
	}
	if err := s.checkClock(ts); err != nil {
		return err
	}
	s.vehicles[id] = &vehicle{state: VehicleIdle}
	s.accept(ts)
	return nil
}

// Unlock starts a ride. Unlocking a parked vehicle releases its fence slot.
func (s *Service) Unlock(vehicleID string, ts int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if vehicleID == "" {
		return codeError(ErrInvalidArgument, "vehicle ID is empty")
	}
	if err := s.checkClock(ts); err != nil {
		return err
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return codeError(ErrVehicleNotFound, "vehicle %s", vehicleID)
	}
	switch v.state {
	case VehicleParked:
		s.counts[v.fenceID]--
		v.fenceID = ""
		v.state = VehicleRiding
	case VehicleIdle:
		v.state = VehicleRiding
	default:
		return codeError(ErrInvalidArgument,
			"vehicle %s is %s, cannot unlock", vehicleID, v.state)
	}
	s.accept(ts)
	return nil
}

// Return ends a ride at the vehicle's current position.
func (s *Service) Return(vehicleID, userID string, p Point, ts int64) (ReturnResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res ReturnResult
	if vehicleID == "" || userID == "" {
		return res, codeError(ErrInvalidArgument, "empty vehicle or user ID")
	}
	if !coordOK(p) {
		return res, codeError(ErrInvalidArgument, "coordinate out of range")
	}
	if err := s.checkClock(ts); err != nil {
		return res, err
	}
	v, ok := s.vehicles[vehicleID]
	if !ok {
		return res, codeError(ErrVehicleNotFound, "vehicle %s", vehicleID)
	}
	if v.state != VehicleRiding {
		return res, codeError(ErrVehicleNotRiding, "vehicle %s is %s", vehicleID, v.state)
	}

	attr, _ := s.locate(p)
	switch attr.Type {
	case NoParking:
		return res, codeError(ErrNoParkingReturn, "point is in no-parking fence %s", attr.FenceID)
	case Reward, Operating:
		f := s.fences[attr.FenceID]
		if s.counts[f.ID] >= f.Capacity {
			return res, codeError(ErrFenceFull, "fence %s is full (%d/%d)",
				f.ID, s.counts[f.ID], f.Capacity)
		}
		v.state = VehicleParked
		v.fenceID = f.ID
		s.counts[f.ID]++
		res.FenceID = f.ID
		if f.Type == Reward {
			key := rewardKey{userID: userID, fenceID: f.ID, day: s.dayKey(ts)}
			if !s.rewards[key] {
				s.rewards[key] = true
				res.Reward = s.cfg.RewardAmount
				s.addLedger(ts, userID, vehicleID, f.ID, LedgerReward, res.Reward)
			}
		}
		if tid := s.maybeEvacuate(f, ts); tid != "" {
			res.TaskIDs = append(res.TaskIDs, tid)
		}
	default: // Outside
		res.Outside = true
		res.Fee = s.cfg.OutsideFee
		s.addLedger(ts, userID, vehicleID, "", LedgerOutsideFee, res.Fee)
		v.state = VehicleInTransit
		tid := s.newTask(RecallTask, "", s.cfg.DefaultOperatingFenceID, vehicleID, 1, ts)
		res.TaskIDs = append(res.TaskIDs, tid)
	}
	s.accept(ts)
	return res, nil
}

func (s *Service) addLedger(ts int64, userID, vehicleID, fenceID string, kind LedgerKind, amount int64) {
	s.ledger = append(s.ledger, LedgerEntry{
		Seq:       len(s.ledger) + 1,
		Time:      ts,
		UserID:    userID,
		VehicleID: vehicleID,
		FenceID:   fenceID,
		Kind:      kind,
		Amount:    amount,
	})
}

// newTask appends a task with a deterministic sequential ID.
func (s *Service) newTask(kind TaskKind, src, dst, vehicleID string, moveCount int, ts int64) string {
	s.seq++
	id := fmt.Sprintf("T%d", s.seq)
	s.tasks[id] = &Task{
		ID:            id,
		Kind:          kind,
		Status:        TaskPending,
		SourceFenceID: src,
		DestFenceID:   dst,
		VehicleID:     vehicleID,
		MoveCount:     moveCount,
		CreatedAt:     ts,
	}
	s.order = append(s.order, id)
	return id
}

// maybeEvacuate creates an evacuation task when the fence's vehicle count
// reaches the configured ratio of its capacity (equality triggers), no
// unfinished evacuation task exists for the fence, and an eligible
// destination exists. Returns the new task ID or "".
func (s *Service) maybeEvacuate(f *Fence, ts int64) string {
	num, den := s.cfg.EvacuationNum, s.cfg.EvacuationDen
	if num <= 0 || f.Capacity <= 0 {
		return ""
	}
	count := int64(s.counts[f.ID])
	if count*den < num*int64(f.Capacity) {
		return ""
	}
	for _, id := range s.order {
		t := s.tasks[id]
		if t.Kind == EvacuateTask && t.SourceFenceID == f.ID && t.Status != TaskCompleted {
			return "" // one unfinished evacuation task per fence at a time
		}
	}
	dest := s.leastLoadedDestination(f)
	if dest == "" {
		return ""
	}
	// The surplus is the amount exceeding the trigger threshold: moving it
	// brings the fence just below the ratio again.
	threshold := (num*int64(f.Capacity) + den - 1) / den // minimal triggering count
	move := int(count-threshold) + 1
	if move < 1 {
		move = 1
	}
	return s.newTask(EvacuateTask, f.ID, dest, "", move, ts)
}

// leastLoadedDestination picks the fence with the fewest vehicles inside the
// same operating fence as f, excluding f itself and all no-parking fences.
// Ties break on the smaller fence ID, keeping the choice deterministic.
func (s *Service) leastLoadedDestination(f *Fence) string {
	best := ""
	bestCount := 0
	consider := func(id string) {
		c := s.counts[id]
		if best == "" || c < bestCount || (c == bestCount && id < best) {
			best, bestCount = id, c
		}
	}
	if f.Type == Operating {
		for id, g := range s.fences {
			if g.parentID == f.ID && g.Type == Reward {
				consider(id)
			}
		}
		return best
	}
	if f.parentID != "" {
		consider(f.parentID)
	}
	for id, g := range s.fences {
		if id != f.ID && g.parentID == f.parentID && g.Type == Reward {
			consider(id)
		}
	}
	return best
}

// effectiveStatus computes the task status at time ts, lazily treating
// claims whose timeout has fully elapsed (elapsed >= ClaimTimeoutSec) as
// expired. It does not mutate anything.
func (s *Service) effectiveStatus(t *Task, ts int64) TaskStatus {
	if t.Status == TaskInProgress && len(t.Claims) > 0 {
		if ts-t.Claims[len(t.Claims)-1].ClaimedAt >= s.cfg.ClaimTimeoutSec {
			return TaskPending
		}
	}
	return t.Status
}

// ClaimTask lets a dispatcher claim a pending task.
func (s *Service) ClaimTask(taskID, dispatcherID string, ts int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if taskID == "" || dispatcherID == "" {
		return codeError(ErrInvalidArgument, "empty task or dispatcher ID")
	}
	if err := s.checkClock(ts); err != nil {
		return err
	}
	t, ok := s.tasks[taskID]
	if !ok {
		return codeError(ErrTaskNotFound, "task %s", taskID)
	}
	if s.effectiveStatus(t, ts) != TaskPending {
		return codeError(ErrTaskAlreadyClaimed, "task %s is not claimable", taskID)
	}
	if t.Status == TaskInProgress {
		// The previous claim expired: keep its record, marked expired.
		t.Claims[len(t.Claims)-1].Expired = true
	}
	t.Status = TaskInProgress
	t.Claims = append(t.Claims, ClaimRecord{DispatcherID: dispatcherID, ClaimedAt: ts})
	s.accept(ts)
	return nil
}

// CompleteTask finishes an in-progress task, reporting the actual drop
// point. A drop into a full fence is rejected with ErrFenceFull and the
// task stays in progress; a drop into a no-parking fence or outside every
// fence is rejected with ErrInvalidDropPoint.
func (s *Service) CompleteTask(taskID string, p Point, ts int64) (CompleteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var res CompleteResult
	if taskID == "" {
		return res, codeError(ErrInvalidArgument, "empty task ID")
	}
	if !coordOK(p) {
		return res, codeError(ErrInvalidArgument, "coordinate out of range")
	}
	if err := s.checkClock(ts); err != nil {
		return res, err
	}
	attr, _ := s.locate(p)
	if attr.Type == Reward || attr.Type == Operating {
		if f := s.fences[attr.FenceID]; s.counts[f.ID] >= f.Capacity {
			return res, codeError(ErrFenceFull, "drop fence %s is full (%d/%d)",
				f.ID, s.counts[f.ID], f.Capacity)
		}
	}
	t, ok := s.tasks[taskID]
	if !ok {
		return res, codeError(ErrTaskNotFound, "task %s", taskID)
	}
	if s.effectiveStatus(t, ts) != TaskInProgress {
		return res, codeError(ErrTaskNotInProgress, "task %s is not in progress", taskID)
	}
	if attr.Type == NoParking || attr.Type == Outside {
		return res, codeError(ErrInvalidDropPoint, "drop point resolves to %s", attr.Type)
	}

	f := s.fences[attr.FenceID]
	if t.Kind == RecallTask {
		v := s.vehicles[t.VehicleID]
		v.state = VehicleParked
		v.fenceID = f.ID
		s.counts[f.ID]++
		res.Moved = 1
	} else {
		free := f.Capacity - s.counts[f.ID]
		moved := 0
		for _, id := range s.parkedVehicles(t.SourceFenceID) {
			if moved >= t.MoveCount || moved >= free {
				break
			}
			s.vehicles[id].fenceID = f.ID
			s.counts[t.SourceFenceID]--
			s.counts[f.ID]++
			moved++
		}
		res.Moved = moved
	}
	t.Status = TaskCompleted
	res.FenceID = f.ID
	if tid := s.maybeEvacuate(f, ts); tid != "" {
		res.TaskIDs = append(res.TaskIDs, tid)
	}
	s.accept(ts)
	return res, nil
}

// parkedVehicles returns the IDs of vehicles parked in a fence, sorted for
// deterministic evacuation picks.
func (s *Service) parkedVehicles(fenceID string) []string {
	var ids []string
	for id, v := range s.vehicles {
		if v.state == VehicleParked && v.fenceID == fenceID {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// Locate attributes a point to a fence. It is read-only and does not touch
// the clock.
func (s *Service) Locate(p Point) Attribution {
	s.mu.Lock()
	defer s.mu.Unlock()
	attr, _ := s.locate(p)
	return attr
}

// LocateStats additionally reports how many candidate fences were
// ray-cast, allowing tests to verify that point queries do not scan all
// fences.
func (s *Service) LocateStats(p Point) (Attribution, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.locate(p)
}

// Count returns the number of vehicles currently attributed to a fence in
// O(1), independent of the total vehicle count.
func (s *Service) Count(fenceID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.fences[fenceID]; !ok {
		return 0, codeError(ErrFenceNotFound, "fence %s", fenceID)
	}
	return s.counts[fenceID], nil
}

// Task returns a copy of a task with its effective status as of the last
// accepted operation's timestamp.
func (s *Service) Task(id string) (Task, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok {
		return Task{}, false
	}
	cp := *t
	cp.Claims = append([]ClaimRecord(nil), t.Claims...)
	cp.Status = s.effectiveStatus(t, s.lastTS)
	return cp, true
}

// Tasks returns copies of all tasks in creation order, with effective
// statuses as of the last accepted operation's timestamp.
func (s *Service) Tasks() []Task {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.order))
	for _, id := range s.order {
		t := s.tasks[id]
		cp := *t
		cp.Claims = append([]ClaimRecord(nil), t.Claims...)
		cp.Status = s.effectiveStatus(t, s.lastTS)
		out = append(out, cp)
	}
	return out
}

// Ledger returns a copy of every money event in order.
func (s *Service) Ledger() []LedgerEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]LedgerEntry(nil), s.ledger...)
}

// Dump is a full, deterministic snapshot of the service state, used to
// verify replay determinism and to compare against the naive model.
type Dump struct {
	LastTS         int64
	Counts         map[string]int
	Vehicles       map[string]VehicleInfo
	Tasks          []Task
	Ledger         []LedgerEntry
	RewardsGranted int
}

// Dump snapshots the whole state. Task statuses are effective as of the
// last accepted operation's timestamp.
func (s *Service) Dump() Dump {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := Dump{
		LastTS:         s.lastTS,
		Counts:         make(map[string]int, len(s.fences)),
		Vehicles:       make(map[string]VehicleInfo, len(s.vehicles)),
		Tasks:          make([]Task, 0, len(s.order)),
		Ledger:         append([]LedgerEntry(nil), s.ledger...),
		RewardsGranted: len(s.rewards),
	}
	for id := range s.fences {
		d.Counts[id] = s.counts[id]
	}
	for id, v := range s.vehicles {
		d.Vehicles[id] = VehicleInfo{State: v.state, FenceID: v.fenceID}
	}
	for _, id := range s.order {
		t := s.tasks[id]
		cp := *t
		cp.Claims = append([]ClaimRecord(nil), t.Claims...)
		cp.Status = s.effectiveStatus(t, s.lastTS)
		d.Tasks = append(d.Tasks, cp)
	}
	return d
}
