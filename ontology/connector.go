package ontology

import (
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"sort"
	"sync"
)

// Option 在构造时调整连接器的可选项。
type Option func(*Connector)

// WithLogWriter 将连接器日志（输入、判定依据、输出配对、清理、拒绝原因）
// 重定向到 w；默认写入 os.Stderr。
func WithLogWriter(w io.Writer) Option {
	return func(c *Connector) {
		c.log = slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
}

// retainedEvent 是连接器内部保留的事件记录。
type retainedEvent struct {
	seq  int64
	key  string
	time int64
}

func (e retainedEvent) toEvent(side Side) Event {
	return Event{Seq: e.seq, Side: side, Key: e.key, Time: e.time}
}

// Connector 是并发安全的双流区间连接器。
//
// 配对条件（两端闭合）：L+LowerBound <= R <= L+UpperBound，其中 L 为左事件
// 时间、R 为右事件时间。两侧各自要求事件时间非递减到达；每处理一条事件，
// 顺序为：校验 -> 推进本侧水位线 -> 与对侧保留事件匹配 -> 清理两侧 ->
// 保留容量检查，任一步拒绝则整体状态不变。
type Connector struct {
	mu  sync.RWMutex
	cfg Config
	log *slog.Logger

	seq     int64 // 已提交事件的全局最大编号
	leftWM  int64 // 左侧水位线；math.MinInt64 表示尚无事件
	rightWM int64

	// 同一 key 下的事件按到达顺序（seq 升、时间非递减）保存。
	left  map[string][]*retainedEvent
	right map[string][]*retainedEvent

	emitted []Pair // 全部已输出配对，按提交顺序
}

// NewConnector 按配置创建连接器。LowerBound>UpperBound 或 MaxRetained<=0
// 时返回包装了 ErrInvalidArgument 的错误。
func NewConnector(cfg Config, opts ...Option) (*Connector, error) {
	if cfg.LowerBound > cfg.UpperBound {
		return nil, fmt.Errorf("%w: lower bound %d is greater than upper bound %d",
			ErrInvalidArgument, cfg.LowerBound, cfg.UpperBound)
	}
	if cfg.MaxRetained <= 0 {
		return nil, fmt.Errorf("%w: max retained %d must be positive",
			ErrInvalidArgument, cfg.MaxRetained)
	}
	c := &Connector{
		cfg:     cfg,
		log:     slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
		leftWM:  math.MinInt64,
		rightWM: math.MinInt64,
		left:    make(map[string][]*retainedEvent),
		right:   make(map[string][]*retainedEvent),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// ProcessLeft 接收一条左流事件，返回本次新输出的配对。
func (c *Connector) ProcessLeft(key string, eventTime int64) ([]Pair, error) {
	return c.process(SideLeft, key, eventTime)
}

// ProcessRight 接收一条右流事件，返回本次新输出的配对。
func (c *Connector) ProcessRight(key string, eventTime int64) ([]Pair, error) {
	return c.process(SideRight, key, eventTime)
}

func (c *Connector) process(side Side, key string, t int64) ([]Pair, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// 1) 输入校验。任何拒绝都发生在状态修改之前。
	if key == "" {
		c.log.Warn("input rejected", "side", side, "time", t, "reason", ErrEmptyKey)
		return nil, ErrEmptyKey
	}
	curWM := c.leftWM
	if side == SideRight {
		curWM = c.rightWM
	}
	if t < curWM {
		c.log.Warn("input rejected", "side", side, "key", key, "time", t,
			"watermark", curWM, "reason", ErrTimeRegressed)
		return nil, fmt.Errorf("%w: side %s event time %d < watermark %d",
			ErrTimeRegressed, side, t, curWM)
	}

	newLeftWM, newRightWM := c.leftWM, c.rightWM
	if side == SideLeft {
		newLeftWM = t
	} else {
		newRightWM = t
	}

	// 2) 干跑：与对侧当前保留事件匹配（先于任何状态修改）。
	newSeq := c.seq + 1
	in := &retainedEvent{seq: newSeq, key: key, time: t}
	var pairs []Pair

	opponent := c.right
	if side == SideRight {
		opponent = c.left
	}
	for _, o := range opponent[key] {
		L, R := t, o.time
		if side == SideRight {
			L, R = o.time, t
		}
		// 两端闭合：L+lower <= R <= L+upper。
		lowerOK := sumCmp(L, c.cfg.LowerBound, R) <= 0
		upperOK := sumCmp(L, c.cfg.UpperBound, R) >= 0
		matched := lowerOK && upperOK
		c.log.Info("match check",
			"incoming_seq", newSeq, "incoming_side", side, "key", key,
			"candidate_seq", o.seq, "left_time", L, "right_time", R,
			"lower_bound", c.cfg.LowerBound, "upper_bound", c.cfg.UpperBound,
			"lower_closed_ok", lowerOK, "upper_closed_ok", upperOK, "matched", matched)
		if !matched {
			continue
		}
		var le, re Event
		if side == SideLeft {
			le, re = in.toEvent(SideLeft), o.toEvent(SideRight)
		} else {
			le, re = o.toEvent(SideLeft), in.toEvent(SideRight)
		}
		pairs = append(pairs, Pair{
			Key: key, Left: le, Right: re, LeftTime: L, RightTime: R,
		})
	}
	c.log.Info("input accepted", "side", side, "seq", newSeq, "key", key, "time", t,
		"new_left_watermark", newLeftWM, "new_right_watermark", newRightWM,
		"emitted_pairs", len(pairs))

	// 3) 干跑：按新双水位线统计清理后的保留总数。
	aliveLeft := func(L int64) bool {
		// 未来右事件 R' >= wmR；wmR > L+upper 时左事件再无匹配可能。
		return sumCmp(L, c.cfg.UpperBound, newRightWM) >= 0
	}
	aliveRight := func(R int64) bool {
		// 未来左事件 L' >= wmL；wmL+lower > R 时右事件再无匹配可能。
		return sumCmp(newLeftWM, c.cfg.LowerBound, R) <= 0
	}
	retained := 0
	for _, es := range c.left {
		for _, e := range es {
			if aliveLeft(e.time) {
				retained++
			}
		}
	}
	for _, es := range c.right {
		for _, e := range es {
			if aliveRight(e.time) {
				retained++
			}
		}
	}
	if side == SideLeft {
		if aliveLeft(t) {
			retained++
		}
	} else {
		if aliveRight(t) {
			retained++
		}
	}
	if retained > c.cfg.MaxRetained {
		c.log.Warn("input rejected", "side", side, "key", key, "time", t,
			"retained_after_cleanup", retained, "limit", c.cfg.MaxRetained,
			"reason", ErrRetentionExceeded)
		return nil, fmt.Errorf("%w: %d events retained after cleanup, limit %d",
			ErrRetentionExceeded, retained, c.cfg.MaxRetained)
	}

	// 4) 提交：水位线、编号、保留状态、输出配对一次性生效。
	if side == SideLeft {
		c.leftWM = t
		c.left[key] = append(c.left[key], in)
	} else {
		c.rightWM = t
		c.right[key] = append(c.right[key], in)
	}
	c.seq = newSeq
	c.emitted = append(c.emitted, pairs...)

	c.compact(c.left, aliveLeft, SideLeft)
	c.compact(c.right, aliveRight, SideRight)

	for i := range pairs {
		c.log.Info("pair emitted", "key", pairs[i].Key,
			"left_seq", pairs[i].Left.Seq, "left_time", pairs[i].LeftTime,
			"right_seq", pairs[i].Right.Seq, "right_time", pairs[i].RightTime)
	}
	return append([]Pair(nil), pairs...), nil
}

// compact 原地删除已不可能再匹配的事件，并逐条记录清理依据。
func (c *Connector) compact(
	m map[string][]*retainedEvent,
	alive func(int64) bool,
	side Side,
) {
	for k, es := range m {
		kept := es[:0]
		for _, e := range es {
			if alive(e.time) {
				kept = append(kept, e)
				continue
			}
			c.log.Info("event cleaned", "side", side, "seq", e.seq, "key", e.key,
				"time", e.time, "left_watermark", c.leftWM,
				"right_watermark", c.rightWM,
				"lower_bound", c.cfg.LowerBound, "upper_bound", c.cfg.UpperBound)
		}
		if len(kept) == 0 {
			delete(m, k)
		} else {
			m[k] = kept
		}
	}
}

// Snapshot 是某一时刻连接器状态的确定性只读副本。
type Snapshot struct {
	Seq            int64   // 已分配的最大事件编号
	LeftWatermark  int64   // 左侧水位线（无事件时为 math.MinInt64）
	RightWatermark int64   // 右侧水位线（无事件时为 math.MinInt64）
	RetainedLeft   []Event // 当前保留的左事件，按 (key,time,seq) 排序
	RetainedRight  []Event // 当前保留的右事件，按 (key,time,seq) 排序
	Pairs          []Pair  // 截至目前全部已输出配对，按输出顺序
}

// Snapshot 返回当前状态副本；可与处理操作及其他只读调用并发使用。
func (c *Connector) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()

	snap := Snapshot{
		Seq:            c.seq,
		LeftWatermark:  c.leftWM,
		RightWatermark: c.rightWM,
		RetainedLeft:   retainedEvents(c.left, SideLeft),
		RetainedRight:  retainedEvents(c.right, SideRight),
		Pairs:          append([]Pair(nil), c.emitted...),
	}
	return snap
}

// Pairs 返回截至目前全部已输出配对的副本，按输出顺序排列。
func (c *Connector) Pairs() []Pair {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]Pair(nil), c.emitted...)
}

func retainedEvents(m map[string][]*retainedEvent, side Side) []Event {
	if len(m) == 0 {
		return []Event{}
	}
	var out []Event
	for _, es := range m {
		for _, e := range es {
			out = append(out, e.toEvent(side))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		if out[i].Time != out[j].Time {
			return out[i].Time < out[j].Time
		}
		return out[i].Seq < out[j].Seq
	})
	return out
}

// sumCmp 在整数 ℤ 上比较 (x+y) 与 z 的大小，返回 -1/0/1，全程不发生 int64
// 溢出：x、y 同号且真实和超出 int64 表示范围时，和按 +∞/-∞ 处理。
func sumCmp(x, y, z int64) int {
	s := x + y
	switch {
	case x > 0 && y > 0 && s <= 0:
		return 1 // 真实 x+y 为 +∞，必然大于 z
	case x < 0 && y < 0 && s >= 0:
		return -1 // 真实 x+y 为 -∞，必然小于 z
	case s < z:
		return -1
	case s > z:
		return 1
	default:
		return 0
	}
}
