package pull_test

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/pull"
)

// oracleState 是完全独立于产品代码、按规格逐条手写的朴素模拟状态。
type oracleState struct {
	curTs, curID int64
	maxNow       int64
	applied      map[int64]int64
	dlq          map[[2]int64]srcRow
	s            int64
}

type oracleResult struct {
	applied, dup, newDLQ, queries int
	errKind                       string // "" | "invalid" | "rollback" | "source"
}

func newOracle(s int64) *oracleState {
	return &oracleState{
		applied: make(map[int64]int64),
		dlq:     make(map[[2]int64]srcRow),
		s:       s,
	}
}

// oraclePull 按规格朴素执行一轮：校验 → 分页读 → 整轮原子应用。
func (o *oracleState) pull(src *memSource, now, d, b, limit int64) oracleResult {
	if now < 0 || now > 1_000_000_000_000 {
		return oracleResult{errKind: "invalid"}
	}
	if now < o.maxNow {
		return oracleResult{errKind: "rollback"}
	}
	hz := now - d
	if hz < 0 {
		o.maxNow = now
		return oracleResult{}
	}
	startTs := o.curTs - b
	if startTs < 0 {
		startTs = 0
	}

	// 整轮读到临时变量里，任一页失败/非法则整轮作废。
	type pos struct{ ts, id int64 }
	after := pos{startTs, 0}
	var read []srcRow
	queries := 0
	for {
		prows, qerr := src.Query(after.ts, after.id, limit, hz, now)
		queries++
		if qerr != nil {
			return oracleResult{queries: queries, errKind: "source"}
		}
		page := make([]srcRow, len(prows))
		for i, r := range prows {
			page[i] = srcRow{r.ID, r.Ts, r.Ver, r.Sv, 0}
		}
		read = append(read, page...)
		if int64(len(page)) < limit {
			break
		}
		after = pos{page[len(page)-1].ts, page[len(page)-1].id}
	}

	// 全部页成功后才应用（原子提交）。
	var gotApplied, gotDup, gotNewDLQ int
	for _, r := range read {
		if r.sv > o.s {
			k := [2]int64{r.id, r.ver}
			if _, ok := o.dlq[k]; !ok {
				o.dlq[k] = r
				gotNewDLQ++
			}
			continue
		}
		if r.ver > o.applied[r.id] {
			o.applied[r.id] = r.ver
			gotApplied++
		} else {
			gotDup++
		}
	}
	if n := len(read); n > 0 {
		last := read[n-1]
		if last.ts > o.curTs || (last.ts == o.curTs && last.id > o.curID) {
			o.curTs, o.curID = last.ts, last.id
		}
	}
	o.maxNow = now
	return oracleResult{gotApplied, gotDup, gotNewDLQ, queries, ""}
}

func (o *oracleState) setSchema(role, ns int64) string {
	if ns < 1 || ns > 100 {
		return "invalid"
	}
	if role != 2 {
		return "forbidden"
	}
	if ns < o.s {
		return "downgrade"
	}
	o.s = ns
	return ""
}

// opKind 为操作类型。
const (
	opPull = iota
	opPullBad
	opSchema
	opSchemaBad
)

type op struct {
	kind   int
	now    int64
	role   int64
	ns     int64
	failAt int // >0 时源端在本轮第 failAt 次 Query 报错
}

// genCase 生成一组随机源端与操作序列。
func genCase(rng *rand.Rand) (d, b, limit, s0 int64, rows []srcRow, ops []op) {
	d = rng.Int63n(12)
	b = rng.Int63n(12)
	limit = rng.Int63n(4) + 1
	s0 = 1

	nRows := rng.Intn(12)
	used := make(map[int64]bool)
	for i := 0; i < nRows; i++ {
		var id int64
		for {
			id = rng.Int63n(8) + 1
			if !used[id] || rng.Intn(2) == 0 {
				break
			}
		}
		ts := rng.Int63n(60)
		var commit int64
		switch rng.Intn(3) {
		case 0:
			commit = ts
		case 1:
			commit = ts + rng.Int63n(d+1) // 不超过 D
		default:
			commit = ts + rng.Int63n(20) // 可能晚于 D，制造长尾
		}
		ver := rng.Int63n(3) + 1
		sv := int64(1)
		if rng.Intn(4) == 0 {
			sv = 2
		}
		rows = append(rows, srcRow{id, ts, ver, sv, commit})
	}

	var now int64
	nOps := rng.Intn(14) + 1
	for i := 0; i < nOps; i++ {
		switch rng.Intn(6) {
		case 0, 1:
			now += rng.Int63n(25)
			o := op{kind: opPull, now: now}
			if rng.Intn(5) == 0 {
				o.failAt = rng.Intn(4) + 1
			}
			ops = append(ops, o)
		case 2:
			// 显式时钟回退（10%）
			ops = append(ops, op{kind: opPull, now: now - rng.Int63n(3) - 1})
		case 3:
			ops = append(ops, op{kind: opSchema, role: 2, ns: s0 + rng.Int63n(2)})
			s0 = 2
		case 4:
			ops = append(ops, op{kind: opSchemaBad, role: 1, ns: 2})
		default:
			ops = append(ops, op{kind: opPullBad, now: -1})
		}
	}
	return d, b, limit, s0, rows, ops
}

func errKindOf(err error) string {
	switch {
	case err == nil:
		return ""
	case eq(err, pull.ErrSource):
		return "source"
	case eq(err, pull.ErrClockRollback):
		return "rollback"
	case eq(err, pull.ErrInvalidParam):
		return "invalid"
	case eq(err, pull.ErrForbidden):
		return "forbidden"
	case eq(err, pull.ErrDowngrade):
		return "downgrade"
	default:
		return "unknown"
	}
}

func eq(err, target error) bool { return err.Error() == target.Error() }

// TestRandomDifferential 用 2000 组随机源端/操作序列将产品实现与朴素模拟逐步对拍。
func TestRandomDifferential(t *testing.T) {
	const cases = 2000
	rng := rand.New(rand.NewSource(20261003))
	for c := 0; c < cases; c++ {
		d, b, limit, s0, rows, ops := genCase(rng)
		src := &memSource{rows: rows}
		p, err := pull.New(src, d, b, limit, s0)
		if err != nil {
			t.Fatalf("case %d New: %v", c, err)
		}
		oc := newOracle(s0)

		var log []string
		log = append(log, fmt.Sprintf("case=%d D=%d B=%d limit=%d S=%d rows=%v", c, d, b, limit, s0, rows))

		for step, o := range ops {
			var got pull.Result
			var gerr error
			var want oracleResult

			switch o.kind {
			case opPull, opPullBad:
				src.failAtQueries = o.failAt
				src.reset()
				got, gerr = p.Pull(o.now)
				src.reset() // oracle 是对同一操作的独立重放，计数从 0 开始
				want = oc.pull(src, o.now, d, b, limit)
			case opSchema, opSchemaBad:
				gerr = p.SetSchema(o.role, o.ns)
				k := oc.setSchema(o.role, o.ns)
				want = oracleResult{errKind: k}
			}

			gk := errKindOf(gerr)
			ok := gk == want.errKind
			if gk == "" {
				ok = ok && got.Applied == want.applied && got.Dup == want.dup &&
					got.NewDLQ == want.newDLQ && got.Queries == want.queries
			} else if gk == "source" {
				ok = ok && got.Queries == want.queries
			}

			curTs, curID := p.Cur()
			ok = ok && curTs == oc.curTs && curID == oc.curID && p.MaxNow() == oc.maxNow &&
				p.Schema() == oc.s && p.DLQSize() == len(oc.dlq)
			for id, v := range oc.applied {
				if p.Applied(id) != v {
					ok = false
				}
			}
			for k, r := range oc.dlq {
				gr, present := p.DLQ(k[0], k[1])
				if !present || gr.ID != r.id || gr.Ver != r.ver || gr.Sv != r.sv {
					ok = false
				}
			}

			reason := "MATCH"
			if !ok {
				reason = "MISMATCH"
			}
			log = append(log, fmt.Sprintf(
				"  step=%d op=%+v -> got=(%+v,%s) want=(%+v) cur=(%d,%d) maxNow=%d S=%d dlq=%d [%s]",
				step, o, got, gk, want, curTs, curID, p.MaxNow(), p.Schema(), p.DLQSize(), reason))

			if !ok {
				for _, line := range log {
					t.Error(line)
				}
				return
			}
		}
		// -v 时打印每个用例的输入/输出/判定依据。
		t.Logf("%s\n  %d steps all MATCH", log[0], len(ops))
	}
}
