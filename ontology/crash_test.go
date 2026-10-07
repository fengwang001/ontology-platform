package ontology

import (
	"fmt"
	"testing"
)

// crashOnce 返回一个只在第一个匹配切分点触发一次的崩溃注入条件。
func crashOnce(point CutPoint) FaultHook {
	fired := false
	return func(p CutPoint, _ uint64, _ string) bool {
		if !fired && p == point {
			fired = true
			return true
		}
		return false
	}
}

// expectCrash 运行 f 并断言确实发生了崩溃模拟。
func expectCrash(t *testing.T, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("expected crash panic, got none")
		}
		if _, ok := r.(crashSignal); !ok {
			t.Fatalf("expected crashSignal, got %v", r)
		}
	}()
	f()
}

// TestCrashRecoveryAtEveryCutPoint 在每个切分点注入崩溃，
// 验证恢复后属性值与索引条目要么完整生效、要么完整回退。
func TestCrashRecoveryAtEveryCutPoint(t *testing.T) {
	points := []CutPoint{CutAfterWALBegin, CutAfterValue, CutAfterIndex, CutAfterAllIndexes, CutAfterCommit}
	for _, point := range points {
		t.Run(point.String(), func(t *testing.T) {
			log := NewBufferLogger()
			s, faults, exact, prefix := newTestStore(log)
			s.AddInstance("A")
			mustWrite(t, s, "A", "status", Of("before"))
			clockBefore := s.Clock()

			faults.CrashWhen(crashOnce(point))
			expectCrash(t, func() { _ = s.Write("A", "status", Of("after")) })

			decisions := s.Recover()
			if len(decisions) != 1 {
				t.Fatalf("decisions=%v, want 1", decisions)
			}
			d := decisions[0]
			t.Logf("point=%s decision=%s reason=%s", point, d.Decision, d.Reason)

			committed := point == CutAfterCommit
			wantVal := Of("before")
			if committed {
				wantVal = Of("after")
				if d.Decision != "rolled-forward" {
					t.Fatalf("committed txn must roll forward, got %s", d.Decision)
				}
			} else if d.Decision != "rolled-back" {
				t.Fatalf("uncommitted txn must roll back, got %s", d.Decision)
			}
			v, err := s.Get("A", "status")
			if err != nil || v != wantVal {
				t.Fatalf("value=%+v err=%v, want %+v", v, err, wantVal)
			}
			// 属性值与两个索引结构必须互相对应，不允许只回退一边。
			assertQuery(t, s, "status", wantVal, "A")
			other := Of("before")
			if wantVal == Of("before") {
				other = Of("after")
			}
			assertQuery(t, s, "status", other)
			if exact.Size() != 1 || prefix.Size() != 1 {
				t.Fatalf("index sizes %d/%d, want 1 each", exact.Size(), prefix.Size())
			}
			// 时钟：回滚则不变，前滚则恢复到提交时刻的值。
			if !committed && s.Clock() != clockBefore {
				t.Fatalf("rolled-back txn advanced clock: %d -> %d", clockBefore, s.Clock())
			}
			if committed && s.Clock() != clockBefore+1 {
				t.Fatalf("rolled-forward txn clock=%d, want %d", s.Clock(), clockBefore+1)
			}
			// 恢复后系统继续可用。
			mustWrite(t, s, "A", "status", Of("next"))
			assertQuery(t, s, "status", Of("next"), "A")
		})
	}
}

// TestCrashRecoveryDelete 在删除处理单元的切分点注入崩溃：
// 恢复后不允许实例已删而索引残留、或索引已清而实例可见。
func TestCrashRecoveryDelete(t *testing.T) {
	points := []CutPoint{CutAfterWALBegin, CutAfterValue, CutAfterIndex, CutAfterAllIndexes, CutAfterCommit}
	for _, point := range points {
		t.Run(point.String(), func(t *testing.T) {
			s, faults, exact, prefix := newTestStore(NewBufferLogger())
			s.AddInstance("A")
			mustWrite(t, s, "A", "status", Of("active"))

			faults.CrashWhen(crashOnce(point))
			expectCrash(t, func() { _ = s.DeleteInstance("A") })

			decisions := s.Recover()
			if len(decisions) != 1 || decisions[0].Kind != "delete" {
				t.Fatalf("decisions=%+v", decisions)
			}
			committed := point == CutAfterCommit
			if committed {
				// 删除已提交：实例不可见且索引无残留。
				if _, err := s.Get("A", "status"); err == nil {
					t.Fatalf("instance still visible after committed delete")
				}
				assertQuery(t, s, "status", Of("active"))
				assertAbsentQuery(t, s, "status")
				if exact.Size() != 0 || prefix.Size() != 0 {
					t.Fatalf("residual entries %d/%d", exact.Size(), prefix.Size())
				}
			} else {
				// 删除未提交：实例与索引条目完整恢复。
				v, err := s.Get("A", "status")
				if err != nil || v != Of("active") {
					t.Fatalf("value=%+v err=%v", v, err)
				}
				assertQuery(t, s, "status", Of("active"), "A")
				if exact.Size() != 1 || prefix.Size() != 1 {
					t.Fatalf("index sizes %d/%d", exact.Size(), prefix.Size())
				}
			}
		})
	}
}

// TestCrashRecoveryBatch 批量写入中途崩溃：恢复后所有实例一致地
// 全部生效或全部回退，不允许部分生效。
func TestCrashRecoveryBatch(t *testing.T) {
	points := []CutPoint{CutAfterWALBegin, CutAfterValue, CutAfterIndex, CutAfterAllIndexes, CutAfterCommit}
	for _, point := range points {
		t.Run(point.String(), func(t *testing.T) {
			s, faults, _, _ := newTestStore(NewBufferLogger())
			for _, id := range []string{"A", "B", "C"} {
				s.AddInstance(id)
				mustWrite(t, s, id, "status", Of("init"))
			}
			faults.CrashWhen(crashOnce(point))
			expectCrash(t, func() {
				s.BatchWrite("status", []InstanceWrite{
					{InstanceID: "A", Value: Of("x")},
					{InstanceID: "B", Value: Of("y")},
					{InstanceID: "C", Value: Of("z")},
				})
			})
			decisions := s.Recover()
			if len(decisions) != 1 || decisions[0].Kind != "batch" {
				t.Fatalf("decisions=%+v", decisions)
			}
			committed := point == CutAfterCommit
			want := map[string]Value{"A": Of("init"), "B": Of("init"), "C": Of("init")}
			if committed {
				want = map[string]Value{"A": Of("x"), "B": Of("y"), "C": Of("z")}
			}
			groups := map[Value][]string{}
			for id, w := range want {
				v, err := s.Get(id, "status")
				if err != nil || v != w {
					t.Fatalf("%s value=%+v err=%v, want %+v", id, v, err, w)
				}
				groups[w] = append(groups[w], id)
			}
			for w, ids := range groups {
				assertQuery(t, s, "status", w, sortedIDs(ids)...)
			}
		})
	}
}

// TestRecoverIsIdempotent 恢复可以重复执行，结果不变。
func TestRecoverIsIdempotent(t *testing.T) {
	s, faults, _, _ := newTestStore(NewBufferLogger())
	s.AddInstance("A")
	mustWrite(t, s, "A", "status", Of("v1"))
	faults.CrashWhen(crashOnce(CutAfterValue))
	expectCrash(t, func() { _ = s.Write("A", "status", Of("v2")) })
	d1 := s.Recover()
	d2 := s.Recover()
	if len(d1) != 1 || len(d2) != 0 {
		t.Fatalf("d1=%v d2=%v", d1, d2)
	}
	assertQuery(t, s, "status", Of("v1"), "A")
}

// TestCrashAtEveryIndexBoundary 多索引结构下逐个索引边界注入崩溃。
func TestCrashAtEveryIndexBoundary(t *testing.T) {
	for i := 0; i < 2; i++ {
		t.Run(fmt.Sprintf("index-%d", i), func(t *testing.T) {
			s, faults, exact, prefix := newTestStore(NewBufferLogger())
			s.AddInstance("A")
			mustWrite(t, s, "A", "status", Of("before"))
			count := 0
			faults.CrashWhen(func(p CutPoint, _ uint64, _ string) bool {
				if p != CutAfterIndex {
					return false
				}
				c := count
				count++
				return c == i
			})
			expectCrash(t, func() { _ = s.Write("A", "status", Of("after")) })
			s.Recover()
			v, _ := s.Get("A", "status")
			if v != Of("before") {
				t.Fatalf("value=%+v, want before", v)
			}
			assertQuery(t, s, "status", Of("before"), "A")
			if exact.Size() != 1 || prefix.Size() != 1 {
				t.Fatalf("index sizes %d/%d", exact.Size(), prefix.Size())
			}
		})
	}
}
