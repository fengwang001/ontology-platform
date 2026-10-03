package cluster

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func TestNewValidation(t *testing.T) {
	bad := [][3]int{{0, 10, 10}, {101, 10, 10}, {1, 0, 10}, {1, 100001, 10}, {1, 10, 0}, {1, 10, 10001}}
	for _, p := range bad {
		if _, err := New(p[0], p[1], p[2]); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("New(%v) 应失败, got %v", p, err)
		}
	}
	if _, err := New(100, 100000, 10000); err != nil {
		t.Fatal(err)
	}
}

func TestIngestValidationAndTenantLimitOrder(t *testing.T) {
	m, _ := New(50, 10, 1)
	m.Ingest("t1", "a b")
	// 非法参数优先于租户上限。
	if _, err := m.Ingest("t2", "a  b"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("应先报参数非法, got %v", err)
	}
	if _, err := m.Ingest("t2", "a b"); !errors.Is(err, ErrTenantLimit) {
		t.Fatalf("应报租户上限, got %v", err)
	}
	// 被拒绝的新租户未登记，之后仍无该租户。
	if _, _, err := m.Snapshot("t2"); !errors.Is(err, ErrNoSuchTenant) {
		t.Fatalf("被拒租户不应登记, got %v", err)
	}
	// 租户串本身非法也算参数非法。
	if _, err := m.Ingest("", "a"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空租户应报参数非法, got %v", err)
	}
	t.Log("拒绝顺序断言通过: 参数非法 > 租户上限，且不登记状态")
}

func TestTenantIsolation(t *testing.T) {
	m, _ := New(50, 10, 10)
	sim := newNaive(50, 10)
	msgs := []string{"open file a ok", "open file b ok", "close x y z", "open 9 file c"}
	for _, msg := range msgs {
		check(t, m, sim, "alice", msg)
		check(t, m, sim, "bob", msg)
	}
	a, aof, _ := m.Snapshot("alice")
	b, bof, _ := m.Snapshot("bob")
	if len(a) != len(b) || aof != bof {
		t.Fatalf("两租户同序列应同结果: %+v vs %+v", a, b)
	}
	if a[0].ID != 1 || b[0].ID != 1 {
		t.Fatalf("租户间 id 应各自从 1 起: a=%d b=%d", a[0].ID, b[0].ID)
	}
	// 不变量：Σcount + overflow == 接受 Ingest 数。
	for name, infos := range map[string][]TemplateInfo{"alice": a, "bob": b} {
		var sum int64
		for _, x := range infos {
			sum += x.Count
		}
		if sum+aof != int64(len(msgs)) {
			t.Fatalf("租户 %s 不变量破坏: Σcount=%d overflow=%d", name, sum, aof)
		}
	}
	t.Logf("租户隔离: alice=%v bob=%v, id 各自计数, 计数和不变量成立", texts(a), texts(b))
}

func TestSetThetaReplayAndNoReeval(t *testing.T) {
	m, _ := New(50, 10, 10)
	sim := newNaive(50, 10)
	check(t, m, sim, "t", "a b c d") // id1
	check(t, m, sim, "t", "a x c d") // eq=3 命中并泛化 id1: a <*> c d
	if err := m.SetTheta(51); err != nil || m.Gen() != 1 {
		t.Fatalf("SetTheta 后 Gen 应为 1, gen=%d err=%v", m.Gen(), err)
	}
	sim.setTheta(51)
	if err := m.SetTheta(0); !errors.Is(err, ErrInvalidArgument) || m.Gen() != 1 {
		t.Fatal("非法 SetTheta 不得推进 Gen")
	}
	// θ 上调后，一条在 θ=50 本可命中的近似消息（通配位处用不相同的具体词，
	// 故对 id1 仅 eq=2）不再合格 → 新建 id2：
	// 同样落在 eq 边界的消息在阈值前后落到不同模板。
	r := check(t, m, sim, "t", "q b c y")
	if !r.Created || r.ID != 2 {
		t.Fatalf("θ 上调后应新建 id2, got %+v", r)
	}
	// 旧模板不被重估：id1 文本不因为新模板出现而改变；同消息重放仍恒满 eq 命中 id1。
	r = check(t, m, sim, "t", "a x c d")
	if r.ID != 1 || !r.Matched {
		t.Fatalf("旧消息应仍命中未重估的 id1, got %+v", r)
	}
	infos, _, _ := m.Snapshot("t")
	if join(infos[0].Text) != "a <*> c d" || infos[0].Gen != 0 || infos[1].Gen != 1 {
		t.Fatalf("旧模板不得重估, 新模板记录创建时 Gen: %+v", infos)
	}
	t.Logf("热更新判定: 旧模板=%q(gen=%d) 保持不变, 新模板 id2 gen=%d",
		join(infos[0].Text), infos[0].Gen, infos[1].Gen)
}

func TestDifferentialNaive(t *testing.T) {
	rng := rand.New(rngSource())
	m, _ := New(50, 100000, 10)
	sim := newNaive(50, 100000)
	vocab := []string{"open", "close", "file", "dir", "ok", "fail", "a", "b", "c", "x"}
	tenants := []string{"t1", "t2", "t3"}
	for step := 0; step < 500; step++ {
		tn := tenants[rng.Intn(len(tenants))]
		n := 1 + rng.Intn(6)
		ws := make([]string, n)
		for i := range ws {
			w := vocab[rng.Intn(len(vocab))]
			if rng.Intn(4) == 0 {
				w = fmt.Sprintf("%s%d", w, rng.Intn(9)) // 触发数字掩码
			}
			ws[i] = w
		}
		msg := strings.Join(ws, " ")
		check(t, m, sim, tn, msg)
	}
	// 全量快照对照（id/文本/计数）。
	for _, tn := range tenants {
		infos, overflow, _ := m.Snapshot(tn)
		snap := sim.snapshot(tn)
		if len(infos) != len(snap) || overflow != sim.overflow[tn] {
			t.Fatalf("%s 模板数/溢出不一致", tn)
		}
		for i, sp := range snap {
			if infos[i].ID != sp.id || join(infos[i].Text) != strings.Join(sp.words, " ") ||
				infos[i].Count != sp.count || infos[i].Gen != sp.gen {
				t.Fatalf("%s 位置%d 不一致: %+v vs %+v", tn, i, infos[i], sp)
			}
		}
	}
	t.Log("500 步随机序列与朴素模拟全量一致（id/文本/计数/Gen/溢出）")
}

func TestOverflowCountInvariant(t *testing.T) {
	m, _ := New(99, 2, 10)
	firsts := []string{"u", "v", "w", "p", "q", "r"}
	for _, f := range firsts {
		m.Ingest("t", fmt.Sprintf("%s a b c d", f)) // 首词纯字母且各异 → 各自叶子
	}
	infos, overflow, _ := m.Snapshot("t")
	var sum int64
	for _, x := range infos {
		sum += x.Count
	}
	if len(infos) != 2 || sum+overflow != 6 || overflow != 4 {
		t.Fatalf("上限不变量破坏: 模板=%d Σcount=%d overflow=%d", len(infos), sum, overflow)
	}
	t.Logf("Tmax=2: 模板 2 个, 溢出桶 %d, Σcount+overflow=6", overflow)
}
