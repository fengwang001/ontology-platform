package timetable

import "sync"

const (
	maxTime   = int64(1_000_000_000)
	maxSegs   = 32
	maxRecord = 64
)

// record 是一条通告记录。被替换时 replacedAt 记录替换发生时的版本，
// 记录本身不删除，使历史版本查询仍可看到它。
type record struct {
	eff        int64
	segments   []Segment
	addedAt    int
	replacedAt int // 0 表示未被替换；版本号从 1 起，故 0 可作哨兵
}

// edge 是一条有向边及其追加式的通告记录。
type edge struct {
	id      int
	u, v    int
	addedAt int
	records []*record
	live    int // 未被替换的记录数（被同 eff 替换掉的不占名额）
}

// Network 是并发安全的时间依赖路网。
type Network struct {
	mu      sync.RWMutex
	n       int
	maxE    int
	now     int64
	version int
	edges   []*edge // 下标为 id-1
}

// New 创建节点数为 N、边数上限为 E 的空路网，时钟与版本均为 0。
func New(N, E int) (*Network, error) {
	if N < 1 || N > 5000 {
		return nil, errorf(ErrInvalidArgument, "timetable: N=%d out of range [1,5000]", N)
	}
	if E < 1 || E > 100000 {
		return nil, errorf(ErrInvalidArgument, "timetable: E=%d out of range [1,100000]", E)
	}
	return &Network{n: N, maxE: E, edges: make([]*edge, 0, E)}, nil
}

// N 返回节点数。
func (nw *Network) N() int { return nw.n }

// Now 返回当前系统时钟。
func (nw *Network) Now() int64 {
	nw.mu.RLock()
	defer nw.mu.RUnlock()
	return nw.now
}

// Version 返回当前版本号（每次被接受的 AddEdge 与 Announce 加一）。
func (nw *Network) Version() int {
	nw.mu.RLock()
	defer nw.mu.RUnlock()
	return nw.version
}

// EdgeCount 返回已分配的边数（被拒绝的加边不占编号）。
func (nw *Network) EdgeCount() int {
	nw.mu.RLock()
	defer nw.mu.RUnlock()
	return len(nw.edges)
}

func validateProfile(p []Segment) error {
	if len(p) < 1 || len(p) > maxSegs {
		return errorf(ErrInvalidArgument, "timetable: profile has %d segments, want 1..32", len(p))
	}
	if p[0].Offset != 0 {
		return errorf(ErrInvalidArgument, "timetable: first profile offset must be 0, got %d", p[0].Offset)
	}
	for i, s := range p {
		if s.Offset < 0 || s.Offset > maxTime {
			return errorf(ErrInvalidArgument, "timetable: segment %d offset %d out of range [0,1e9]", i, s.Offset)
		}
		if i > 0 && s.Offset <= p[i-1].Offset {
			return errorf(ErrInvalidArgument, "timetable: segment offsets must be strictly increasing at %d", i)
		}
		if s.Cost != -1 && (s.Cost < 1 || s.Cost > 1_000_000) {
			return errorf(ErrInvalidArgument, "timetable: segment %d cost %d must be -1 or in [1,1e6]", i, s.Cost)
		}
	}
	return nil
}

func cloneProfile(p []Segment) []Segment {
	q := make([]Segment, len(p))
	copy(q, p)
	return q
}

func (nw *Network) validNode(x int) bool { return x >= 0 && x < nw.n }

// AddError 仅用于让骨架编译通过，后续文件不再使用。
var _ = maxRecord

// AddEdge 追加一条 u->v 的边，登记一条 eff=0、版本为当前版本+1 的记录。
// 成功时返回从 1 起严格递增的边编号。
func (nw *Network) AddEdge(u, v int, profile []Segment) (int, error) {
	if !nw.validNode(u) || !nw.validNode(v) || u == v {
		return 0, errorf(ErrInvalidArgument, "timetable: invalid endpoints u=%d v=%d", u, v)
	}
	if err := validateProfile(profile); err != nil {
		return 0, err
	}
	nw.mu.Lock()
	defer nw.mu.Unlock()
	if len(nw.edges) >= nw.maxE {
		return 0, errorf(ErrEdgeLimit, "timetable: edge limit %d reached", nw.maxE)
	}
	nw.version++
	id := len(nw.edges) + 1
	nw.edges = append(nw.edges, &edge{
		id:      id,
		u:       u,
		v:       v,
		addedAt: nw.version,
		records: []*record{{eff: 0, segments: cloneProfile(profile), addedAt: nw.version}},
		live:    1,
	})
	return id, nil
}

// Announce 为已存在的边追加或替换一条通告记录。
// eff==最后一条记录的 eff 时替换（不增加记录条数）；eff 更大时追加。
func (nw *Network) Announce(edgeID int, eff int64, profile []Segment) error {
	if edgeID < 1 || eff < 0 || eff > maxTime {
		return errorf(ErrInvalidArgument, "timetable: invalid announcement edge=%d eff=%d", edgeID, eff)
	}
	if err := validateProfile(profile); err != nil {
		return err
	}
	nw.mu.Lock()
	defer nw.mu.Unlock()
	if edgeID > len(nw.edges) {
		return errorf(ErrNoSuchEdge, "timetable: edge %d not allocated", edgeID)
	}
	e := nw.edges[edgeID-1]
	if eff < nw.now {
		return errorf(ErrRetroactive, "timetable: eff %d before now %d", eff, nw.now)
	}
	last := e.records[len(e.records)-1]
	if eff < last.eff {
		return errorf(ErrOutOfOrder, "timetable: eff %d before last record eff %d", eff, last.eff)
	}
	if eff > last.eff && e.live >= maxRecord {
		return errorf(ErrRecordLimit, "timetable: edge %d already has %d live records", edgeID, e.live)
	}
	nw.version++
	rec := &record{eff: eff, segments: cloneProfile(profile), addedAt: nw.version}
	if eff == last.eff {
		last.replacedAt = nw.version
	} else {
		e.live++
	}
	e.records = append(e.records, rec)
	return nil
}

// Advance 把系统时钟单调推进到 t。
func (nw *Network) Advance(t int64) error {
	if t < 0 || t > maxTime {
		return errorf(ErrInvalidArgument, "timetable: advance target %d out of range [0,1e9]", t)
	}
	nw.mu.Lock()
	defer nw.mu.Unlock()
	if t < nw.now {
		return errorf(ErrClockRollback, "timetable: cannot roll back from %d to %d", nw.now, t)
	}
	nw.now = t
	return nil
}

// newUnlimited 仅供包内规模测试：放宽 N 的公开上限，其余规则不变。
func newUnlimited(N, E int) *Network {
	return &Network{n: N, maxE: E, edges: make([]*edge, 0, E)}
}
