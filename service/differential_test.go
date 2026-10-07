package service

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/store"
)

// naiveModel 是独立实现的朴素参照：保留全部版本，查询时线性扫描。
// 它与被测系统共享同一份语义规约，但实现路径完全不同（无索引、无 treap），
// 用于在大量随机操作下逐条对照结果。
type naiveModel struct {
	chains map[string][]store.Version
	minBiz map[string]int64
	clock  int64
}

func newNaiveModel(clockStart int64) *naiveModel {
	return &naiveModel{chains: map[string][]store.Version{}, minBiz: map[string]int64{}, clock: clockStart}
}

func naiveKey(objectType, pk string) string { return objectType + "/" + pk }

func (m *naiveModel) write(objectType, pk string, token, bizStart int64, kind store.OpKind, payload string) (store.Version, store.ErrKind, bool) {
	if objectType == "" || pk == "" || bizStart < 0 || bizStart > store.MaxBizStart || token < 0 {
		return store.Version{}, store.ErrInvalidArgument, false
	}
	key := naiveKey(objectType, pk)
	vs := m.chains[key]
	latest := int64(len(vs))
	if token != latest {
		return store.Version{}, store.ErrConcurrencyConflict, false
	}
	if latest > 0 && bizStart < m.minBiz[key] {
		return store.Version{}, store.ErrBizBoundary, false
	}
	sys := m.clock
	m.clock++
	v := store.Version{Seq: latest + 1, Sys: sys, BizStart: bizStart, Kind: kind, Payload: payload}
	m.chains[key] = append(vs, v)
	if latest == 0 || bizStart < m.minBiz[key] {
		m.minBiz[key] = bizStart
	}
	return v, 0, true
}

func (m *naiveModel) query(objectType, pk string, sysQ, bizQ int64) (store.Version, store.Status) {
	key := naiveKey(objectType, pk)
	vs, ok := m.chains[key]
	if !ok || len(vs) == 0 {
		return store.Version{}, store.StatusNeverWritten
	}
	var best *store.Version
	for i := range vs {
		v := &vs[i]
		if v.Sys > sysQ || v.BizStart > bizQ {
			continue
		}
		if best == nil || v.BizStart > best.BizStart ||
			(v.BizStart == best.BizStart && v.Sys > best.Sys) {
			best = v
		}
	}
	if best == nil {
		return store.Version{}, store.StatusNoVersionAtTime
	}
	if best.Kind == store.KindDelete {
		return *best, store.StatusDeleted
	}
	return *best, store.StatusFound
}

// TestDifferentialAgainstNaive 在大量随机写入/删除/双时态查询序列下，
// 将被测系统与朴素线性扫描模型逐条对照。每次操作打印输入、实际输出
// 与据以判定的版本及时间依据（go test -v 可见）。
func TestDifferentialAgainstNaive(t *testing.T) {
	const clockStart = int64(1000)
	st := store.New(store.NewFakeClock(clockStart))
	naive := newNaiveModel(clockStart)
	rng := rand.New(rand.NewSource(1650))

	types := []string{"Employee", "Asset"}
	pks := []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7", "p8"}

	tokenOf := func(ot, pk string) int64 { return st.LatestSeq(ot, pk) }

	const ops = 4000
	for i := 0; i < ops; i++ {
		ot := types[rng.Intn(len(types))]
		pk := pks[rng.Intn(len(pks))]
		switch r := rng.Intn(100); {
		case r < 50: // 普通写入
			latest := tokenOf(ot, pk)
			token := latest
			if rng.Intn(100) < 25 { // 25% 概率使用过期或未来凭证
				token = rng.Int63n(latest + 3)
			}
			biz := rng.Int63n(120)
			payload := fmt.Sprintf("payload-%d", i)
			input := fmt.Sprintf("Write(%s/%s, token=v%d, biz=%d, payload=%q)", ot, pk, token, biz, payload)
			gotV, gotErr := st.Write(ot, pk, token, biz, payload)
			wantV, wantKind, wantOK := naive.write(ot, pk, token, biz, store.KindPut, payload)
			if !compareWrite(t, i, input, gotV, gotErr, wantV, wantKind, wantOK) {
				return
			}
		case r < 70: // 逻辑删除
			latest := tokenOf(ot, pk)
			token := latest
			if rng.Intn(100) < 25 {
				token = rng.Int63n(latest + 3)
			}
			biz := rng.Int63n(120)
			input := fmt.Sprintf("Delete(%s/%s, token=v%d, biz=%d)", ot, pk, token, biz)
			gotV, gotErr := st.Delete(ot, pk, token, biz)
			wantV, wantKind, wantOK := naive.write(ot, pk, token, biz, store.KindDelete, "")
			if !compareWrite(t, i, input, gotV, gotErr, wantV, wantKind, wantOK) {
				return
			}
		default: // 双时态查询
			sysQ := clockStart + rng.Int63n(int64(i)+2) - 1
			bizQ := rng.Int63n(130) - 5
			gotV, gotSt := st.Query(ot, pk, sysQ, bizQ)
			wantV, wantSt := naive.query(ot, pk, sysQ, bizQ)
			basis := queryBasis(wantSt, wantV)
			t.Logf("op#%d 输入=Query(%s/%s, sysQ=%d, bizQ=%d) 实际输出=(%s, %+v) 判定依据=%s",
				i, ot, pk, sysQ, bizQ, statusName(gotSt), gotV, basis)
			if gotSt != wantSt || gotV != wantV {
				t.Fatalf("op#%d 查询不一致: got (%v, %+v), want (%v, %+v)",
					i, gotSt, gotV, wantSt, wantV)
			}
		}
	}

	// 全量版本链一致性。
	for _, ot := range types {
		for _, pk := range pks {
			got := st.Versions(ot, pk)
			want := naive.chains[naiveKey(ot, pk)]
			if fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", want) {
				t.Fatalf("%s/%s 版本链不一致:\n got %+v\nwant %+v", ot, pk, got, want)
			}
		}
	}
	// 收尾全坐标扫描对照。
	for _, ot := range types {
		for _, pk := range pks {
			for sysQ := clockStart - 1; sysQ < clockStart+int64(ops)+2; sysQ += 97 {
				for bizQ := int64(-5); bizQ < 130; bizQ += 7 {
					gotV, gotSt := st.Query(ot, pk, sysQ, bizQ)
					wantV, wantSt := naive.query(ot, pk, sysQ, bizQ)
					if gotSt != wantSt || gotV != wantV {
						t.Fatalf("收尾对照 Query(%s/%s,%d,%d): got (%v,%+v), want (%v,%+v)",
							ot, pk, sysQ, bizQ, gotSt, gotV, wantSt, wantV)
					}
				}
			}
		}
	}
}

func statusName(s store.Status) string {
	switch s {
	case store.StatusFound:
		return "FOUND"
	case store.StatusDeleted:
		return "DELETED"
	case store.StatusNoVersionAtTime:
		return "NO_VERSION_AT_TIME"
	default:
		return "NEVER_WRITTEN"
	}
}

func queryBasis(st store.Status, v store.Version) string {
	switch st {
	case store.StatusFound, store.StatusDeleted:
		return fmt.Sprintf("命中版本 v%d (sys=%d, bizStart=%d, kind=%d)，其为可见前缀内业务起点<=bizQ 的最大起点且系统时间最晚者",
			v.Seq, v.Sys, v.BizStart, v.Kind)
	case store.StatusNoVersionAtTime:
		return "主键已写入，但可见前缀内无业务起点<=bizQ 的版本（或 sysQ 早于首次提交）"
	default:
		return "主键从未写入"
	}
}

func compareWrite(t *testing.T, op int, input string, gotV store.Version, gotErr error,
	wantV store.Version, wantKind store.ErrKind, wantOK bool) bool {
	t.Helper()
	if (gotErr == nil) != wantOK {
		t.Fatalf("op#%d %s: 被测系统 ok=%v, 朴素模型 ok=%v", op, input, gotErr == nil, wantOK)
		return false
	}
	if gotErr != nil {
		re := gotErr.(*store.RejectError)
		t.Logf("op#%d 输入=%s 实际输出=拒绝(%s) 判定依据=拒绝次序命中第 %d 类原因",
			op, input, rejectName(re.Kind), int(re.Kind)+1)
		if re.Kind != wantKind {
			t.Fatalf("op#%d %s: 拒绝原因 got %v, want %v", op, input, re.Kind, wantKind)
			return false
		}
		return true
	}
	t.Logf("op#%d 输入=%s 实际输出=提交(v%d, sys=%d, bizStart=%d) 判定依据=凭证匹配且 bizStart 不早于最早可追溯边界",
		op, input, gotV.Seq, gotV.Sys, gotV.BizStart)
	if gotV != wantV {
		t.Fatalf("op#%d %s: 版本不一致 got %+v, want %+v", op, input, gotV, wantV)
		return false
	}
	return true
}

func rejectName(k store.ErrKind) string {
	switch k {
	case store.ErrInvalidArgument:
		return "INVALID_ARGUMENT"
	case store.ErrConcurrencyConflict:
		return "CONCURRENCY_CONFLICT"
	default:
		return "BIZ_BOUNDARY_VIOLATION"
	}
}

// TestQueryCostIndependentOfInstanceCount 证明查询开销与对象类型下
// 实例总数无关：同一主键的同一查询，在实例总数从 1 涨到 2 万后，
// 索引访问节点数完全不变。
func TestQueryCostIndependentOfInstanceCount(t *testing.T) {
	st := store.New(store.NewFakeClock(1))
	biz := int64(1000)
	for i := int64(0); i < 500; i++ {
		if _, err := st.Write("T", "target", i, biz+i, "x"); err != nil {
			t.Fatal(err)
		}
	}
	probe := func() int {
		st.Query("T", "target", 1<<62, biz+500)
		return st.QueryVisited("T", "target")
	}
	before := probe()
	// 注入 2 万个无关实例。
	for i := 0; i < 20000; i++ {
		pk := fmt.Sprintf("noise-%d", i)
		if _, err := st.Write("T", pk, 0, 10, "n"); err != nil {
			t.Fatal(err)
		}
	}
	after := probe()
	t.Logf("实例总数 1 -> 20001, 目标主键单次查询访问节点数 %d -> %d", before, after)
	if before != after {
		t.Fatalf("查询开销随实例总数变化: %d -> %d", before, after)
	}
}
