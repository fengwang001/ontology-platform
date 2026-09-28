package temporal

import (
	"io"
	"log"
	"math"
	"sort"
	"strconv"
	"sync"
)

// Joiner 是流与版本表的时态连接组件。
//
// 语义概要（详见包文档与 README）：
//   - 每个键的版本按生效起点升序保存，区间左闭右开，最后一个版本到 +Inf；
//     墓碑表示该键在区间内无值。
//   - 事件取“生效起点不大于其事件时间且起点最大”的版本；无此版本或命中
//     墓碑时输出未命中。
//   - 版本变更仅当生效起点严格大于当前水位线时接受，否则判为迟到并拒绝；
//     同一生效起点的变更覆盖既有记录。
//   - 事件时间不大于当前水位线的事件立即确定，否则缓冲；推进水位线会使
//     所有事件时间不超过新水位线的缓冲事件最终确定。
//
// 所有方法均可被并发调用。
type Joiner struct {
	mu sync.Mutex

	// log 记录每次输入、连接结果与判定依据；为 nil 时不输出日志。
	log *log.Logger

	// bufferLimit > 0 时为缓冲事件总数上限；<= 0 表示不限。
	bufferLimit int

	// watermark 为当前水位线；初始为 math.MinInt64，Drain 后为 math.MaxInt64。
	watermark int64

	// versions[k] 为键 k 的版本序列，按 EffectiveAt 严格升序；
	// 同一起点只保留最后一次提交（覆盖语义）。
	versions map[string][]Version

	// buffered 为尚未最终确定的事件，按接受顺序排列。
	buffered []pending

	// emitted 保存全部已确定输出，按确定（输出）先后排列；
	// Result.Seq 记录事件的接受顺序，可用于重排。
	emitted []Result

	// seqCounter 为已接受事件计数，用于分配接受序号。
	seqCounter int
}

// pending 是一个等待水位线推进的缓冲事件。
type pending struct {
	seq int
	e   Event
}

// NewJoiner 创建一个连接组件。bufferLimit 为允许缓冲的事件总数上限；
// bufferLimit <= 0 表示不限制。日志写入标准 logger。
func NewJoiner(bufferLimit int) *Joiner {
	return NewJoinerWithLogger(bufferLimit, log.Default())
}

// NewJoinerWithLogger 与 NewJoiner 相同，但日志写入指定 logger；
// logger 为 nil（或输出目标为 io.Discard）时等价于关闭日志。
func NewJoinerWithLogger(bufferLimit int, logger *log.Logger) *Joiner {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Joiner{
		log:         logger,
		bufferLimit: bufferLimit,
		watermark:   math.MinInt64,
		versions:    make(map[string][]Version),
	}
}

// ApplyVersion 提交一条版本变更。
//
// 接受条件（任一不满足即拒绝并返回对应错误，状态不变）：
//  1. 键非空（否则 ErrEmptyKey）；
//  2. 生效起点严格大于当前水位线（否则 ErrLateVersion）。
//
// 同一生效起点的变更覆盖旧记录（值覆盖墓碑、墓碑覆盖值均允许）。
func (j *Joiner) ApplyVersion(v Version) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	if v.Key == "" {
		j.log.Printf("reject version %s: empty key", formatVersion(v))
		return ErrEmptyKey
	}
	if v.EffectiveAt <= j.watermark {
		j.log.Printf("reject version %s: late (effectiveAt=%d <= watermark=%d)",
			formatVersion(v), v.EffectiveAt, j.watermark)
		return ErrLateVersion
	}

	vs := j.versions[v.Key]
	idx := sort.Search(len(vs), func(i int) bool { return vs[i].EffectiveAt >= v.EffectiveAt })
	if idx < len(vs) && vs[idx].EffectiveAt == v.EffectiveAt {
		vs[idx] = v // 同一起点：覆盖
	} else {
		vs = append(vs, Version{})
		copy(vs[idx+1:], vs[idx:])
		vs[idx] = v
	}
	j.versions[v.Key] = vs

	j.log.Printf("accept version %s: watermark=%d, %d version(s) for key",
		formatVersion(v), j.watermark, len(vs))
	return nil
}

// ProcessEvent 提交一条事件。
//
// 空键返回 ErrEmptyKey；事件需要缓冲但缓冲已达上限时返回
// ErrBufferLimitExceeded。被拒绝时不改变任何状态、不占用接受序号。
//
// 被接受的事件：
//   - 事件时间不大于当前水位线：立即确定，返回 StatusResolved 与非空 Result；
//   - 否则进入缓冲，返回 StatusBuffered 与 nil Result，待后续
//     AdvanceWatermark/Drain 时恰好输出一次。
func (j *Joiner) ProcessEvent(e Event) (Status, *Result, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if e.Key == "" {
		j.log.Printf("reject event %s: empty key", formatEvent(e))
		return StatusResolved, nil, ErrEmptyKey
	}

	// 需要缓冲的事件先做容量判定，拒绝时不得留下任何痕迹。
	if e.EventTime > j.watermark && j.bufferLimit > 0 && len(j.buffered) >= j.bufferLimit {
		j.log.Printf("reject event %s: buffer limit %d exceeded (buffered=%d, watermark=%d)",
			formatEvent(e), j.bufferLimit, len(j.buffered), j.watermark)
		return StatusResolved, nil, ErrBufferLimitExceeded
	}

	seq := j.seqCounter
	j.seqCounter++

	if e.EventTime <= j.watermark {
		r := j.resolve(seq, e)
		j.emitted = append(j.emitted, r)
		j.log.Printf("accept event %s: resolved immediately at watermark=%d -> %s",
			formatEvent(e), j.watermark, formatResultBasis(r, j.versions[e.Key], e.EventTime))
		return StatusResolved, &r, nil
	}

	j.buffered = append(j.buffered, pending{seq: seq, e: e})
	j.log.Printf("accept event %s: buffered (eventTime=%d > watermark=%d, buffered=%d)",
		formatEvent(e), e.EventTime, j.watermark, len(j.buffered))
	return StatusBuffered, nil, nil
}

// AdvanceWatermark 将水位线推进到 ts，并返回因此次推进而最终确定的事件
// 结果（批内按事件接受顺序）。
//
// ts 必须不小于当前水位线；水位线回退返回 ErrWatermarkRegression 且
// 不改变任何状态（本次不输出任何结果）。相等的推进是幂等空操作。
func (j *Joiner) AdvanceWatermark(ts int64) ([]Result, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	if ts < j.watermark {
		j.log.Printf("reject watermark advance %d -> %d: regression", j.watermark, ts)
		return nil, ErrWatermarkRegression
	}
	if ts == j.watermark {
		j.log.Printf("watermark unchanged at %d: nothing to resolve", ts)
		return nil, nil
	}

	old := j.watermark
	j.watermark = ts

	out := j.drainBufferLocked()
	j.log.Printf("advance watermark %d -> %d: %d event(s) resolved, %d remain buffered",
		old, ts, len(out), len(j.buffered))
	return out, nil
}

// Drain 宣告输入结束：将水位线推进到 +Inf 并输出所有仍在缓冲的事件
// （按接受顺序）。此后任何版本变更都会因迟到被拒绝。
func (j *Joiner) Drain() []Result {
	j.mu.Lock()
	defer j.mu.Unlock()

	old := j.watermark
	j.watermark = math.MaxInt64
	out := j.drainBufferLocked()
	j.log.Printf("drain: watermark %d -> +Inf: %d event(s) resolved, %d remain buffered",
		old, len(out), len(j.buffered))
	return out
}

// Results 返回截至目前全部已确定输出的快照，按事件接受顺序（Seq）排列。
// 立即确定与缓冲后确定的事件在此合并为一条统一的、与输入接受顺序一致的结果流。
func (j *Joiner) Results() []Result {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Result, len(j.emitted))
	copy(out, j.emitted)
	sort.SliceStable(out, func(a, b int) bool { return out[a].Seq < out[b].Seq })
	return out
}

// Watermark 返回当前水位线。
func (j *Joiner) Watermark() int64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.watermark
}

// resolve 对单条事件做朴素时态查询：取生效起点不大于 t 且起点最大的版本。
// 调用方必须持有 j.mu。
func (j *Joiner) resolve(seq int, e Event) Result {
	r := Result{
		Seq:       seq,
		Key:       e.Key,
		EventTime: e.EventTime,
		Payload:   e.Payload,
		Kind:      KindMiss,
	}
	vs := j.versions[e.Key]
	// 最大的 EffectiveAt <= EventTime：搜索第一个 > EventTime 的位置再回退一格。
	idx := sort.Search(len(vs), func(i int) bool { return vs[i].EffectiveAt > e.EventTime })
	if idx == 0 {
		return r // 不存在生效起点不大于事件时间的版本
	}
	v := vs[idx-1]
	if v.Tombstone {
		return r // 命中墓碑 => 区间内无值
	}
	r.Kind = KindHit
	r.EffectiveAt = v.EffectiveAt
	r.Value = v.Value
	return r
}

// drainBufferLocked 确定所有事件时间不超过当前水位线的缓冲事件，
// 保持其余事件的缓冲顺序。调用方必须持有 j.mu。
func (j *Joiner) drainBufferLocked() []Result {
	if len(j.buffered) == 0 {
		return nil
	}
	kept := j.buffered[:0]
	var out []Result
	for _, p := range j.buffered {
		if p.e.EventTime <= j.watermark {
			r := j.resolve(p.seq, p.e)
			j.emitted = append(j.emitted, r)
			out = append(out, r)
			j.log.Printf("resolve buffered event %s: watermark=%d -> %s",
				formatEvent(p.e), j.watermark, formatResultBasis(r, j.versions[p.e.Key], p.e.EventTime))
		} else {
			kept = append(kept, p)
		}
	}
	j.buffered = kept
	return out
}

// formatEvent / formatVersion / formatResultBasis 构造日志文本。
func formatEvent(e Event) string {
	return "event{key=" + quoteEmpty(e.Key) + ", eventTime=" + itoa(e.EventTime) +
		", payload=" + quoteEmpty(e.Payload) + "}"
}

func formatVersion(v Version) string {
	if v.Tombstone {
		return "version{key=" + quoteEmpty(v.Key) + ", effectiveAt=" + itoa(v.EffectiveAt) + ", tombstone}"
	}
	return "version{key=" + quoteEmpty(v.Key) + ", effectiveAt=" + itoa(v.EffectiveAt) +
		", value=" + quoteEmpty(v.Value) + "}"
}

// formatResultBasis 给出连接结果及其判定依据，用于日志。
func formatResultBasis(r Result, vs []Version, eventTime int64) string {
	switch r.Kind {
	case KindHit:
		return "HIT effectiveAt=" + itoa(r.EffectiveAt) +
			" (largest effectiveAt <= eventTime=" + itoa(eventTime) + ")"
	default:
		idx := sort.Search(len(vs), func(i int) bool { return vs[i].EffectiveAt > eventTime })
		if idx == 0 {
			return "MISS (no version with effectiveAt <= eventTime=" + itoa(eventTime) + ")"
		}
		return "MISS (tombstone at effectiveAt=" + itoa(vs[idx-1].EffectiveAt) +
			" for eventTime=" + itoa(eventTime) + ")"
	}
}

func quoteEmpty(s string) string {
	if s == "" {
		return `""`
	}
	return s
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
