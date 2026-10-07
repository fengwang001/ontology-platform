package audit

import (
	"fmt"
	"sync"
)

// frame 是前向重放的工作状态：
//   - values：实例当前值；
//   - prov：每个实例当前值由哪条动作记录贡献（动作自身序号；
//     订正不改变来源，只替换该来源的有效值）；
//   - eff：每条已订正动作各实例“最近一次订正后”的有效值。
//
// 订正语义（重放视角）：订正记录替换其指向动作的“增量”，随后的
// 正常提交若又写过同一实例，则以随后的提交为准（订正只覆盖原记录
// 对后续重建的影响，不倒装更晚发生的独立写入）。
type frame struct {
	values map[string]string
	prov   map[string]int64
	eff    map[int64]map[string]string
}

func newFrame() *frame {
	return &frame{
		values: map[string]string{},
		prov:   map[string]int64{},
		eff:    map[int64]map[string]string{},
	}
}

func (f *frame) clone() *frame {
	cp := newFrame()
	for k, v := range f.values {
		cp.values[k] = v
		cp.prov[k] = f.prov[k]
	}
	for seq, m := range f.eff {
		nm := make(map[string]string, len(m))
		for k, v := range m {
			nm[k] = v
		}
		cp.eff[seq] = nm
	}
	return cp
}

func (f *frame) state() map[string]string {
	out := make(map[string]string, len(f.values))
	for k, v := range f.values {
		out[k] = v
	}
	return out
}

// replayCheckpoint 记录“应用完 seq 及之前全部记录后”的状态帧。
type replayCheckpoint struct {
	seq   int64
	frame *frame
}

// Replayer 依据只增审计序列重放重建状态。
//
// 为了让区间重放的开销只与区间长度相关、与历史总长无关，Replayer
// 维护每 interval 条记录一个的周期快照：
//
//   - 在线阶段订阅日志追加事件，前向维护状态帧，每 interval 条
//     记录做不可变快照（每条记录摊还 O(1)）；
//   - 重放任意区间 (from,to] 时，从「不晚于 from 的最近快照」出发，
//     仅线性扫描到 to，读取记录数 ≤ interval + 区间长度，
//     与该类型历史记录总数无关；
//   - 回退记录的变更被跳过（只视作一次尝试）；订正记录替换其指向
//     原动作的增量，原记录在序列中原样保留。
type Replayer struct {
	log      *AuditLog
	interval int64
	mu       sync.Mutex
	snaps    map[string][]replayCheckpoint
	// current 为每类型持续推进的在线状态帧。
	current map[string]*frame
}

// NewReplayer 创建重放器并订阅日志在线追加事件。
func NewReplayer(log *AuditLog, snapshotInterval int) *Replayer {
	if snapshotInterval < 1 {
		snapshotInterval = 64
	}
	r := &Replayer{
		log:      log,
		interval: int64(snapshotInterval),
		snaps:    make(map[string][]replayCheckpoint),
		current:  make(map[string]*frame),
	}
	log.Subscribe(r.onAppend)
	return r
}

func (r *Replayer) onAppend(typeName string, rec Record) {
	r.mu.Lock()
	defer r.mu.Unlock()

	f := r.current[typeName]
	if f == nil {
		f = newFrame()
		r.current[typeName] = f
	}
	applyRecord(f, rec)

	if rec.Seq%r.interval == 0 {
		r.snaps[typeName] = append(r.snaps[typeName], replayCheckpoint{
			seq:   rec.Seq,
			frame: f.clone(),
		})
	}
}

// latestSnapshotLocked 返回不晚于 seq 的最近快照（无则空帧,0）。
func (r *Replayer) latestSnapshotLocked(typeName string, seq int64) replayCheckpoint {
	list := r.snaps[typeName]
	lo, hi := 0, len(list)
	for lo < hi {
		mid := (lo + hi) / 2
		if list[mid].seq <= seq {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	if lo == 0 {
		return replayCheckpoint{seq: 0, frame: newFrame()}
	}
	// 返回快照帧的拷贝，避免调用方污染不可变快照。
	return replayCheckpoint{seq: list[lo-1].seq, frame: list[lo-1].frame.clone()}
}

// scanFrom 从某快照帧出发，把 (cp.seq, to] 的记录前向应用到副本，
// 返回应用至 to 后的帧以及本次实际读取的记录数。
func (r *Replayer) scanFrom(typeName string, cp replayCheckpoint, to int64) (*frame, int64) {
	f := cp.frame.clone()
	records := r.log.Range(typeName, cp.seq, to)
	var maxSeq int64 = cp.seq
	for _, rec := range records {
		applyRecord(f, rec)
		maxSeq = rec.Seq
	}
	return f, maxSeq
}

// Replay 重放半开序号区间 (fromSeq, toSeq]。
//
// StateFrom = 第 fromSeq 条记录之后的状态（fromSeq=0 为初始态）；
// StateTo   = 第 toSeq 条记录之后的状态；
// Events    = 区间内导致状态差异的全部变更（回退记录不产生差异）。
// 同一区间任意多次重放结果完全相同（纯读取，不修改审计序列）。
func (r *Replayer) Replay(typeName string, fromSeq, toSeq int64) (ReplayResult, error) {
	if fromSeq < 0 || toSeq < fromSeq {
		return ReplayResult{}, &IllegalRequestError{
			Reason: fmt.Sprintf("invalid range [%d,%d]", fromSeq, toSeq),
		}
	}

	r.mu.Lock()
	base := r.latestSnapshotLocked(typeName, fromSeq)
	r.mu.Unlock()

	// 1) 扫描到 to，得到区间终点状态。
	toFrame, _ := r.scanFrom(typeName, base, toSeq)

	// 2) 从同一快照基础扫描到 from，得到区间起点状态。
	fromFrame, _ := r.scanFrom(typeName, base, fromSeq)

	// 3) 计算区间内差异：在 from 态上逐条应用并记录真正发生的变化。
	events := make([]ReplayEvent, 0)
	cursor := fromFrame.clone()
	for _, rec := range r.log.Range(typeName, fromSeq, toSeq) {
		diff := applyRecord(cursor, rec)
		if len(diff) > 0 {
			events = append(events, ReplayEvent{
				Seq:      rec.Seq,
				Kind:     rec.Kind,
				ActionID: rec.ActionID,
				Changes:  diff,
			})
		}
	}

	return ReplayResult{
		FromSeq:   fromSeq,
		ToSeq:     toSeq,
		StateFrom: fromFrame.state(),
		StateTo:   toFrame.state(),
		Events:    events,
	}, nil
}

// applyRecord 按记录语义前向推进状态帧，返回本次真正产生的差异。
//
//   - 提交动作（含实例注册）：把 After 写入各实例，来源记为该序号；
//   - 回退记录：跳过（Before==After，仅表示一次尝试）；
//   - 订正记录：替换目标动作各实例的有效值；若某实例当前仍由该
//     目标动作贡献，则立即按新有效值改变状态，否则保持更晚写入的
//     值不动。
func applyRecord(f *frame, rec Record) []Change {
	switch rec.Kind {
	case KindAction:
		diff := make([]Change, 0, len(rec.Changes))
		for _, c := range rec.Changes {
			if f.values[c.Instance] != c.After {
				diff = append(diff, Change{
					Instance: c.Instance,
					Before:   f.values[c.Instance],
					After:    c.After,
				})
			}
			f.values[c.Instance] = c.After
			f.prov[c.Instance] = rec.Seq
		}
		return diff

	case KindCorrection:
		// 记录/更新目标动作的有效订正值。
		eff := f.eff[rec.TargetSeq]
		if eff == nil {
			eff = make(map[string]string, len(rec.Changes))
			f.eff[rec.TargetSeq] = eff
		}
		diff := make([]Change, 0, len(rec.Changes))
		for _, c := range rec.Changes {
			eff[c.Instance] = c.After
			// 仅当该实例当前值仍来源于被订正动作时才覆盖。
			if f.prov[c.Instance] == rec.TargetSeq && f.values[c.Instance] != c.After {
				diff = append(diff, Change{
					Instance: c.Instance,
					Before:   f.values[c.Instance],
					After:    c.After,
				})
				f.values[c.Instance] = c.After
			}
		}
		return diff

	case KindRollback:
		return nil

	default:
		return nil
	}
}

// StateAt 是重放到指定序号的便捷封装。
func (r *Replayer) StateAt(typeName string, seq int64) (map[string]string, error) {
	res, err := r.Replay(typeName, 0, seq)
	if err != nil {
		return nil, err
	}
	return res.StateTo, nil
}

// SnapshotInterval 暴露快照间隔，供成本证明测试引用。
func (r *Replayer) SnapshotInterval() int64 { return r.interval }
