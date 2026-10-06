package presence

import (
	"container/heap"
	"sort"
	"sync"
	"sync/atomic"
)

// Config configures a Service. LeaseSeconds must be in [1, 3600].
type Config struct {
	LeaseSeconds int64
	Shards       int
}

type shard struct {
	mu    sync.Mutex
	users map[string]*userRec
}

// Service is the presence aggregation and subscription service.
type Service struct {
	cfg    Config
	shards []*shard
	seq    atomic.Uint64
}

// New creates a Service. It returns ErrInvalidArgument for a bad config.
func New(cfg Config) (*Service, error) {
	if cfg.LeaseSeconds < 1 || cfg.LeaseSeconds > 3600 {
		return nil, ErrInvalidArgument
	}
	if cfg.Shards <= 0 {
		cfg.Shards = 64
	}
	s := &Service{cfg: cfg, shards: make([]*shard, cfg.Shards)}
	for i := range s.shards {
		s.shards[i] = &shard{users: make(map[string]*userRec)}
	}
	return s, nil
}

func (s *Service) shardFor(id string) *shard {
	// Zero-allocation FNV-1a 32-bit hash.
	const (
		offset32 = uint32(2166136261)
		prime32  = uint32(16777619)
	)
	h := offset32
	for i := 0; i < len(id); i++ {
		h ^= uint32(id[i])
		h *= prime32
	}
	return s.shards[int(h%uint32(len(s.shards)))]
}

// lockShards locks every shard owning one of ids in ascending shard order,
// which makes multi-shard operations deadlock-free under any call mix.
func (s *Service) lockShards(ids ...string) func() {
	var ordered [2]*shard
	n := 0
	for _, id := range ids {
		sh := s.shardFor(id)
		dup := false
		for i := 0; i < n; i++ {
			if ordered[i] == sh {
				dup = true
				break
			}
		}
		if !dup {
			ordered[n] = sh
			n++
		}
	}
	if n == 2 && s.shardIndex(ordered[0]) > s.shardIndex(ordered[1]) {
		ordered[0], ordered[1] = ordered[1], ordered[0]
	}
	for i := 0; i < n; i++ {
		ordered[i].mu.Lock()
	}
	return func() {
		for i := n - 1; i >= 0; i-- {
			ordered[i].mu.Unlock()
		}
	}
}

func (s *Service) shardIndex(sh *shard) int {
	for i := range s.shards {
		if s.shards[i] == sh {
			return i
		}
	}
	return 0
}

func unlockShards(locked []*shard) {
	for i := len(locked) - 1; i >= 0; i-- {
		locked[i].mu.Unlock()
	}
}

func (s *Service) nextSeq() uint64 { return s.seq.Add(1) }

func validTime(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func validDeviceStatus(st Status) bool { return st >= Away && st <= Online }

// advance makes every expired device of u effective, one at a time, in expiry
// order. Each expiry recomputes the real aggregate and emits a visibility
// event stamped with the expiry instant, not the processing instant. Because
// every mutation touching u advances first, no visibility switch can have
// been committed while an earlier expiry was still pending: flags seen here
// are exactly the flags in force at the expiry time.
func (s *Service) advance(u *userRec, now int64) {
	for u.expiries.Len() > 0 && u.expiries[0].expiry <= now {
		d := heap.Pop(&u.expiries).(*device)
		delete(u.devices, d.id)
		u.activeCnt--
		u.recompute()
		eff := d.expiry
		for viewer, sb := range u.subs {
			sb.emit(u.id, u.visibleTo(viewer), eff, d.originSeq, false)
		}
	}
}

// emitVisible fans a current visibility change out to all subscribers.
func (s *Service) emitVisible(u *userRec, now int64) {
	seq := s.nextSeq()
	for viewer, sb := range u.subs {
		sb.emit(u.id, u.visibleTo(viewer), now, seq, true)
	}
}

// Report upserts a device heartbeat with status and renews its lease.
// Unknown users are auto-created; a new device is rejected with
// ErrTooManyDevices once 8 active devices are registered.
func (s *Service) Report(user, devID string, status Status, now int64) error {
	if user == "" || devID == "" || !validDeviceStatus(status) || !validTime(now) {
		return ErrInvalidArgument
	}
	unlock := s.lockShards(user)
	defer unlock()
	sh := s.shardFor(user)
	u := sh.users[user]
	if u == nil {
		u = newUserRec(user)
		sh.users[user] = u
	} else if now < u.lastNow {
		return ErrClockBackwards
	}
	s.advance(u, now)

	d := u.devices[devID]
	if d == nil {
		if u.activeCnt >= 8 {
			return ErrTooManyDevices
		}
		d = &device{id: devID}
		u.devices[devID] = d
		heap.Push(&u.expiries, d)
		u.activeCnt++
	}
	d.status = status
	d.expiry = now + s.cfg.LeaseSeconds
	d.originSeq = s.nextSeq()
	heap.Fix(&u.expiries, d.index)
	u.recompute()
	u.lastNow = now
	s.emitVisible(u, now)
	return nil
}

// Offline immediately invalidates an existing device lease.
func (s *Service) Offline(user, devID string, now int64) error {
	if user == "" || devID == "" || !validTime(now) {
		return ErrInvalidArgument
	}
	unlock := s.lockShards(user)
	defer unlock()
	u := s.shardFor(user).users[user]
	if u == nil {
		return ErrNotFound
	}
	if now < u.lastNow {
		return ErrClockBackwards
	}
	d := u.devices[devID]
	if d == nil {
		return ErrNotFound
	}
	// Validate against the logical state that includes pending expiries: a
	// device whose lease is already due is no longer registered. Reject
	// before applying anything, so a rejected operation commits no effects.
	if d.expiry <= now {
		return ErrNotFound
	}
	s.advance(u, now)
	heap.Remove(&u.expiries, d.index)
	delete(u.devices, devID)
	u.activeCnt--
	u.recompute()
	u.lastNow = now
	s.emitVisible(u, now)
	return nil
}

// SetInvisible toggles the owner's invisibility flag. While invisible every
// other observer sees offline; the owner still sees the real aggregate.
func (s *Service) SetInvisible(user string, on bool, now int64) error {
	if user == "" || !validTime(now) {
		return ErrInvalidArgument
	}
	unlock := s.lockShards(user)
	defer unlock()
	u := s.shardFor(user).users[user]
	if u == nil {
		return ErrNotFound
	}
	if now < u.lastNow {
		return ErrClockBackwards
	}
	s.advance(u, now)
	if u.invisible != on {
		u.invisible = on
		s.emitVisible(u, now)
	}
	u.lastNow = now
	return nil
}

// Block makes who always see owner as offline and suppresses owner
// notifications while the block lasts. Self-blocking is rejected.
func (s *Service) Block(owner, who string, now int64) error {
	return s.setBlock(owner, who, now, true)
}

// Unblock reverses Block; only the current state is revealed, never backlog.
func (s *Service) Unblock(owner, who string, now int64) error {
	return s.setBlock(owner, who, now, false)
}

func (s *Service) setBlock(owner, who string, now int64, block bool) error {
	if owner == "" || who == "" || !validTime(now) {
		return ErrInvalidArgument
	}
	if owner == who {
		return ErrCannotBlockSelf
	}
	unlock := s.lockShards(owner, who)
	defer unlock()
	u := s.shardFor(owner).users[owner]
	if u == nil {
		return ErrNotFound
	}
	v := s.shardFor(who).users[who]
	if v == nil {
		return ErrNotFound
	}
	if now < u.lastNow || now < v.lastNow {
		return ErrClockBackwards
	}
	s.advance(u, now)
	if u.blocked[who] != block {
		u.blocked[who] = block
		s.emitVisible(u, now)
	}
	u.lastNow = now
	v.lastNow = now
	return nil
}

// Subscribe starts watching target from viewer. The initial visible state is
// recorded without producing a notification.
func (s *Service) Subscribe(viewer, target string, now int64) error {
	if viewer == "" || target == "" || !validTime(now) {
		return ErrInvalidArgument
	}
	if viewer == target {
		return ErrCannotSubscribeSelf
	}
	unlock := s.lockShards(viewer, target)
	defer unlock()
	v := s.shardFor(viewer).users[viewer]
	if v == nil {
		return ErrNotFound
	}
	u := s.shardFor(target).users[target]
	if u == nil {
		return ErrNotFound
	}
	if now < v.lastNow || now < u.lastNow {
		return ErrClockBackwards
	}
	if v.subscribedTo[target] {
		return ErrAlreadySubscribed
	}
	s.advance(v, now)
	s.advance(u, now)
	u.subs[viewer] = &sub{last: u.visibleTo(viewer)}
	v.subscribedTo[target] = true
	v.lastNow = now
	u.lastNow = now
	return nil
}

// Unsubscribe removes a subscription and its undrained notifications.
func (s *Service) Unsubscribe(viewer, target string, now int64) error {
	if viewer == "" || target == "" || !validTime(now) {
		return ErrInvalidArgument
	}
	unlock := s.lockShards(viewer, target)
	defer unlock()
	v := s.shardFor(viewer).users[viewer]
	u := s.shardFor(target).users[target]
	if v == nil || u == nil || !v.subscribedTo[target] {
		return ErrNotFound
	}
	if now < v.lastNow || now < u.lastNow {
		return ErrClockBackwards
	}
	delete(v.subscribedTo, target)
	delete(u.subs, viewer)
	v.lastNow = now
	u.lastNow = now
	return nil
}

// Query returns the visible status and active device count. Self queries also
// return per-device detail.
func (s *Service) Query(viewer, target string, now int64) (QueryResult, error) {
	if viewer == "" || target == "" || !validTime(now) {
		return QueryResult{}, ErrInvalidArgument
	}
	unlock := s.lockShards(viewer, target)
	defer unlock()
	v := s.shardFor(viewer).users[viewer]
	if v == nil {
		return QueryResult{}, ErrNotFound
	}
	u := s.shardFor(target).users[target]
	if u == nil {
		return QueryResult{}, ErrNotFound
	}
	if now < v.lastNow || now < u.lastNow {
		return QueryResult{}, ErrClockBackwards
	}
	s.advance(v, now)
	s.advance(u, now)
	res := QueryResult{Status: u.visibleTo(viewer), ActiveDevice: u.activeCnt}
	if viewer == target {
		res.Devices = make([]DeviceInfo, 0, len(u.devices))
		for _, d := range u.devices {
			res.Devices = append(res.Devices, DeviceInfo{
				Device: d.id, Status: d.status, Expiry: d.expiry,
			})
		}
		sort.Slice(res.Devices, func(i, j int) bool {
			return res.Devices[i].Device < res.Devices[j].Device
		})
	}
	v.lastNow = now
	u.lastNow = now
	return res, nil
}

// Drain returns queued visible-state changes for the viewer, merged across
// targets by effective time (expiry instant for expiries, operation now for
// switches); ties resolve with the global arrival sequence, under which
// same-instant expiries are emitted before the switch that processes them.
func (s *Service) Drain(viewer string, now int64) (DrainResult, error) {
	if viewer == "" || !validTime(now) {
		return DrainResult{}, ErrInvalidArgument
	}
	vsh := s.shardFor(viewer)
	vsh.mu.Lock()
	v := vsh.users[viewer]
	if v == nil {
		vsh.mu.Unlock()
		return DrainResult{}, ErrNotFound
	}
	if now < v.lastNow {
		vsh.mu.Unlock()
		return DrainResult{}, ErrClockBackwards
	}
	targets := make([]string, 0, len(v.subscribedTo))
	for t := range v.subscribedTo {
		targets = append(targets, t)
	}
	sort.Strings(targets)
	vsh.mu.Unlock()

	// Lock every target shard up front (in a deterministic order) and
	// precheck clocks before any state changes, so a rejected drain is
	// all-or-nothing. Releasing afterwards guarantees no nested locking with
	// Subscribe.
	lockedSet := map[*shard]bool{}
	for _, t := range targets {
		lockedSet[s.shardFor(t)] = true
	}
	// Lock in ascending shard-index order, identical to every other
	// multi-shard operation, so Drain can never form a lock cycle.
	locked := make([]*shard, 0, len(lockedSet))
	for sh := range lockedSet {
		locked = append(locked, sh)
	}
	sort.Slice(locked, func(i, j int) bool { return s.shardIndex(locked[i]) < s.shardIndex(locked[j]) })
	for _, sh := range locked {
		sh.mu.Lock()
	}
	for _, t := range targets {
		if u := s.shardFor(t).users[t]; u != nil && now < u.lastNow {
			unlockShards(locked)
			return DrainResult{}, ErrClockBackwards
		}
	}

	vsh.mu.Lock()
	s.advance(v, now)
	v.lastNow = now
	vsh.mu.Unlock()

	var merged []Notification
	for _, t := range targets {
		if u := s.shardFor(t).users[t]; u != nil {
			s.advance(u, now)
			u.lastNow = now
			if sb := u.subs[viewer]; sb != nil {
				merged = append(merged, sb.pending...)
				sb.pending = sb.pending[:0]
			}
		}
	}
	unlockShards(locked)

	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Effective != merged[j].Effective {
			return merged[i].Effective < merged[j].Effective
		}
		if merged[i].switchEvent != merged[j].switchEvent {
			return !merged[i].switchEvent // expiry (false) before switch (true)
		}
		return merged[i].Seq < merged[j].Seq
	})

	dropped := 0
	if excess := len(merged) - 1000; excess > 0 {
		dropped = excess
		merged = merged[excess:]
	}

	vsh.mu.Lock()
	v.lastNow = now
	vsh.mu.Unlock()

	return DrainResult{Notifications: merged, Dropped: dropped}, nil
}
