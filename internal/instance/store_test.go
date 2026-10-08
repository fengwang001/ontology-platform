package instance

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// tracef 统一打印“输入 → 实际输出 → 版本与时间依据”，满足可审计要求。
func tracef(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf("TRACE %s", fmt.Sprintf(format, args...))
}

func mustWrite(t *testing.T, s *Store, req WriteRequest) Version {
	t.Helper()
	v, err := s.Write(req)
	tracef(t, "WRITE in={type:%q pk:%q biz:%d base:%d del:%v payload:%q} -> out={sys:%d err:%v}; 依据: 提交后最新指针=v%d 全局时钟=%d",
		req.ObjectType, req.PrimaryKey, req.BizStart, req.Base, req.Deleted, req.Payload, v.SysVersion, err, v.SysVersion, s.Clock())
	if err != nil {
		t.Fatalf("unexpected write error: %v", err)
	}
	return v
}

func expectReject(t *testing.T, s *Store, req WriteRequest, want error) {
	t.Helper()
	clockBefore := s.Clock()
	v, err := s.Write(req)
	tracef(t, "WRITE(invalid) in={pk:%q biz:%d base:%d del:%v} -> out={sys:%d err:%v}; 依据: 期望错误=%v 时钟前后=(%d,%d)",
		req.PrimaryKey, req.BizStart, req.Base, req.Deleted, v.SysVersion, err, want, clockBefore, s.Clock())
	if !errors.Is(err, want) {
		t.Fatalf("want %v, got %v", want, err)
	}
	if s.Clock() != clockBefore {
		t.Fatalf("rejected write consumed system version: %d -> %d", clockBefore, s.Clock())
	}
}

func checkState(t *testing.T, s *Store, objType, pk string, sys, biz int64, wantKind StateKind, wantVer int64) {
	t.Helper()
	st, err := s.GetAt(objType, pk, sys, biz)
	if err != nil {
		t.Fatal(err)
	}
	ver := int64(0)
	if st.Kind != StateUnknown {
		ver = st.Version.SysVersion
	}
	tracef(t, "QUERY in={type:%q pk:%q sys:%d biz:%d} -> out={kind:%d sysVer:%d payload:%q del:%v}; 依据: 期望 kind=%d 版本=v%d",
		objType, pk, sys, biz, st.Kind, ver, st.Version.Payload, st.Version.Deleted, wantKind, wantVer)
	if st.Kind != wantKind || ver != wantVer {
		t.Fatalf("at sys=%d biz=%d got (kind=%d,v%d) want (kind=%d,v%d)",
			sys, biz, st.Kind, ver, wantKind, wantVer)
	}
}

// 场景：删除后再次写入（复活），删除前/删除中/复活后三段历史必须可分别还原。
func TestDeleteResurrectThreePhases(t *testing.T) {
	s := New()
	mustWrite(t, s, WriteRequest{ObjectType: "Person", PrimaryKey: "p1", BizStart: 10, Payload: "alice-v1"})          // s1
	mustWrite(t, s, WriteRequest{ObjectType: "Person", PrimaryKey: "p1", BizStart: 30, Base: 1, Deleted: true})       // s2 删除
	mustWrite(t, s, WriteRequest{ObjectType: "Person", PrimaryKey: "p1", BizStart: 50, Base: 2, Payload: "alice-v2"}) // s3 复活

	// 在“当前系统时间 s=3”下沿业务时间切片，三段必须分明。
	checkState(t, s, "Person", "p1", 3, 9, StateUnknown, 0) // 早于最早业务起点
	checkState(t, s, "Person", "p1", 3, 10, StatePresent, 1)
	checkState(t, s, "Person", "p1", 3, 29, StatePresent, 1)
	checkState(t, s, "Person", "p1", 3, 30, StateAbsent, 2)
	checkState(t, s, "Person", "p1", 3, 49, StateAbsent, 2)
	checkState(t, s, "Person", "p1", 3, 50, StatePresent, 3)
	checkState(t, s, "Person", "p1", 3, 1<<62, StatePresent, 3)

	// 系统时间回溯：s=1 时只有存活；s=2 时删除区间已出现、复活尚未发生。
	// sys=1 时起点 10 的区间终点仍开放，覆盖 biz=50；
	// sys=2 的删除把 [30,50) 纳入删除区间；sys=3 复活在 50 生效。
	checkState(t, s, "Person", "p1", 1, 50, StatePresent, 1)
	checkState(t, s, "Person", "p1", 2, 50, StateAbsent, 2)
	checkState(t, s, "Person", "p1", 2, 10, StatePresent, 1)
	checkState(t, s, "Person", "p1", 3, 50, StatePresent, 3)

	// 历史时间轴重建。
	segs, ok := s.HistoryAsOf("Person", "p1", 3)
	if !ok || len(segs) != 3 {
		t.Fatalf("history ok=%v segs=%+v", ok, segs)
	}
	tracef(t, "HISTORY sys=3 -> %+v", segs)
	if segs[0].State.Kind != StatePresent || segs[1].State.Kind != StateAbsent || segs[2].State.Kind != StatePresent {
		t.Fatalf("three phases not separable: %+v", segs)
	}
}

// 场景：业务时间起点相同、系统时间更晚者覆盖，但两条记录都保留且可回溯。
func TestSameBizStartOverride(t *testing.T) {
	s := New()
	mustWrite(t, s, WriteRequest{ObjectType: "T", PrimaryKey: "k", BizStart: 100, Payload: "a"})          // s1
	mustWrite(t, s, WriteRequest{ObjectType: "T", PrimaryKey: "k", BizStart: 100, Base: 1, Payload: "b"}) // s2 覆盖

	checkState(t, s, "T", "k", 1, 100, StatePresent, 1) // 回溯仍见旧值
	checkState(t, s, "T", "k", 2, 100, StatePresent, 2) // 当前见新值

	// 两条物理记录都保留。
	if latest, ok := s.Latest("T", "k"); !ok || latest.SysVersion != 2 || latest.Payload != "b" {
		t.Fatalf("latest wrong: %+v", latest)
	}
	c := s.chains[key{objType: "T", pk: "k"}]
	if len(c.commits) != 2 || c.commits[0].Payload != "a" {
		t.Fatalf("overwritten record must be retained, got %+v", c.commits)
	}
}

// 场景：查询时点早于任何记录 / 晚于全部记录两种边界，与“从未写入”区分。
func TestQueryBoundaries(t *testing.T) {
	s := New()
	mustWrite(t, s, WriteRequest{ObjectType: "T", PrimaryKey: "k", BizStart: 100, Payload: "a"})

	// 从未写入的主键：任何时点都是 Unknown。
	checkState(t, s, "T", "k", 0, 0, StateUnknown, 0)
	if st, _ := s.GetAt("T", "ghost", 999, 999); st.Kind != StateUnknown {
		t.Fatalf("never-written key must be Unknown, got %d", st.Kind)
	}
	// 系统时间早于首版本：与从未写入同标记（此时确实还看不到任何东西）。
	checkState(t, s, "T", "k", 0, 100, StateUnknown, 0)
	// 业务时间早于最早起点：系统时间再晚也是 Unknown。
	checkState(t, s, "T", "k", 999, 99, StateUnknown, 0)
	// 晚于全部记录：开放区间持续生效。
	checkState(t, s, "T", "k", 999, 100, StatePresent, 1)
	checkState(t, s, "T", "k", 999, 1<<62, StatePresent, 1)
}

// 场景：拒绝次序——参数非法先于凭证冲突，凭证冲突先于可追溯边界。
func TestRejectionOrder(t *testing.T) {
	s := New()
	mustWrite(t, s, WriteRequest{ObjectType: "T", PrimaryKey: "k", BizStart: 100, Payload: "a"}) // s1

	// 主键为空：即便凭证也错、biz 也越界，只报参数非法。
	expectReject(t, s, WriteRequest{ObjectType: "T", PrimaryKey: "", BizStart: -1, Base: 99}, ErrInvalidArgument)
	// 主键合法、biz 合法、凭证错：报冲突（不报边界）。
	expectReject(t, s, WriteRequest{ObjectType: "T", PrimaryKey: "k", BizStart: 50, Base: 99, Payload: "x"}, ErrConflict)
	// 凭证合法（base=1）但 biz 早于最早边界 100：报边界错误。
	expectReject(t, s, WriteRequest{ObjectType: "T", PrimaryKey: "k", BizStart: 99, Base: 1, Payload: "x"}, ErrBeforeEarliestBiz)
	// 起点等于边界允许（覆盖语义）。
	v := mustWrite(t, s, WriteRequest{ObjectType: "T", PrimaryKey: "k", BizStart: 100, Base: 1, Payload: "b"})
	if v.SysVersion != 2 {
		t.Fatalf("expected s2, got %d", v.SysVersion)
	}
}

// 场景：多个 goroutine 持同一凭证竞争，只允许一个成功，其余冲突，
// 且效果等价于某种全局串行顺序（无丢版本、无版本号空洞）。
func TestOptimisticConcurrencyRace(t *testing.T) {
	s := New()
	mustWrite(t, s, WriteRequest{ObjectType: "T", PrimaryKey: "k", BizStart: 0, Payload: "init"}) // s1

	const n = 32
	var wg sync.WaitGroup
	var winners int64
	var mu sync.Mutex
	wg.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer wg.Done()
			v, err := s.Write(WriteRequest{
				ObjectType: "T", PrimaryKey: "k",
				BizStart: int64(10 + i), Base: 1, Payload: fmt.Sprintf("p%d", i),
			})
			mu.Lock()
			defer mu.Unlock()
			tracef(t, "RACE writer=%d -> sys=%d err=%v", i, v.SysVersion, err)
			if err == nil {
				winners++
			} else if !errors.Is(err, ErrConflict) {
				t.Errorf("unexpected err: %v", err)
			}
		}()
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("exactly one winner expected, got %d", winners)
	}
	latest, _ := s.Latest("T", "k")
	if latest.SysVersion != 2 {
		t.Fatalf("exactly one new version expected, latest=%d", latest.SysVersion)
	}
	if s.Clock() != 2 {
		t.Fatalf("clock must have no holes, got %d", s.Clock())
	}

	// 链式串行：失败者带新凭证重试，最终全部成功且顺序等价于串行。
	for i := 0; i < 4; i++ {
		cur, _ := s.Latest("T", "k")
		mustWrite(t, s, WriteRequest{
			ObjectType: "T", PrimaryKey: "k",
			BizStart: int64(100 + i), Base: cur.SysVersion, Payload: fmt.Sprintf("q%d", i),
		})
	}
	if s.Clock() != 6 {
		t.Fatalf("clock after serial chain = %d, want 6", s.Clock())
	}
}

// 场景：确定性重放——同一组操作在新存储上重放，版本链逐字段一致。
func TestDeterministicReplay(t *testing.T) {
	type op struct {
		biz     int64
		base    int64
		deleted bool
		payload string
		ok      bool
	}
	ops := []op{
		{10, 0, false, "a", true},
		{20, 1, false, "b", true},
		{20, 1, false, "stale", false}, // 凭证陈旧，必失败
		{30, 2, true, "", true},
		{5, 3, false, "early", false}, // 早于边界，必失败
		{40, 3, false, "c", true},
	}
	run := func() []Version {
		s := New()
		var got []Version
		for _, o := range ops {
			v, err := s.Write(WriteRequest{
				ObjectType: "T", PrimaryKey: "k", BizStart: o.biz, Base: o.base,
				Deleted: o.deleted, Payload: o.payload,
			})
			if (err == nil) != o.ok {
				t.Fatalf("op %+v err=%v", o, err)
			}
			if err == nil {
				got = append(got, v)
			}
		}
		return got
	}
	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatal("replay length differs")
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay mismatch at %d: %+v vs %+v", i, first[i], second[i])
		}
	}
}
