package backup

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// 本文件包含一个独立编写的朴素模型，用于与 Service 做随机操作序列对照。
// 朴素模型刻意使用最直接（但低效）的实现：每个备份单独沿链上溯判定
// 可恢复性、逐周期全量扫描选取代表、逐备份沿链标记祖先，以便让
// “正确性”一目了然，把性能优化全部留给被测实现。

type nrec struct {
	id        string
	parent    string
	size      int64
	createdAt int64
	corrupted bool
	held      bool
}

type naive struct {
	recs map[string]*nrec
	pol  Policy
	last int64
}

func newNaive() *naive { return &naive{recs: make(map[string]*nrec)} }

func (n *naive) checkTime(now int64) *Error {
	if now < 0 || now > MaxTime {
		return &Error{ErrInvalidArgument, "时刻越界"}
	}
	if now < n.last {
		return &Error{ErrClockRollback, "时钟回退"}
	}
	return nil
}

func (n *naive) register(now int64, id string, kind Kind, parent string, size int64) error {
	if id == "" || (kind != Full && kind != Incremental) ||
		(kind == Full && parent != "") || (kind == Incremental && parent == "") ||
		size < 0 || size > MaxSize {
		return &Error{ErrInvalidArgument, "参数非法"}
	}
	if err := n.checkTime(now); err != nil {
		return err
	}
	if _, ok := n.recs[id]; ok {
		return &Error{ErrDuplicateID, "标识重复"}
	}
	var pr *nrec
	if kind == Incremental {
		pr = n.recs[parent]
		if pr == nil {
			return &Error{ErrParentNotFound, "父备份不存在"}
		}
	}
	if pr != nil && now < pr.createdAt {
		return &Error{ErrTimeOrder, "子早于父"}
	}
	n.recs[id] = &nrec{id: id, parent: parent, size: size, createdAt: now}
	n.last = now
	return nil
}

func (n *naive) lookup(now int64, id string) (*nrec, error) {
	if id == "" {
		return nil, &Error{ErrInvalidArgument, "空标识"}
	}
	if err := n.checkTime(now); err != nil {
		return nil, err
	}
	r := n.recs[id]
	if r == nil {
		return nil, &Error{ErrBackupNotFound, "备份不存在"}
	}
	return r, nil
}

func (n *naive) markCorrupted(now int64, id string) error {
	r, err := n.lookup(now, id)
	if err != nil {
		return err
	}
	r.corrupted = true
	n.last = now
	return nil
}

func (n *naive) setLegalHold(now int64, id string) error {
	r, err := n.lookup(now, id)
	if err != nil {
		return err
	}
	r.held = true
	n.last = now
	return nil
}

func (n *naive) releaseLegalHold(now int64, id string) error {
	r, err := n.lookup(now, id)
	if err != nil {
		return err
	}
	r.held = false
	n.last = now
	return nil
}

func (n *naive) setPolicy(now int64, p Policy) error {
	if err := n.checkTime(now); err != nil {
		return err
	}
	if !validCount(p.Daily) || !validCount(p.Weekly) || !validCount(p.Monthly) {
		return &Error{ErrRetentionLimit, "层数量越界"}
	}
	n.pol = p
	n.last = now
	return nil
}

// recoverable 递归沿父链上溯：自身及全部祖先均未损坏才可恢复。
func (n *naive) recoverable(id string) bool {
	for cur := id; ; {
		r := n.recs[cur]
		if r.corrupted {
			return false
		}
		if r.parent == "" {
			return true
		}
		cur = r.parent
	}
}

// plan 朴素实现：逐层逐周期全量扫描。
func (n *naive) plan(now int64) (Plan, error) {
	if err := n.checkTime(now); err != nil {
		return Plan{}, err
	}
	n.last = now
	return n.computePlan(now), nil
}

func (n *naive) computePlan(now int64) Plan {
	reasons := make(map[string]Reasons, len(n.recs))
	layers := []layerSpec{
		{dayIndex, n.pol.Daily, ReasonDaily},
		{weekIndex, n.pol.Weekly, ReasonWeekly},
		{monthIndex, n.pol.Monthly, ReasonMonthly},
	}
	// 直接保留：每层在最近 N 个周期内，每个有可恢复备份的周期选最优者。
	for _, layer := range layers {
		if layer.n <= 0 {
			continue
		}
		c := layer.index(now)
		for p := c - int64(layer.n) + 1; p <= c; p++ {
			best := ""
			for id, r := range n.recs {
				if layer.index(r.createdAt) != p || !n.recoverable(id) {
					continue
				}
				if best == "" || beatsNaive(r, n.recs[best]) {
					best = id
				}
			}
			if best != "" {
				reasons[best] |= layer.bit
			}
		}
	}
	// 依赖保护：被直接保留备份的全部祖先。
	for id, rs := range reasons {
		if rs.Direct() == 0 {
			continue
		}
		for cur := n.recs[id].parent; cur != ""; cur = n.recs[cur].parent {
			reasons[cur] |= ReasonDependency
		}
	}
	// 法律保留：被标记备份及其全部祖先，不受可恢复性限制。
	for id, r := range n.recs {
		if !r.held {
			continue
		}
		reasons[id] |= ReasonLegalHold
		for cur := r.parent; cur != ""; cur = n.recs[cur].parent {
			reasons[cur] |= ReasonLegalHold
		}
	}
	// 汇总并按（创建时刻，标识）排序。
	var p Plan
	for id, r := range n.recs {
		if reasons[id] == 0 {
			p.Deletable = append(p.Deletable, id)
		} else {
			p.Retained = append(p.Retained, RetainedEntry{ID: id, Reasons: reasons[id]})
		}
		_ = r
	}
	sort.Slice(p.Deletable, func(i, j int) bool {
		a, b := n.recs[p.Deletable[i]], n.recs[p.Deletable[j]]
		if a.createdAt != b.createdAt {
			return a.createdAt < b.createdAt
		}
		return a.id < b.id
	})
	sort.Slice(p.Retained, func(i, j int) bool {
		a, b := n.recs[p.Retained[i].ID], n.recs[p.Retained[j].ID]
		if a.createdAt != b.createdAt {
			return a.createdAt < b.createdAt
		}
		return a.id < b.id
	})
	return p
}

func beatsNaive(a, b *nrec) bool {
	if a.createdAt != b.createdAt {
		return a.createdAt > b.createdAt
	}
	return a.id > b.id
}

func (n *naive) execute(now int64) (Plan, error) {
	if err := n.checkTime(now); err != nil {
		return Plan{}, err
	}
	p := n.computePlan(now)
	for _, id := range p.Deletable {
		delete(n.recs, id)
	}
	n.last = now
	return p, nil
}

// kindOf 提取错误类别；无错误返回 0。
func kindOf(err error) ErrorKind {
	if err == nil {
		return 0
	}
	if be, ok := err.(*Error); ok {
		return be.Kind
	}
	return -1
}

// TestDifferentialRandom 用同一随机操作序列同时驱动 Service 与朴素模型，
// 逐条比对错误类别与清理计划（含保留原因），并打印每条操作的
// 输入、输出与判定依据。相同种子重放得到完全相同的序列。
func TestDifferentialRandom(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 2024, 99991} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			svc := NewService()
			nv := newNaive()
			now := int64(0)
			nextID := 0
			// 小标识池制造重复与父备份命中。
			pool := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
			pickID := func() string {
				if rng.Intn(4) == 0 {
					nextID++
					return fmt.Sprintf("n%d", nextID)
				}
				return pool[rng.Intn(len(pool))]
			}
			pickParent := func() string {
				if rng.Intn(5) == 0 {
					return "ghost" // 触发父备份不存在
				}
				var ids []string
				for id := range nv.recs {
					ids = append(ids, id)
				}
				if len(ids) == 0 || rng.Intn(3) == 0 {
					return pool[rng.Intn(len(pool))]
				}
				return ids[rng.Intn(len(ids))]
			}
			for i := 0; i < 4000; i++ {
				// 时钟：多数前进，偶尔原地或回退。
				switch rng.Intn(20) {
				case 0:
					now -= rng.Int63n(3) // 可能触发时钟回退
				case 1:
					// 原地不动
				default:
					now += rng.Int63n(200000)
				}
				if now < 0 {
					now = 0
				}
				var in string
				var svcErr, nvErr error
				var svcPlan, nvPlan Plan
				switch rng.Intn(8) {
				case 0:
					id := pickID()
					size := rng.Int63n(MaxSize + 2)
					in = fmt.Sprintf("register(now=%d id=%q full size=%d)", now, id, size)
					svcErr = svc.RegisterBackup(now, id, Full, "", size)
					nvErr = nv.register(now, id, Full, "", size)
				case 1:
					id, parent := pickID(), pickParent()
					in = fmt.Sprintf("register(now=%d id=%q incr parent=%q)", now, id, parent)
					svcErr = svc.RegisterBackup(now, id, Incremental, parent, 1)
					nvErr = nv.register(now, id, Incremental, parent, 1)
				case 2:
					id := pickID()
					in = fmt.Sprintf("markCorrupted(now=%d id=%q)", now, id)
					svcErr = svc.MarkCorrupted(now, id)
					nvErr = nv.markCorrupted(now, id)
				case 3:
					id := pickID()
					in = fmt.Sprintf("setLegalHold(now=%d id=%q)", now, id)
					svcErr = svc.SetLegalHold(now, id)
					nvErr = nv.setLegalHold(now, id)
				case 4:
					id := pickID()
					in = fmt.Sprintf("releaseLegalHold(now=%d id=%q)", now, id)
					svcErr = svc.ReleaseLegalHold(now, id)
					nvErr = nv.releaseLegalHold(now, id)
				case 5:
					counts := []int{-1, 0, 1, 2, 3, MaxRetentionCount, MaxRetentionCount + 1}
					p := Policy{
						Daily:   counts[rng.Intn(len(counts))],
						Weekly:  counts[rng.Intn(len(counts))],
						Monthly: counts[rng.Intn(len(counts))],
					}
					in = fmt.Sprintf("setPolicy(now=%d %+v)", now, p)
					svcErr = svc.SetPolicy(now, p)
					nvErr = nv.setPolicy(now, p)
				case 6:
					in = fmt.Sprintf("plan(now=%d)", now)
					svcPlan, svcErr = svc.PlanCleanup(now)
					nvPlan, nvErr = nv.plan(now)
				case 7:
					in = fmt.Sprintf("execute(now=%d)", now)
					svcPlan, svcErr = svc.ExecuteCleanup(now)
					nvPlan, nvErr = nv.execute(now)
				}
				// 判定依据：错误类别必须一致；成功时计划必须完全一致。
				verdict := "ok"
				if kindOf(svcErr) != kindOf(nvErr) {
					verdict = fmt.Sprintf("MISMATCH err svc=%v naive=%v", svcErr, nvErr)
				} else if svcErr == nil && !reflect.DeepEqual(svcPlan, nvPlan) {
					verdict = fmt.Sprintf("MISMATCH plan svc=%+v naive=%+v", svcPlan, nvPlan)
				}
				t.Logf("op %d: %s => svc=%v naive=%v deletable=%d retained=%d [%s]",
					i, in, kindOf(svcErr), kindOf(nvErr), len(svcPlan.Deletable), len(svcPlan.Retained), verdict)
				if verdict != "ok" {
					t.Fatalf("对照失败: %s", verdict)
				}
			}
		})
	}
}
