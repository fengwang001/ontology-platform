package fulljoin

import (
	"math/rand"
	"reflect"
	"testing"
)

// TestRandomDifferential 随机生成合法/非法混合批次，以朴素批量重算为基准，
// 逐步核对：成功批后视图、日志重放视图、自检；失败批的错误类别与状态不变。
func TestRandomDifferential(t *testing.T) {
	const iterations = 2000
	rng := rand.New(rand.NewSource(42))
	m := New()

	type shadowRow struct{ key, val string }
	shadowL := map[string]shadowRow{}
	shadowR := map[string]shadowRow{}

	shadowView := func() []OutRow {
		l := map[string]Row{}
		for id, s := range shadowL {
			l[id] = Row{Key: s.key, ID: id, Val: s.val}
		}
		r := map[string]Row{}
		for id, s := range shadowR {
			r[id] = Row{Key: s.key, ID: id, Val: s.val}
		}
		return referenceView(l, r)
	}

	for iter := 0; iter < iterations; iter++ {
		n := 1 + rng.Intn(4)
		batch := make([]Change, 0, n)
		injected := error(nil)

		// 影子副本模拟整批顺序效果，决定期望错误类别。
		simL, simR := map[string]shadowRow{}, map[string]shadowRow{}
		for id, s := range shadowL {
			simL[id] = s
		}
		for id, s := range shadowR {
			simR[id] = s
		}

		for i := 0; i < n; i++ {
			side := SideLeft
			sim := simL
			if rng.Intn(2) == 1 {
				side, sim = SideRight, simR
			}
			ids := make([]string, 0, len(sim))
			for id := range sim {
				ids = append(ids, id)
			}

			var c Change
			switch {
			case rng.Intn(12) == 0:
				// 注入空键。
				c = Change{Kind: pick(rng, '+', '-'), Side: side, Row: Row{Key: "", ID: "x" + itoa(iter) + "_" + itoa(i)}}
				if injected == nil {
					injected = ErrEmptyKey
				}
			case rng.Intn(8) == 0 && len(ids) > 0:
				// 注入重复插入。
				id := ids[rng.Intn(len(ids))]
				c = ins(side, "k0", id)
				if injected == nil {
					injected = ErrDuplicateID
				}
			case rng.Intn(8) == 0:
				// 注入删除不存在行。
				c = del(side, "k0", "ghost-"+itoa(iter)+"-"+itoa(i))
				if injected == nil {
					injected = ErrRowNotFound
				}
			case len(ids) > 0 && rng.Intn(2) == 0:
				id := ids[rng.Intn(len(ids))]
				c = Change{Kind: '-', Side: side, Row: Row{Key: sim[id].key, ID: id, Val: sim[id].val}}
				delete(sim, id)
			default:
				id := side.String() + itoa(iter) + "_" + itoa(i)
				c = Change{Kind: '+', Side: side, Row: Row{Key: "k" + itoa(rng.Intn(4)), ID: id, Val: "v" + id}}
				sim[id] = shadowRow{key: c.Row.Key, val: c.Row.Val}
			}
			batch = append(batch, c)
		}

		viewBefore := normalize(m.View())
		logBefore := m.Log()
		_, err := m.Apply(batch)

		if injected != nil {
			if err == nil {
				t.Fatalf("iter %d: 期望错误 %v，批次 %v 被接受", iter, injected, batch)
			}
			switch injected {
			case ErrDuplicateID:
				if !IsDuplicateID(err) {
					t.Fatalf("iter %d: 期望重复插入错误，实际 %v (batch=%v)", iter, err, batch)
				}
			case ErrRowNotFound:
				if !IsRowNotFound(err) {
					t.Fatalf("iter %d: 期望行不存在错误，实际 %v (batch=%v)", iter, err, batch)
				}
			case ErrEmptyKey:
				if !IsEmptyKey(err) {
					t.Fatalf("iter %d: 期望空键错误，实际 %v (batch=%v)", iter, err, batch)
				}
			}
			// 整批不生效。
			if got := normalize(m.View()); !reflect.DeepEqual(got, viewBefore) {
				t.Fatalf("iter %d: 拒绝后视图改变", iter)
			}
			if !reflect.DeepEqual(m.Log(), logBefore) {
				t.Fatalf("iter %d: 拒绝后日志改变", iter)
			}
			continue
		}

		if err != nil {
			t.Fatalf("iter %d: 合法批次被拒: %v (batch=%v)", iter, err, batch)
		}
		// 提交影子状态。
		shadowL, shadowR = simL, simR
		if got := normalize(m.View()); !reflect.DeepEqual(got, normalize(shadowView())) {
			t.Fatalf("iter %d: 视图与批量重算不符\ngot=%v\nwant=%v\nbatch=%v", iter, got, shadowView(), batch)
		}
		if err := m.Check(); err != nil {
			t.Fatalf("iter %d: 自检失败: %v", iter, err)
		}
	}
	t.Logf("判定: %d 个随机批次后，增量视图始终等于朴素重算，日志可重放，非法批整批回滚", iterations)
}

func pick(rng *rand.Rand, a, b byte) byte {
	if rng.Intn(2) == 0 {
		return a
	}
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
