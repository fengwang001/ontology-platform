package incremental

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"testing"
)

// naiveModel 是按规则逐步独立判定的朴素参照模型：刻意使用最直接
// 的实现（线性扫描去重、逐条重放历史），与被测组件的优化实现对照。
type naiveModel struct {
	maxWrites int
	window    int

	confirmed Cursor
	corrupt   bool
	memCursor Cursor
	hasMem    bool

	recs    []IncrementRecord // 虚拟账台（含被丢弃记录）
	dropped map[uint64]bool

	output []string // 本进程已发布输出

	inCycle          bool
	cycStart, cycEnd Cursor
	cycIDs           []string
}

func newNaiveModel(maxWrites, window int) *naiveModel {
	return &naiveModel{
		maxWrites: maxWrites,
		window:    window,
		dropped:   map[uint64]bool{},
	}
}

// visibleSeq 返回序号为 seq 且未被丢弃的记录。
func (m *naiveModel) visibleSeq(seq uint64) (IncrementRecord, bool) {
	if seq == 0 || int(seq) > len(m.recs) || m.dropped[seq] {
		return IncrementRecord{}, false
	}
	return m.recs[seq-1], true
}

// derive 朴素推导：从 1 号记录逐条向后走，遇缺口停止。
// 返回安全位点与是否发现缺口（缺口 = 停止点之后仍存在可见记录）。
func (m *naiveModel) derive() (Cursor, bool) {
	cursor := GenesisCursor
	var seq uint64 = 1
	for {
		rec, ok := m.visibleSeq(seq)
		if !ok {
			break
		}
		if rec.Start != cursor {
			break
		}
		cursor = rec.End
		seq++
	}
	gap := false
	for s := seq; int(s) <= len(m.recs); s++ {
		if _, ok := m.visibleSeq(s); ok {
			gap = true
			break
		}
	}
	return cursor, gap
}

// effective 计算当前生效的已确认位点（供生成器选择声明位点）。
func (m *naiveModel) effective() Cursor {
	if !m.corrupt {
		return m.confirmed
	}
	if m.hasMem {
		return m.memCursor
	}
	safe, _ := m.derive()
	return safe
}

type beginResult struct {
	kind     ErrorKind // -1 表示成功
	findings []ErrorKind
	safe     Cursor
	hasSafe  bool
}

func (m *naiveModel) begin(declaredStart, end Cursor) beginResult {
	var res beginResult
	res.kind = ErrorKind(-1)
	effective := m.confirmed
	if m.corrupt {
		res.findings = append(res.findings, ErrKindCursorUnreadable)
		if m.hasMem {
			effective = m.memCursor
		} else {
			safe, gap := m.derive()
			m.memCursor, m.hasMem = safe, true
			if gap {
				res.kind = ErrKindHistoryGap
				res.safe, res.hasSafe = safe, true
				return res
			}
			effective = safe
		}
	}
	if declaredStart != effective {
		res.kind = ErrKindStartMismatch
		return res
	}
	m.inCycle = true
	m.cycStart, m.cycEnd = declaredStart, end
	m.cycIDs = nil
	return res
}

func (m *naiveModel) inWindow(id string) bool {
	var ids []string
	for s := len(m.recs); s >= 1 && len(ids) < m.window; s-- {
		rec, ok := m.visibleSeq(uint64(s))
		if !ok {
			continue
		}
		for j := len(rec.WriteIDs) - 1; j >= 0 && len(ids) < m.window; j-- {
			ids = append(ids, rec.WriteIDs[j])
		}
	}
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// submit 返回错误类别（-1 表示成功）。
func (m *naiveModel) submit(id string) ErrorKind {
	if !m.inCycle {
		panic("model: submit outside cycle")
	}
	for _, x := range m.cycIDs { // 朴素线性去重
		if x == id {
			return ErrorKind(-1)
		}
	}
	if m.window > 0 && m.inWindow(id) {
		return ErrorKind(-1)
	}
	if m.maxWrites > 0 && len(m.cycIDs) >= m.maxWrites {
		m.inCycle = false // 资源不足：中止周期
		m.cycIDs = nil
		return ErrKindResourceExhausted
	}
	m.cycIDs = append(m.cycIDs, id)
	return ErrorKind(-1)
}

func (m *naiveModel) commit() {
	if !m.inCycle {
		panic("model: commit outside cycle")
	}
	m.recs = append(m.recs, IncrementRecord{
		Chain:    "diff",
		Seq:      uint64(len(m.recs)) + 1,
		Start:    m.cycStart,
		End:      m.cycEnd,
		WriteIDs: append([]string(nil), m.cycIDs...),
	})
	m.confirmed = m.cycEnd
	m.memCursor, m.hasMem = m.cycEnd, true
	m.output = append(m.output, m.cycIDs...)
	m.inCycle = false
	m.cycIDs = nil
}

func (m *naiveModel) abort() {
	m.inCycle = false
	m.cycIDs = nil
}

// errKindOf 提取组件返回错误的类别；-1 表示无错误或非组件错误。
func errKindOf(err error) ErrorKind {
	if err == nil {
		return ErrorKind(-1)
	}
	var ierr *Error
	if errors.As(err, &ierr) {
		return ierr.Kind
	}
	return ErrorKind(-1)
}

// TestDifferentialAgainstNaiveModel 在大量随机构造的中断与重试序列上，
// 将组件与朴素参照模型逐步对照，并记录每次的输入、输出与判定依据。
func TestDifferentialAgainstNaiveModel(t *testing.T) {
	const chains = 1
	for seed := int64(0); seed < 30; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			opts := Options{MaxCycleWrites: 1 + rng.Intn(6), DedupWindow: 1 + rng.Intn(8)}
			src := NewMemSource()
			cs := NewMemCursorStore()
			ledger := NewMemLedger()
			exp := NewExporter(src, cs, ledger, opts)
			model := newNaiveModel(opts.MaxCycleWrites, opts.DedupWindow)

			var cyc *Cycle // 当前打开的周期（组件侧）
			writeN := 0
			appendSource := func(n int) {
				for i := 0; i < n; i++ {
					src.Append(Write{ID: fmt.Sprintf("w%d", writeN), Payload: "p"})
					writeN++
				}
			}
			appendSource(1 + rng.Intn(4))

			for step := 0; step < 400; step++ {
				head, _ := src.Head()
				log := func(format string, args ...any) {
					t.Logf("seed=%d step=%d "+format, append([]any{seed, step}, args...)...)
				}
				if !model.inCycle {
					switch rng.Intn(6) {
					case 0, 1: // 追加源写入
						n := 1 + rng.Intn(4)
						appendSource(n)
						log("op=append-source n=%d head=%d", n, writeN)
					case 2: // 损坏位点记录
						cs.Corrupt("diff")
						model.corrupt = true
						log("op=corrupt-cursor")
					case 3: // 丢弃一条历史记录（制造缺口）
						if len(model.recs) > 0 {
							seq := uint64(1 + rng.Intn(len(model.recs)))
							ledger.Drop("diff", seq)
							model.dropped[seq] = true
							log("op=drop-record seq=%d", seq)
						}
					case 4: // 模拟进程重启
						exp = NewExporter(src, cs, ledger, opts)
						model.hasMem = false
						model.output = nil
						log("op=restart")
					default: // 开启周期
						eff := model.effective()
						declared := eff
						if rng.Intn(4) == 0 { // 25% 概率声明错误起始位点
							declared = Cursor(rng.Intn(int(head) + 2))
						}
						end := eff + Cursor(rng.Intn(int(head-eff)+1))
						if end < declared {
							end = declared
						}
						want := model.begin(declared, end)
						got, err := exp.BeginCycle("diff", declared, end)
						if k := errKindOf(err); k != want.kind {
							t.Fatalf("begin(%d,%d): kind=%v, model=%v", declared, end, k, want.kind)
						}
						if want.kind == ErrKindHistoryGap {
							var ierr *Error
							if !errors.As(err, &ierr) || ierr.SafeCursor != want.safe {
								t.Fatalf("begin(%d,%d): safe=%v, model=%v", declared, end, err, want.safe)
							}
						}
						var gotFindings []ErrorKind
						if err == nil {
							for _, f := range got.Findings {
								gotFindings = append(gotFindings, f.Kind)
							}
							cyc = got
						} else {
							var ierr *Error
							if errors.As(err, &ierr) {
								for _, f := range ierr.Findings {
									gotFindings = append(gotFindings, f.Kind)
								}
							}
						}
						if !slices.Equal(gotFindings, want.findings) {
							t.Fatalf("begin(%d,%d): findings=%v, model=%v",
								declared, end, gotFindings, want.findings)
						}
						log("op=begin declared=%d end=%d kind=%v findings=%v (basis: effective=%d corrupt=%v)",
							declared, end, want.kind, want.findings, eff, model.corrupt)
					}
					continue
				}

				// 周期内操作
				switch rng.Intn(10) {
				case 0, 1: // 确认结束位点
					model.commit()
					if err := cyc.Commit(); err != nil {
						t.Fatalf("commit: %v", err)
					}
					if got := writeIDs(exp.Published("diff")); !slices.Equal(got, model.output) {
						t.Fatalf("output=%v, model=%v", got, model.output)
					}
					log("op=commit output=%v", model.output)
					cyc = nil
				case 2, 3: // 中断（未确认即放弃）
					model.abort()
					cyc.Abort()
					cyc = nil
					log("op=abort (interrupt before confirm)")
				default: // 提交写入（含重试与跨周期重复）
					if model.cycEnd <= model.cycStart {
						continue // 空区间无可提交写入
					}
					var id string
					var cursor Cursor
					choice := rng.Intn(10)
					switch {
					case choice < 5 || len(model.cycIDs) == 0 && choice < 7:
						// 新写入：从 (start, end] 范围内取源写入
						ws, _ := src.Scan(model.cycStart, model.cycEnd)
						if len(ws) == 0 {
							continue
						}
						w := ws[rng.Intn(len(ws))]
						id, cursor = w.ID, w.Cursor
					case choice < 8 && len(model.cycIDs) > 0:
						// 本周期内重试已提交过的写入
						id = model.cycIDs[rng.Intn(len(model.cycIDs))]
						cursor = model.cycStart + 1 + Cursor(rng.Intn(int(model.cycEnd-model.cycStart)))
					default:
						// 跨周期重复：旧写入以新位点重新提交
						id = fmt.Sprintf("w%d", rng.Intn(writeN))
						cursor = model.cycStart + 1 + Cursor(rng.Intn(int(model.cycEnd-model.cycStart)))
					}
					want := model.submit(id)
					got := errKindOf(cyc.Submit(Write{ID: id, Cursor: cursor, Payload: "p"}))
					if got != want {
						t.Fatalf("submit(%q): kind=%v, model=%v", id, got, want)
					}
					log("op=submit id=%q cursor=%d kind=%v (basis: in-cycle-dup=%v window-hit=%v buffered=%d/%d)",
						id, cursor, want, contains(model.cycIDs, id), model.inWindow(id),
						len(model.cycIDs), model.maxWrites)
					if want == ErrKindResourceExhausted {
						cyc = nil // 组件已自动中止周期
					}
				}
			}
			_ = chains
		})
	}
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
