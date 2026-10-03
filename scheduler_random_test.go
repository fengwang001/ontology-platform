package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type modelPeer struct {
	have     map[int]bool
	failed   map[int]bool
	inflight map[int]int64
	timeouts int
	failures int
}

type naiveScheduler struct {
	cfg       Config
	now       int64
	peers     map[string]*modelPeer
	banned    map[string]bool
	completed map[int]bool
}

type modelDoneResult struct {
	Banned   bool
	Canceled []string
}

type modelExpired struct {
	Issued int64
	PeerID string
	Block  int
}

type randomEvent struct {
	kind   string
	now    int64
	id     string
	block  int
	ok     bool
	have   []bool
	reason string
}

func newNaiveScheduler(cfg Config) *naiveScheduler {
	return &naiveScheduler{
		cfg:       cfg,
		peers:     make(map[string]*modelPeer),
		banned:    make(map[string]bool),
		completed: make(map[int]bool),
	}
}

func (m *naiveScheduler) checkClock(now int64) error {
	if now < m.now {
		return ErrClock
	}
	m.now = now
	return nil
}

func (m *naiveScheduler) availability(block int) int {
	count := 0
	for id, peer := range m.peers {
		if !m.banned[id] && peer.have[block] {
			count++
		}
	}
	return count
}

func (m *naiveScheduler) blockInflight(block int) int {
	count := 0
	for _, peer := range m.peers {
		if _, ok := peer.inflight[block]; ok {
			count++
		}
	}
	return count
}

func (m *naiveScheduler) globalInflight() int {
	count := 0
	for _, peer := range m.peers {
		count += len(peer.inflight)
	}
	return count
}

func (m *naiveScheduler) complete() bool {
	return len(m.completed) == m.cfg.Blocks
}

func (m *naiveScheduler) addPeer(id string, have []bool) error {
	if id == "" || len(have) != m.cfg.Blocks {
		return ErrBadArg
	}
	if _, exists := m.peers[id]; exists {
		return ErrPeerExists
	}
	if m.banned[id] {
		return ErrBanned
	}
	peer := &modelPeer{
		have:     make(map[int]bool),
		failed:   make(map[int]bool),
		inflight: make(map[int]int64),
	}
	for block, exists := range have {
		peer.have[block] = exists
	}
	m.peers[id] = peer
	return nil
}

func (m *naiveScheduler) have(id string, block int) error {
	peer := m.peers[id]
	if peer == nil {
		return ErrNoPeer
	}
	if block < 0 || block >= m.cfg.Blocks {
		return ErrBadArg
	}
	peer.have[block] = true
	return nil
}

func (m *naiveScheduler) drop(id string) error {
	if m.peers[id] == nil {
		return ErrNoPeer
	}
	delete(m.peers, id)
	return nil
}

func (m *naiveScheduler) capacity(peer *modelPeer) int {
	capacity := m.cfg.BaseConcurrency - peer.timeouts/2
	if capacity < 1 {
		return 1
	}
	return capacity
}

func (m *naiveScheduler) next(now int64, id string) (int, bool, error) {
	if err := m.checkClock(now); err != nil {
		return 0, false, err
	}
	peer := m.peers[id]
	if peer == nil {
		return 0, false, ErrNoPeer
	}
	if m.banned[id] {
		return 0, false, ErrBanned
	}
	if len(peer.inflight) >= m.capacity(peer) || m.globalInflight() >= m.cfg.GlobalInflightLimit || m.complete() {
		return 0, false, nil
	}

	hasFresh := false
	for block := 0; block < m.cfg.Blocks; block++ {
		if !m.completed[block] && m.blockInflight(block) == 0 && m.availability(block) > 0 {
			hasFresh = true
			break
		}
	}

	type candidate struct {
		block       int
		available   int
		blockActive int
	}
	var candidates []candidate
	for block := 0; block < m.cfg.Blocks; block++ {
		if m.completed[block] || !peer.have[block] || peer.failed[block] {
			continue
		}
		if hasFresh {
			if m.blockInflight(block) == 0 && m.availability(block) > 0 {
				candidates = append(candidates, candidate{block: block, available: m.availability(block)})
			}
			continue
		}
		if _, active := peer.inflight[block]; active || m.blockInflight(block) >= m.cfg.MaxBlockRequests {
			continue
		}
		candidates = append(candidates, candidate{
			block:       block,
			available:   m.availability(block),
			blockActive: m.blockInflight(block),
		})
	}
	if len(candidates) == 0 {
		return 0, false, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if hasFresh {
			if candidates[i].available != candidates[j].available {
				return candidates[i].available < candidates[j].available
			}
			return candidates[i].block < candidates[j].block
		}
		if candidates[i].blockActive != candidates[j].blockActive {
			return candidates[i].blockActive < candidates[j].blockActive
		}
		if candidates[i].available != candidates[j].available {
			return candidates[i].available < candidates[j].available
		}
		return candidates[i].block < candidates[j].block
	})

	chosen := candidates[0].block
	peer.inflight[chosen] = now
	return chosen, true, nil
}

func (m *naiveScheduler) done(now int64, id string, block int, ok bool) (modelDoneResult, error) {
	if err := m.checkClock(now); err != nil {
		return modelDoneResult{}, err
	}
	peer := m.peers[id]
	if peer == nil {
		return modelDoneResult{}, ErrNoPeer
	}
	if block < 0 || block >= m.cfg.Blocks {
		return modelDoneResult{}, ErrBadArg
	}
	if _, active := peer.inflight[block]; !active {
		return modelDoneResult{}, ErrNoRequest
	}

	delete(peer.inflight, block)
	if ok {
		peer.timeouts = 0
		m.completed[block] = true
		canceled := make([]string, 0)
		for otherID, other := range m.peers {
			if otherID != id {
				if _, active := other.inflight[block]; active {
					delete(other.inflight, block)
					canceled = append(canceled, otherID)
				}
			}
		}
		sort.Strings(canceled)
		return modelDoneResult{Canceled: canceled}, nil
	}

	peer.failed[block] = true
	peer.failures++
	if peer.failures < m.cfg.FailureBanThreshold {
		return modelDoneResult{}, nil
	}
	m.banned[id] = true
	peer.inflight = make(map[int]int64)
	return modelDoneResult{Banned: true}, nil
}

func (m *naiveScheduler) tick(now int64) ([]modelExpired, error) {
	if err := m.checkClock(now); err != nil {
		return nil, err
	}
	expired := make([]modelExpired, 0)
	for id, peer := range m.peers {
		if m.banned[id] {
			continue
		}
		for block, issued := range peer.inflight {
			if now-issued >= m.cfg.Timeout {
				expired = append(expired, modelExpired{Issued: issued, PeerID: id, Block: block})
			}
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].Issued != expired[j].Issued {
			return expired[i].Issued < expired[j].Issued
		}
		if expired[i].PeerID != expired[j].PeerID {
			return expired[i].PeerID < expired[j].PeerID
		}
		return expired[i].Block < expired[j].Block
	})
	for _, request := range expired {
		delete(m.peers[request.PeerID].inflight, request.Block)
		m.peers[request.PeerID].timeouts++
	}
	return expired, nil
}

type modelResult struct {
	nextBlock int
	nextOK    bool
	nextErr   error
	done      modelDoneResult
	doneErr   error
	expired   []modelExpired
	tickErr   error
	complete  bool
	addErr    error
	haveErr   error
	dropErr   error
}

type actualResult struct {
	nextBlock int
	nextOK    bool
	nextErr   error
	done      DoneResult
	doneErr   error
	expired   []ExpiredRequest
	tickErr   error
	complete  bool
	addErr    error
	haveErr   error
	dropErr   error
}

func TestRandomEventsMatchNaiveScheduler(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	const sequences = 2000

	for sequence := 0; sequence < sequences; sequence++ {
		cfg := Config{
			Blocks:              1 + rng.Intn(6),
			BaseConcurrency:     1 + rng.Intn(4),
			GlobalInflightLimit: 1 + rng.Intn(5),
			MaxBlockRequests:    2 + rng.Intn(3),
			Timeout:             int64(1 + rng.Intn(5)),
			FailureBanThreshold: 1 + rng.Intn(3),
		}
		actual, err := NewBlockScheduler(cfg)
		if err != nil {
			t.Fatal(err)
		}
		naive := newNaiveScheduler(cfg)
		now := int64(0)

		for step := 0; step < 60; step++ {
			event := generateRandomEvent(t, rng, cfg, naive, &now)
			got := applyActualEvent(actual, event)
			want := applyModelEvent(naive, event)
			t.Logf("sequence=%d step=%d cfg=%#v event=%+v actual=%#v model=%#v", sequence, step, cfg, event, got, want)
			if violation := schedulerInvariantViolation(actual); violation != "" {
				t.Fatalf("sequence=%d step=%d invariant violation=%q event=%#v", sequence, step, violation, event)
			}
			if mismatch := resultMismatch(got, want); mismatch != "" {
				t.Fatalf("sequence=%d step=%d mismatch=%s event=%#v\nactual=%#v\nmodel=%#v", sequence, step, mismatch, event, got, want)
			}
		}
	}
}

func schedulerInvariantViolation(scheduler *BlockScheduler) string {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()

	summed := 0
	for block := 0; block < scheduler.blocks; block++ {
		if scheduler.completed[block] && scheduler.blockInflight[block] != 0 {
			return fmt.Sprintf("completed block %d has %d inflight", block, scheduler.blockInflight[block])
		}
		if scheduler.blockInflight[block] > scheduler.maxBlockRequests {
			return fmt.Sprintf("block %d inflight %d exceeds M", block, scheduler.blockInflight[block])
		}
		summed += scheduler.blockInflight[block]
	}
	if summed != scheduler.globalInflight {
		return fmt.Sprintf("block inflight sum %d differs from global %d", summed, scheduler.globalInflight)
	}
	if scheduler.globalInflight > scheduler.globalInflightLimit {
		return fmt.Sprintf("global inflight %d exceeds G", scheduler.globalInflight)
	}

	peerSummed := 0
	for id, peer := range scheduler.peers {
		peerSummed += len(peer.inflight)
		if scheduler.banned[id] && len(peer.inflight) != 0 {
			return fmt.Sprintf("banned peer %s has %d inflight", id, len(peer.inflight))
		}
		for block := range peer.inflight {
			if block < 0 || block >= scheduler.blocks || scheduler.completed[block] {
				return fmt.Sprintf("peer %s has invalid/completed inflight block %d", id, block)
			}
			if peer.failed[block] {
				return fmt.Sprintf("peer %s requests failed block %d", id, block)
			}
		}
	}
	if peerSummed != scheduler.globalInflight {
		return fmt.Sprintf("peer inflight sum %d differs from global %d", peerSummed, scheduler.globalInflight)
	}
	return ""
}

func generateRandomEvent(t *testing.T, rng *rand.Rand, cfg Config, model *naiveScheduler, now *int64) randomEvent {
	t.Helper()

	switch rng.Intn(10) {
	case 0:
		*now += int64(rng.Intn(3))
		return randomEvent{kind: "tick", now: *now, reason: "advance clock and expire requests when now-issued >= T"}
	case 1:
		id := randomPeerID(rng)
		have := make([]bool, cfg.Blocks)
		for block := range have {
			have[block] = rng.Intn(2) == 0
		}
		return randomEvent{kind: "add", now: *now, id: id, have: have, reason: "validate arguments, existing peer, then permanent ban"}
	case 2:
		id := randomExistingPeerID(rng, model)
		if id == "" {
			id = randomPeerID(rng)
		}
		return randomEvent{kind: "drop", now: *now, id: id, reason: "remove peer and release every inflight slot it owns"}
	case 3:
		id := randomExistingPeerID(rng, model)
		if id == "" {
			id = randomPeerID(rng)
		}
		block := rng.Intn(cfg.Blocks + 1)
		return randomEvent{kind: "have", now: *now, id: id, block: block, reason: "missing peer precedes block-range validation"}
	case 4, 5, 6:
		id := randomExistingPeerID(rng, model)
		if id == "" {
			id = randomPeerID(rng)
		}
		return randomEvent{kind: "next", now: *now, id: id, reason: "check clock/peer/ban, capacity, then fresh or endgame ordering"}
	default:
		id := randomExistingPeerID(rng, model)
		if id == "" {
			id = randomPeerID(rng)
		}
		block := rng.Intn(cfg.Blocks + 1)
		return randomEvent{
			kind:   "done",
			now:    *now,
			id:     id,
			block:  block,
			ok:     rng.Intn(2) == 0,
			reason: "complete or fail exactly the peer-block request that is still inflight",
		}
	}
}

func randomPeerID(rng *rand.Rand) string {
	return fmt.Sprintf("p%d", rng.Intn(5))
}

func randomExistingPeerID(rng *rand.Rand, model *naiveScheduler) string {
	ids := make([]string, 0, len(model.peers))
	for id := range model.peers {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return ""
	}
	sort.Strings(ids)
	return ids[rng.Intn(len(ids))]
}

func applyActualEvent(scheduler *BlockScheduler, event randomEvent) actualResult {
	result := actualResult{}
	switch event.kind {
	case "add":
		result.addErr = scheduler.AddPeer(event.id, event.have)
	case "have":
		result.haveErr = scheduler.Have(event.id, event.block)
	case "drop":
		result.dropErr = scheduler.Drop(event.id)
	case "next":
		result.nextBlock, result.nextOK, result.nextErr = scheduler.Next(event.now, event.id)
	case "done":
		result.done, result.doneErr = scheduler.Done(event.now, event.id, event.block, event.ok)
	case "tick":
		result.expired, result.tickErr = scheduler.Tick(event.now)
	}
	result.complete = scheduler.Complete()
	return result
}

func applyModelEvent(model *naiveScheduler, event randomEvent) modelResult {
	result := modelResult{}
	switch event.kind {
	case "add":
		result.addErr = model.addPeer(event.id, event.have)
	case "have":
		result.haveErr = model.have(event.id, event.block)
	case "drop":
		result.dropErr = model.drop(event.id)
	case "next":
		result.nextBlock, result.nextOK, result.nextErr = model.next(event.now, event.id)
	case "done":
		result.done, result.doneErr = model.done(event.now, event.id, event.block, event.ok)
	case "tick":
		result.expired, result.tickErr = model.tick(event.now)
	}
	result.complete = model.complete()
	return result
}

func resultMismatch(actual actualResult, want modelResult) string {
	checks := []struct {
		name        string
		actual      any
		want        any
		deepCompare bool
	}{
		{"next block", actual.nextBlock, want.nextBlock, false},
		{"next ok", actual.nextOK, want.nextOK, false},
		{"next error", errorString(actual.nextErr), errorString(want.nextErr), false},
		{"done banned", actual.done.Banned, want.done.Banned, false},
		{"done canceled", stringSliceSignature(actual.done.Canceled), stringSliceSignature(want.done.Canceled), false},
		{"done error", errorString(actual.doneErr), errorString(want.doneErr), false},
		{"tick error", errorString(actual.tickErr), errorString(want.tickErr), false},
		{"add error", errorString(actual.addErr), errorString(want.addErr), false},
		{"have error", errorString(actual.haveErr), errorString(want.haveErr), false},
		{"drop error", errorString(actual.dropErr), errorString(want.dropErr), false},
		{"complete", actual.complete, want.complete, false},
	}
	for _, check := range checks {
		if check.deepCompare {
			if !reflect.DeepEqual(check.actual, check.want) {
				return check.name
			}
			continue
		}
		if check.actual != check.want {
			return check.name
		}
	}
	if len(actual.expired) != len(want.expired) {
		return "expired length"
	}
	for i := range actual.expired {
		if actual.expired[i] != ExpiredRequest(want.expired[i]) {
			return fmt.Sprintf("expired[%d]", i)
		}
	}
	return ""
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func stringSliceSignature(values []string) string {
	return fmt.Sprint(values)
}
