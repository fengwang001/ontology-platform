package intervaljoin

import (
	"io"
	"log"
	"os"
	"sort"
	"sync"
)

// Side 标识双流连接器中的一条流。
type Side int8

const (
	// SideLeft 为左侧流。
	SideLeft Side = 0
	// SideRight 为右侧流。
	SideRight Side = 1
)

// sideCount 为流侧总数。
const sideCount = 2

func (s Side) String() string {
	switch s {
	case SideLeft:
		return "left"
	case SideRight:
		return "right"
	default:
		return "invalid"
	}
}

// valid 判断侧标识是否合法。
func (s Side) valid() bool { return s == SideLeft || s == SideRight }

// sideOther 返回对侧。
func sideOther(s Side) Side {
	if s == SideLeft {
		return SideRight
	}
	return SideLeft
}

// Event 是一条到达事件：连接键 Key 与闭合事件时间区间 [Lo, Hi]。
type Event struct {
	Key string
	Lo  int64
	Hi  int64
}

// RetainedEvent 是当前仍被连接器保留的事件，带本侧单调编号。
type RetainedEvent struct {
	ID int64
	Event
}

// Pair 是一对成功匹配的左右事件快照。
type Pair struct {
	Key     string
	LeftID  int64
	RightID int64
	LeftLo  int64
	LeftHi  int64
	RightLo int64
	RightHi int64
}

// Config 为连接器参数。
type Config struct {
	// MaxRetainedPerSide 限制每一侧在处理一步之后允许保留的最大事件数；必须为正数。
	MaxRetainedPerSide int
}

// entry 是内部保留的事件。
type entry struct {
	id int64
	e  Event
}

// sideState 为单侧的水位线与按连接键分组的保留事件。
type sideState struct {
	wm    int64
	wmSet bool
	// 每个键下的事件按编号（到达顺序）升序排列。
	retained map[string][]entry
	nextID   int64
}

func newSideState() sideState {
	return sideState{retained: make(map[string][]entry)}
}

func (s *sideState) count() int {
	n := 0
	for _, es := range s.retained {
		n += len(es)
	}
	return n
}

// Connector 是线程安全的双流区间连接器。
//
// Process 串行化写入；Pairs、Retained、Watermark 使用读锁，
// 并发只读始终观察到逐字段一致的状态快照。
type Connector struct {
	mu     sync.RWMutex
	sides  [sideCount]sideState
	pairs  []Pair
	maxRet int
}

// 日志记录输入、输出配对与判定依据；默认输出到 stderr，可经 SetLogger 替换。
var (
	loggerMu sync.RWMutex
	logger   = log.New(os.Stderr, "[intervaljoin] ", log.LstdFlags|log.Lmicroseconds)
)

// SetLogger 替换内部日志输出；w 为 nil 时丢弃日志。
func SetLogger(w io.Writer) {
	loggerMu.Lock()
	defer loggerMu.Unlock()
	if w == nil {
		logger = log.New(io.Discard, "", 0)
	} else {
		logger = log.New(w, "[intervaljoin] ", log.LstdFlags|log.Lmicroseconds)
	}
}

func logf(format string, args ...any) {
	loggerMu.RLock()
	l := logger
	loggerMu.RUnlock()
	l.Printf(format, args...)
}

// rejectf 构造拒绝错误并打印一行 reject 日志（只输出，不触碰任何状态）。
func rejectf(code ErrorCode, side Side, format string, args ...any) *JoinError {
	je := joinErrorf(code, side, format, args...)
	logf("reject  side=%s code=%s detail=%q", side, code, je.Msg)
	return je
}

// New 创建连接器；非法配置返回带 CodeInvalidParameter 的错误。
func New(cfg Config) (*Connector, error) {
	if cfg.MaxRetainedPerSide <= 0 {
		return nil, rejectf(CodeInvalidParameter, -1,
			"MaxRetainedPerSide must be positive, got %d", cfg.MaxRetainedPerSide)
	}
	c := &Connector{maxRet: cfg.MaxRetainedPerSide}
	for i := range c.sides {
		c.sides[i] = newSideState()
	}
	return c, nil
}

// overlap 判断两个闭合区间是否相交：[aLo,aHi] ∩ [bLo,bHi] ≠ ∅，
// 即两端闭合条件 aLo <= bHi 且 bLo <= aHi。
func overlap(aLo, aHi, bLo, bHi int64) bool {
	return aLo <= bHi && bLo <= aHi
}

// wouldExpire 判断区间 [lo,hi] 在对侧水位线 wm 下是否已不可能再匹配。
// 对侧未来事件的 Lo 不会低于 wm；闭合区间下 hi < wm 即永无交集。
// hi == wm 仍可能与下一条 [wm, ...] 在端点上相交，必须保留。
func wouldExpire(hi, wm int64, wmSet bool) bool {
	return wmSet && hi < wm
}

// Process 处理一个到达事件：先更新本侧水位线，再与对侧当前保留事件按区间条件
// 两两配对，最后清理两侧已不可能再匹配的事件。返回这一步新产生的配对，
// 按对侧事件编号升序。
//
// 任何校验失败都会整体拒绝该操作，水位线、编号、保留状态与已输出配对均不变。
func (c *Connector) Process(side Side, e *Event) ([]Pair, error) {
	// —— 纯参数校验，不触碰状态 ——
	if !side.valid() {
		return nil, rejectf(CodeInvalidParameter, side, "invalid side %d", int(side))
	}
	if e == nil {
		return nil, rejectf(CodeInvalidParameter, side, "event is nil")
	}
	if e.Lo > e.Hi {
		return nil, rejectf(CodeInvalidParameter, side,
			"event interval invalid: Lo %d > Hi %d", e.Lo, e.Hi)
	}
	if e.Key == "" {
		return nil, rejectf(CodeEmptyKey, side, "event key is empty")
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	self := &c.sides[side]
	other := &c.sides[sideOther(side)]

	// 单侧时间非递减：事件 Lo 不得小于本侧已有水位线。
	if self.wmSet && e.Lo < self.wm {
		return nil, rejectf(CodeTimeRegression, side,
			"event Lo %d regresses below %s watermark %d", e.Lo, side, self.wm)
	}

	// 预演这一步提交、清理之后两侧各自的保留数量；超限则整体拒绝、不落任何状态。
	// 清理规则与 cleanup 完全一致：
	//   - 本侧事件按对侧“旧”水位线判定（本步对侧水位线不变）；
	//   - 对侧事件按本侧“新”水位线 newWM 判定。
	newWM := e.Lo
	id := self.nextID + 1
	selfAfter := 0
	for _, es := range self.retained {
		for _, en := range es {
			if !wouldExpire(en.e.Hi, other.wm, other.wmSet) {
				selfAfter++
			}
		}
	}
	if !wouldExpire(e.Hi, other.wm, other.wmSet) {
		selfAfter++ // 新事件本身在清理后仍保留
	}
	otherAfter := 0
	for _, es := range other.retained {
		for _, en := range es {
			if !wouldExpire(en.e.Hi, newWM, true) {
				otherAfter++
			}
		}
	}
	if selfAfter > c.maxRet {
		return nil, rejectf(CodeRetentionLimitExceeded, side,
			"%s retained %d would exceed limit %d after event id=%d key=%q",
			side, selfAfter, c.maxRet, id, e.Key)
	}
	if otherAfter > c.maxRet {
		return nil, rejectf(CodeRetentionLimitExceeded, sideOther(side),
			"%s retained %d would exceed limit %d after %s event id=%d key=%q",
			sideOther(side), otherAfter, c.maxRet, side, id, e.Key)
	}

	// —— 校验通过，正式提交 —— //
	prevVal, prevSet := self.wm, self.wmSet
	self.wm = newWM
	self.wmSet = true
	self.nextID = id
	ne := entry{id: id, e: *e}

	if prevSet {
		logf("input  side=%s id=%d key=%q interval=[%d,%d] watermark=%d->%d",
			side, id, e.Key, e.Lo, e.Hi, prevVal, newWM)
	} else {
		logf("input  side=%s id=%d key=%q interval=[%d,%d] watermark=<none>->%d (initialized)",
			side, id, e.Key, e.Lo, e.Hi, newWM)
	}

	// 1) 与对侧同键保留事件配对（切片按编号升序，输出天然按对侧编号升序）。
	step := make([]Pair, 0)
	for _, o := range other.retained[e.Key] {
		hit := overlap(e.Lo, e.Hi, o.e.Lo, o.e.Hi)
		logf("match? %s id=%d [%d,%d] vs %s id=%d [%d,%d] key=%q => %v "+
			"(closed-interval: Lo<=otherHi is %d<=%d=%t; otherLo<=Hi is %d<=%d=%t)",
			side, id, e.Lo, e.Hi, sideOther(side), o.id, o.e.Lo, o.e.Hi, e.Key, hit,
			e.Lo, o.e.Hi, e.Lo <= o.e.Hi, o.e.Lo, e.Hi, o.e.Lo <= e.Hi)
		if hit {
			p := makePair(side, ne, o)
			step = append(step, p)
			c.pairs = append(c.pairs, p)
		}
	}

	// 2) 新事件入保留集合，随后统一清理（对外部只呈现清理后的结果）。
	self.retained[e.Key] = append(self.retained[e.Key], ne)

	// 3) 用更新后的双侧水位线清理两侧：按侧、按键、按编号给出判定依据。
	c.cleanup(SideLeft)
	c.cleanup(SideRight)

	logf("output side=%s id=%d pairs=%d retained(left=%d,right=%d)",
		side, id, len(step), c.sides[SideLeft].count(), c.sides[SideRight].count())

	out := make([]Pair, len(step))
	copy(out, step)
	return out, nil
}

// makePair 按“左/右”固定位置构造配对，与到达侧无关。
func makePair(arrivingSide Side, a, b entry) Pair {
	l, r := a, b
	if arrivingSide == SideRight {
		l, r = b, a
	}
	return Pair{
		Key:     l.e.Key,
		LeftID:  l.id,
		RightID: r.id,
		LeftLo:  l.e.Lo,
		LeftHi:  l.e.Hi,
		RightLo: r.e.Lo,
		RightHi: r.e.Hi,
	}
}

// cleanup 依据对侧当前水位线删除该侧已不可能再匹配的事件，
// 并逐项输出判定依据；键集合按字典序遍历以保证日志可复现。
func (c *Connector) cleanup(s Side) {
	st := &c.sides[s]
	opp := &c.sides[sideOther(s)]
	if !opp.wmSet {
		return
	}
	keys := make([]string, 0, len(st.retained))
	for k := range st.retained {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		es := st.retained[k]
		kept := es[:0]
		for _, en := range es {
			if en.e.Hi < opp.wm {
				logf("cleanup  side=%s id=%d key=%q interval=[%d,%d] removed: Hi %d < opposite(%s) watermark %d",
					s, en.id, k, en.e.Lo, en.e.Hi, en.e.Hi, sideOther(s), opp.wm)
				continue
			}
			logf("cleanup  side=%s id=%d key=%q interval=[%d,%d] kept: Hi %d >= opposite(%s) watermark %d",
				s, en.id, k, en.e.Lo, en.e.Hi, en.e.Hi, sideOther(s), opp.wm)
			kept = append(kept, en)
		}
		if len(kept) == 0 {
			delete(st.retained, k)
		} else {
			st.retained[k] = kept
		}
	}
}

// Pairs 返回截至当前全部已输出配对的深拷贝（按 LeftID、RightID 升序）。
func (c *Connector) Pairs() []Pair {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Pair, len(c.pairs))
	copy(out, c.pairs)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].LeftID != out[j].LeftID {
			return out[i].LeftID < out[j].LeftID
		}
		return out[i].RightID < out[j].RightID
	})
	return out
}

// Retained 返回指定侧当前保留事件的深拷贝（跨键按编号升序）；非法侧返回空切片。
func (c *Connector) Retained(side Side) []RetainedEvent {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !side.valid() {
		return []RetainedEvent{}
	}
	st := &c.sides[side]
	out := make([]RetainedEvent, 0, st.count())
	for _, es := range st.retained {
		for _, en := range es {
			out = append(out, RetainedEvent{ID: en.id, Event: en.e})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Watermark 返回指定侧当前水位线；尚未有事件到达或侧非法时 ok 为 false。
func (c *Connector) Watermark(side Side) (wm int64, ok bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if !side.valid() {
		return 0, false
	}
	st := &c.sides[side]
	return st.wm, st.wmSet
}
