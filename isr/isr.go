package isr

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Replica 描述单个副本的进度与最近一次追平时间。
type Replica struct {
	LEO          int64
	LastCaughtUp int64
}

// Snapshot 是一次查询得到的副本集状态。
type Snapshot struct {
	HighWatermark int64
	ISR           []string
	Progress      map[string]Replica
}

type replicaState struct {
	leo          int64
	lastCaughtUp int64
}

// ReplicaSet 协调领导者写入、跟随者拉取、ISR 维护与高水位推进。
// 所有方法均可被多个执行体并发调用；任何被拒绝的调用都不会留下状态痕迹。
type ReplicaSet struct {
	mu        sync.Mutex
	leaderID  string
	progress  map[string]*replicaState
	isr       map[string]struct{}
	hw        int64
	maxSeenTs int64
	logger    *log.Logger
}

// NewReplicaSet 创建仅包含领导者（初始 LEO 0、初始高水位 0）的副本集。
func NewReplicaSet(leaderID string, logger *log.Logger) (*ReplicaSet, error) {
	if strings.TrimSpace(leaderID) == "" {
		return nil, ErrInvalidArgument
	}
	if logger == nil {
		logger = log.Default()
	}
	rs := &ReplicaSet{
		leaderID: leaderID,
		progress: map[string]*replicaState{
			leaderID: {leo: 0, lastCaughtUp: 0},
		},
		isr:    map[string]struct{}{leaderID: {}},
		hw:     0,
		logger: logger,
	}
	rs.logf("NewReplicaSet", "leader=%q", leaderID)
	return rs, nil
}

// AddReplica 注册一个新跟随者（初始 LEO 0）。
// 高水位为 0 时直接进入同步副本集；否则留在集外，待其拉取追平高水位后重新加入。
func (rs *ReplicaSet) AddReplica(id string) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if strings.TrimSpace(id) == "" || id == rs.leaderID {
		return rs.reject("AddReplica", ErrInvalidArgument, "id=%q", id)
	}
	if _, ok := rs.progress[id]; ok {
		return rs.reject("AddReplica", ErrInvalidArgument, "id=%q already-registered", id)
	}

	rs.progress[id] = &replicaState{}
	inISR := false
	if rs.hw == 0 {
		rs.isr[id] = struct{}{}
		inISR = true
	}
	rs.logf("AddReplica", "id=%q in-isr=%t basis=%s", id, inISR,
		caughtUpBasis(0, rs.hw))
	return nil
}

// Append 由领导者写入并把领导者日志结束位点推进到 newLEO（必须严格大于当前 LEO）。
func (rs *ReplicaSet) Append(newLEO, now int64) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if err := rs.checkTime("Append", now); err != nil {
		return err
	}
	leader := rs.progress[rs.leaderID]
	if newLEO <= leader.leo {
		return rs.reject("Append", ErrInvalidOffset,
			"newLEO=%d leader-LEO=%d", newLEO, leader.leo)
	}

	oldHW := rs.hw
	leader.leo = newLEO
	rs.maxSeenTs = now
	rs.advanceHW("Append")
	rs.logf("Append", "newLEO=%d now=%d hw:%d->%d basis=min-LEO-over-ISR-including-leader",
		newLEO, now, oldHW, rs.hw)
	return nil
}

// Fetch 由跟随者上报其已拥有位点 offset（不得小于自身进度、不得超过领导者 LEO）。
// offset 追平当前高水位时刷新追上时间；被移出 ISR 的跟随者因此重新加入。
func (rs *ReplicaSet) Fetch(replicaID string, offset, now int64) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if strings.TrimSpace(replicaID) == "" {
		return rs.reject("Fetch", ErrInvalidArgument, "empty replica id")
	}
	if replicaID == rs.leaderID {
		return rs.reject("Fetch", ErrInvalidArgument, "replica=%q is leader", replicaID)
	}
	if err := rs.checkTime("Fetch", now); err != nil {
		return err
	}
	r, ok := rs.progress[replicaID]
	if !ok {
		return rs.reject("Fetch", ErrUnknownReplica, "replica=%q", replicaID)
	}
	leaderLEO := rs.progress[rs.leaderID].leo
	if offset < 0 || offset < r.leo {
		return rs.reject("Fetch", ErrInvalidOffset,
			"replica=%q offset=%d own-LEO=%d (offset regresses or negative)",
			replicaID, offset, r.leo)
	}
	if offset > leaderLEO {
		return rs.reject("Fetch", ErrInvalidOffset,
			"replica=%q offset=%d leader-LEO=%d (offset beyond leader)",
			replicaID, offset, leaderLEO)
	}

	oldHW := rs.hw
	_, wasInISR := rs.isr[replicaID]
	r.leo = offset
	caughtUp := offset >= rs.hw
	rejoined := false
	if caughtUp {
		r.lastCaughtUp = now
		if !wasInISR {
			rs.isr[replicaID] = struct{}{}
			rejoined = true
		}
	}
	rs.maxSeenTs = now
	rs.advanceHW("Fetch")
	rs.logf("Fetch",
		"replica=%q offset=%d now=%d caught-up=%t was-in-isr=%t rejoined=%t hw:%d->%d basis=%s",
		replicaID, offset, now, caughtUp, wasInISR, rejoined, oldHW, rs.hw,
		caughtUpBasis(offset, oldHW))
	return nil
}

// PeriodicCheck 周期性剔除落后副本：
// 当 now-lastCaughtUp 严格大于 tolerance 时，将该跟随者移出同步副本集。
// 领导者永不移出。剔除后高水位只可能保持或推进，绝不回退。
func (rs *ReplicaSet) PeriodicCheck(now, tolerance int64) error {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if tolerance < 0 {
		return rs.reject("PeriodicCheck", ErrInvalidArgument, "tolerance=%d negative", tolerance)
	}
	if err := rs.checkTime("PeriodicCheck", now); err != nil {
		return err
	}

	oldHW := rs.hw
	var removed []string
	for id := range rs.isr {
		if id == rs.leaderID {
			continue
		}
		lag := now - rs.progress[id].lastCaughtUp
		if lag > tolerance {
			delete(rs.isr, id)
			removed = append(removed, id)
		}
	}
	sort.Strings(removed)
	rs.maxSeenTs = now
	rs.advanceHW("PeriodicCheck")
	rs.logf("PeriodicCheck",
		"now=%d tolerance=%d removed=%v hw:%d->%d basis=remove-iff (now-lastCaughtUp)>tolerance; strict-greater-than",
		now, tolerance, removed, oldHW, rs.hw)
	return nil
}

// Query 返回当前高水位、同步副本集与各副本进度的深拷贝快照。
func (rs *ReplicaSet) Query(now int64) (Snapshot, error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()

	if err := rs.checkTime("Query", now); err != nil {
		return Snapshot{}, err
	}
	rs.maxSeenTs = now

	snap := Snapshot{
		HighWatermark: rs.hw,
		ISR:           make([]string, 0, len(rs.isr)),
		Progress:      make(map[string]Replica, len(rs.progress)),
	}
	for id := range rs.isr {
		snap.ISR = append(snap.ISR, id)
	}
	sort.Strings(snap.ISR)
	for id, r := range rs.progress {
		snap.Progress[id] = Replica{LEO: r.leo, LastCaughtUp: r.lastCaughtUp}
	}
	rs.logf("Query", "now=%d hw=%d basis=read-only-snapshot", now, rs.hw)
	return snap, nil
}

// advanceHW 取同步副本集（含领导者）全部 LEO 的最小值，并保证高水位只进不退。
func (rs *ReplicaSet) advanceHW(op string) {
	minLEO := rs.progress[rs.leaderID].leo
	for id := range rs.isr {
		if leo := rs.progress[id].leo; leo < minLEO {
			minLEO = leo
		}
	}
	if minLEO > rs.hw {
		rs.hw = minLEO
	}
}

func (rs *ReplicaSet) checkTime(op string, now int64) error {
	if now < 0 {
		return rs.reject(op, ErrInvalidArgument, "now=%d negative", now)
	}
	if now < rs.maxSeenTs {
		return rs.reject(op, ErrClockWentBack, "now=%d max-seen=%d", now, rs.maxSeenTs)
	}
	return nil
}

func caughtUpBasis(offset, hw int64) string {
	return "caught-up-iff offset>=hw (" +
		"offset=" + itoa(offset) + ",hw=" + itoa(hw) + ")"
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func (rs *ReplicaSet) isrList() []string {
	ids := make([]string, 0, len(rs.isr))
	for id := range rs.isr {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (rs *ReplicaSet) logf(op, format string, args ...any) {
	detail := fmt.Sprintf(format, args...)
	rs.logger.Printf("isr[%s] hw=%d isr=%v | %s", op, rs.hw, rs.isrList(), detail)
}

func (rs *ReplicaSet) reject(op string, err *Error, format string, args ...any) error {
	rs.logger.Printf("isr[%s] hw=%d isr=%v | REJECTED reason=%q detail=%s",
		op, rs.hw, rs.isrList(), err.Reason, fmt.Sprintf(format, args...))
	return err
}
