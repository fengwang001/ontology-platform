package whiteboard

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 随机对照测试：对同一随机操作序列，分别作用于 Board 与独立朴素模型，
// 逐步比较错误类别（含锁持有者与剩余秒数）、修订号、全序、名次与区间查询。
// 每个操作都打印输入、输出与判定依据（t.Log，-v 可见），失败时打印完整状态。

const (
	randomSequences = 1500 // 随机操作序列组数
	randomBaseSeed  = 20261008
)

var (
	randUsers = []string{"alice", "bob", "carol", "dora"}
	randElems = []string{"e0", "e1", "e2", "e3", "e4", "e5", "e6", "e7", "e8", "e9",
		"e10", "e11", "e12", "e13", "e14", "e15", "e16", "e17", "e18", "e19"}
	randGroups = []string{"g0", "g1", "g2", "g3"}
	randAllIDs = append(append([]string{}, randElems...), randGroups...)
)

// errDesc 描述一次操作的结果，用于比较与日志。
type errDesc struct {
	kind      int // -1 表示成功
	holder    string
	remaining int64
	msg       string
}

func describe(err error) errDesc {
	if err == nil {
		return errDesc{kind: -1}
	}
	werr, ok := err.(*Error)
	if !ok {
		return errDesc{kind: -2, msg: err.Error()}
	}
	return errDesc{kind: int(werr.Kind), holder: werr.Holder, remaining: werr.Remaining, msg: werr.Msg}
}

func (d errDesc) String() string {
	if d.kind == -1 {
		return "OK"
	}
	if d.kind == int(ErrLocked) {
		return fmt.Sprintf("REJECT kind=Locked holder=%s remaining=%d", d.holder, d.remaining)
	}
	return fmt.Sprintf("REJECT kind=%d", d.kind)
}

func sameErr(a, b errDesc) bool {
	return a.kind == b.kind && a.holder == b.holder && a.remaining == b.remaining
}

// equalOrder 比较两个序列，空切片与 nil 视为相等。
func equalOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// randNow 生成时间戳：通常单调推进，偶尔回退或越界。
func randNow(rng *rand.Rand, cur *int64) int64 {
	switch x := rng.Intn(100); {
	case x < 75:
		*cur += int64(rng.Intn(20))
	case x < 85:
		*cur += 0 // 原地：等于上次，允许
	case x < 95:
		*cur -= int64(rng.Intn(30)) // 回退
	default:
		if rng.Intn(2) == 0 {
			return -1 - int64(rng.Intn(5))
		}
		return MaxNow + 1 + int64(rng.Intn(5))
	}
	return *cur
}

func randUser(rng *rand.Rand) string {
	if rng.Intn(40) == 0 {
		return ""
	}
	return randUsers[rng.Intn(len(randUsers))]
}

func randElemID(rng *rand.Rand) string {
	if rng.Intn(50) == 0 {
		return "ghost" // 不存在的标识
	}
	return randElems[rng.Intn(len(randElems))]
}

func randAnyID(rng *rand.Rand) string {
	if rng.Intn(50) == 0 {
		return "ghost"
	}
	return randAllIDs[rng.Intn(len(randAllIDs))]
}

// runOneSequence 执行一条随机操作序列并逐步对照，返回是否一致。
func runOneSequence(t *testing.T, seed int64, nOps int) {
	rng := rand.New(rand.NewSource(seed))
	b := New()
	m := newModel()
	var clock int64

	fail := func(opIdx int, opDesc string, why string) {
		t.Fatalf("seq seed=%d op=%d %s\n判定依据: %s\nBoard order=%v rev=%d\nmodel order=%v rev=%d",
			seed, opIdx, opDesc, why, b.Order(), b.Revision(), m.order, m.rev)
	}

	check := func(opIdx int, opDesc string, be, me error) {
		bd, md := describe(be), describe(me)
		if !sameErr(bd, md) {
			fail(opIdx, opDesc, fmt.Sprintf("结果不一致: Board=%s model=%s", bd, md))
		}
		if b.Revision() != m.rev {
			fail(opIdx, opDesc, fmt.Sprintf("修订号不一致: Board=%d model=%d", b.Revision(), m.rev))
		}
		if !equalOrder(b.Order(), m.order) {
			fail(opIdx, opDesc, fmt.Sprintf("全序不一致: Board=%v model=%v", b.Order(), m.order))
		}
		if err := b.ol.validate(); err != nil {
			fail(opIdx, opDesc, fmt.Sprintf("树结构不变量破坏: %v", err))
		}
		t.Logf("seed=%d op=%d %s -> %s | rev=%d order=%v", seed, opIdx, opDesc, bd, b.Revision(), b.Order())
	}

	for i := 0; i < nOps; i++ {
		now := randNow(rng, &clock)
		switch rng.Intn(100) {
		case 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14: // Add 15%
			u, id := randUser(rng), randElemID(rng)
			check(i, fmt.Sprintf("Add(%q,%q,%d)", u, id, now), b.Add(u, id, now), m.add(u, id, now))
		case 15, 16, 17, 18, 19, 20, 21, 22: // Group 8%
			u, gid := randUser(rng), randGroups[rng.Intn(len(randGroups))]
			k := rng.Intn(6) // 0..5 名成员，覆盖非法成员数
			ids := make([]string, k)
			for j := range ids {
				ids[j] = randElemID(rng)
			}
			check(i, fmt.Sprintf("Group(%q,%q,%v,%d)", u, gid, ids, now), b.Group(u, gid, ids, now), m.group(u, gid, ids, now))
		case 23, 24, 25, 26, 27, 28: // Ungroup 6%
			u, gid := randUser(rng), randAnyID(rng)
			check(i, fmt.Sprintf("Ungroup(%q,%q,%d)", u, gid, now), b.Ungroup(u, gid, now), m.ungroup(u, gid, now))
		case 29, 30, 31, 32, 33, 34, 35, 36, 37, 38: // Remove 10%
			u, id := randUser(rng), randAnyID(rng)
			check(i, fmt.Sprintf("Remove(%q,%q,%d)", u, id, now), b.Remove(u, id, now), m.remove(u, id, now))
		case 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 49, 50, 51, 52, 53, 54, 55, 56, 57, 58: // Reorder 20%
			u, tgt, anc := randUser(rng), randAnyID(rng), randElemID(rng)
			side := Side(rng.Intn(3)) // 含非法 side
			baseRev := uint64(0)
			switch rng.Intn(4) {
			case 0:
				baseRev = uint64(rng.Intn(int(b.Revision()) + 3))
			case 1:
				baseRev = b.Revision()
			case 2:
				baseRev = 1 << 62
			default:
				baseRev = uint64(rng.Intn(3))
			}
			check(i, fmt.Sprintf("Reorder(%q,%q,%q,side=%d,base=%d,%d)", u, tgt, anc, side, baseRev, now),
				b.Reorder(u, tgt, anc, side, baseRev, now), m.reorder(u, tgt, anc, side, baseRev, now))
		case 59, 60, 61, 62, 63, 64, 65, 66, 67, 68, 69, 70: // Lock 12%
			u, id := randUser(rng), randAnyID(rng)
			ttl := int64(rng.Intn(3702)) // 含非法 ttl
			check(i, fmt.Sprintf("Lock(%q,%q,ttl=%d,%d)", u, id, ttl, now), b.Lock(u, id, ttl, now), m.lock(u, id, ttl, now))
		case 71, 72, 73, 74, 75, 76, 77, 78: // Unlock 8%
			u, id := randUser(rng), randAnyID(rng)
			check(i, fmt.Sprintf("Unlock(%q,%q,%d)", u, id, now), b.Unlock(u, id, now), m.unlock(u, id, now))
		case 79, 80, 81, 82, 83, 84: // Rank 6%
			id := randAnyID(rng)
			br, bok := b.Rank(id)
			mr := m.rankOf(id)
			mok := mr >= 0
			if bok != mok || (bok && br != mr) {
				fail(i, fmt.Sprintf("Rank(%q)", id), fmt.Sprintf("名次不一致: Board=%d,%v model=%d,%v", br, bok, mr, mok))
			}
			t.Logf("seed=%d op=%d Rank(%q) -> %d,%v", seed, i, id, br, bok)
		default: // Between 与 Order 15%
			n := len(m.order)
			if n == 0 {
				continue
			}
			lo, hi := rng.Intn(n+2)-1, rng.Intn(n+2)-1
			got, err := b.Between(lo, hi)
			if lo < 0 || hi < lo || hi >= n {
				if err == nil {
					fail(i, fmt.Sprintf("Between(%d,%d)", lo, hi), "非法区间未报错")
				}
				t.Logf("seed=%d op=%d Between(%d,%d) -> 非法区间（按约定拒绝）", seed, i, lo, hi)
				continue
			}
			if err != nil || !reflect.DeepEqual(got, m.order[lo:hi+1]) {
				fail(i, fmt.Sprintf("Between(%d,%d)", lo, hi), fmt.Sprintf("区间不一致: Board=%v,%v model=%v", got, err, m.order[lo:hi+1]))
			}
			t.Logf("seed=%d op=%d Between(%d,%d) -> %v", seed, i, lo, hi, got)
		}
	}
}

func TestRandomAgainstModel(t *testing.T) {
	t.Logf("随机对照: %d 组序列, baseSeed=%d（判定依据: 错误类别/持有者/剩余秒数、修订号、全序、名次、区间逐项相等）",
		randomSequences, randomBaseSeed)
	for seq := 0; seq < randomSequences; seq++ {
		seed := int64(randomBaseSeed + seq)
		rng := rand.New(rand.NewSource(seed))
		nOps := 60 + rng.Intn(140) // 60..199 个操作
		runOneSequence(t, seed, nOps)
	}
}
