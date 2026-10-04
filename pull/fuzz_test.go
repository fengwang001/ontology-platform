package pull_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"ontology/cursor"
	"ontology/pull"
	"ontology/sink"
)

// naivePuller 是按规则逐条写成的朴素模拟，用于与正式实现对拍。
// 不复用 cursor 包比较函数与 sink 包，逻辑独立。
type naivePuller struct {
	src     *fakeSource
	D, B    int64
	limit   int
	S       int64
	curTS   int64
	curID   int64
	applied map[int64]int64
	dead    map[[2]int64]sink.Row
	maxNow  int64
}

func newNaive(src *fakeSource, D, B, limit, S int64) *naivePuller {
	return &naivePuller{
		src:     src,
		D:       D,
		B:       B,
		limit:   int(limit),
		S:       S,
		applied: map[int64]int64{},
		dead:    map[[2]int64]sink.Row{},
	}
}

func (n *naivePuller) pull(now int64) (pull.Stats, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return pull.Stats{}, pull.ErrInvalid
	}
	if now < n.maxNow {
		return pull.Stats{}, pull.ErrClock
	}
	hz := now - n.D
	if hz < 0 {
		n.maxNow = now
		return pull.Stats{}, nil
	}
	var all []sink.Row
	queries := 0
	afterTS := n.curTS - n.B
	if afterTS < 0 {
		afterTS = 0
	}
	afterID := int64(0)
	for {
		page, err := n.src.Query(cursor.Cursor{TS: afterTS, ID: afterID}, n.limit, hz, now)
		queries++
		if err != nil {
			return pull.Stats{}, pull.ErrSource
		}
		if len(page) > n.limit {
			return pull.Stats{}, pull.ErrInvalid
		}
		prevTS, prevID := afterTS, afterID
		for _, r := range page {
			if r.ID < 1 || r.ID > 1_000_000_000 ||
				r.TS < 0 || r.TS > 1_000_000_000_000 ||
				r.Ver < 1 || r.Ver > 1_000_000_000 || r.SV < 1 {
				return pull.Stats{}, pull.ErrInvalid
			}
			if r.TS > hz {
				return pull.Stats{}, pull.ErrInvalid
			}
			if r.TS < prevTS || (r.TS == prevTS && r.ID <= prevID) {
				return pull.Stats{}, pull.ErrInvalid
			}
			prevTS, prevID = r.TS, r.ID
		}
		all = append(all, page...)
		if len(page) < n.limit {
			break
		}
		last := page[len(page)-1]
		afterTS, afterID = last.TS, last.ID
	}
	var st pull.Stats
	st.Queries = queries
	for _, r := range all {
		if r.SV > n.S {
			k := [2]int64{r.ID, r.Ver}
			if _, ok := n.dead[k]; !ok {
				n.dead[k] = r
				st.Dead++
			}
			continue
		}
		if r.Ver > n.applied[r.ID] {
			n.applied[r.ID] = r.Ver
			st.Applied++
		} else {
			st.Dup++
		}
	}
	if len(all) > 0 {
		last := all[len(all)-1]
		if last.TS > n.curTS || (last.TS == n.curTS && last.ID > n.curID) {
			n.curTS, n.curID = last.TS, last.ID
		}
	}
	n.maxNow = now
	return st, nil
}

func (n *naivePuller) setSchema(role, s int64) error {
	if s < 1 || s > 100 {
		return pull.ErrInvalid
	}
	if role != 2 {
		return pull.ErrPerm
	}
	if s < n.S {
		return pull.ErrDowngrade
	}
	n.S = s
	return nil
}

func errKind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, pull.ErrInvalid):
		return "invalid"
	case errors.Is(err, pull.ErrClock):
		return "clock"
	case errors.Is(err, pull.ErrPerm):
		return "perm"
	case errors.Is(err, pull.ErrDowngrade):
		return "downgrade"
	case errors.Is(err, pull.ErrSource):
		return "source"
	default:
		return "unknown"
	}
}

type fuzzOp struct {
	schema  bool
	now     int64
	role, s int64
}

func deadEqual(a map[sink.Key]sink.Row, b map[[2]int64]sink.Row) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		bv, ok := b[[2]int64{k.ID, k.Ver}]
		if !ok || bv != v {
			return false
		}
	}
	return true
}

// 与朴素模拟对拍 2000 组随机源端与操作序列；日志打印输入、输出与判定依据。
func TestFuzzAgainstNaive(t *testing.T) {
	const cases = 2000
	for c := 0; c < cases; c++ {
		rng := rand.New(rand.NewSource(int64(c)*7919 + 1))
		D := rng.Int63n(21)
		B := rng.Int63n(21)
		limit := int64(1 + rng.Int63n(5))
		S := int64(1 + rng.Int63n(3))

		var rows []srcRow
		numIDs := int64(1 + rng.Int63n(8))
		for id := int64(1); id <= numIDs; id++ {
			vers := int64(1 + rng.Int63n(4))
			ts := rng.Int63n(50)
			for v := int64(1); v <= vers; v++ {
				ts += rng.Int63n(40)
				delay := rng.Int63n(31) // 可超过 D+B → 漏拉
				sv := int64(1 + rng.Int63n(4))
				rows = append(rows, srcRow{id, ts, v, ts + delay, sv})
			}
		}

		var ops []fuzzOp
		lastNow := int64(0)
		numOps := 5 + rng.Int63n(25)
		for i := int64(0); i < numOps; i++ {
			if rng.Int63n(100) < 20 {
				role := int64(2)
				if rng.Int63n(100) < 30 {
					role = rng.Int63n(2) // 0 或 1 → 权限不足
				}
				s := S + rng.Int63n(5) - 2 // 可能越界或降级
				ops = append(ops, fuzzOp{schema: true, role: role, s: s})
				continue
			}
			var now int64
			switch r := rng.Int63n(100); {
			case r < 75:
				lastNow += rng.Int63n(50)
				now = lastNow
			case r < 85:
				now = lastNow - 1 - rng.Int63n(10) // 时钟回退或非法
			case r < 90:
				now = lastNow
			case r < 95:
				now = 1_000_000_000_001 + rng.Int63n(100) // 非法
			default:
				now = -1 - rng.Int63n(10) // 非法
			}
			ops = append(ops, fuzzOp{now: now})
		}

		errAt := map[int]bool{}
		if rng.Int63n(100) < 30 {
			for j := int64(0); j < 1+rng.Int63n(3); j++ {
				errAt[1+int(rng.Int63n(40))] = true
			}
		}

		runReal := func() ([]pull.Stats, []string, pull.Snapshot) {
			p, err := pull.New(&fakeSource{rows: rows, errAt: errAt}, D, B, limit, S)
			if err != nil {
				t.Fatalf("case %d: New: %v", c, err)
			}
			stats := make([]pull.Stats, 0, len(ops))
			kinds := make([]string, 0, len(ops))
			for _, op := range ops {
				if op.schema {
					kinds = append(kinds, errKind(p.SetSchema(op.role, op.s)))
					stats = append(stats, pull.Stats{})
				} else {
					st, err := p.Pull(op.now)
					stats = append(stats, st)
					kinds = append(kinds, errKind(err))
				}
			}
			return stats, kinds, p.Snapshot()
		}

		stats1, kinds1, snap1 := runReal()
		stats2, kinds2, snap2 := runReal()

		n := newNaive(&fakeSource{rows: rows, errAt: errAt}, D, B, limit, S)
		nStats := make([]pull.Stats, 0, len(ops))
		nKinds := make([]string, 0, len(ops))
		for _, op := range ops {
			if op.schema {
				nKinds = append(nKinds, errKind(n.setSchema(op.role, op.s)))
				nStats = append(nStats, pull.Stats{})
			} else {
				st, err := n.pull(op.now)
				nStats = append(nStats, st)
				nKinds = append(nKinds, errKind(err))
			}
		}

		ok := true
		why := ""
		switch {
		case !reflect.DeepEqual(stats1, stats2) || !reflect.DeepEqual(kinds1, kinds2) ||
			!reflect.DeepEqual(snap1, snap2):
			ok, why = false, "相同输入重放两次结果不一致"
		case !reflect.DeepEqual(stats1, nStats):
			ok, why = false, "Stats 与朴素模拟不一致"
		case !reflect.DeepEqual(kinds1, nKinds):
			ok, why = false, "错误类别与朴素模拟不一致"
		case snap1.Cur != (cursor.Cursor{TS: n.curTS, ID: n.curID}):
			ok, why = false, "游标不一致"
		case !reflect.DeepEqual(snap1.Applied, n.applied):
			ok, why = false, "applied 不一致"
		case !deadEqual(snap1.Dead, n.dead):
			ok, why = false, "死信不一致"
		case snap1.MaxNow != n.maxNow:
			ok, why = false, "maxNow 不一致"
		case snap1.S != n.S:
			ok, why = false, "S 不一致"
		}

		var log strings.Builder
		fmt.Fprintf(&log, "case %d 输入: D=%d B=%d limit=%d S=%d rows=%v ops=%v errAt=%v\n",
			c, D, B, limit, S, rows, ops, errAt)
		for i := range ops {
			fmt.Fprintf(&log, "  输出 op[%d]=%+v → real=%+v/%s naive=%+v/%s\n",
				i, ops[i], stats1[i], kinds1[i], nStats[i], nKinds[i])
		}
		fmt.Fprintf(&log, "  判定依据: 逐步 Stats 与错误类别、最终 cur/applied/dead/maxNow/S 逐项相等，且重放一致；ok=%v %s",
			ok, why)
		t.Log(log.String())
		if !ok {
			t.Fatalf("case %d 对拍失败: %s", c, why)
		}
	}
}
