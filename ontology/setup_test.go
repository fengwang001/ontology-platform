package ontology

import (
	"testing"
)

// testEnv 封装一组“单个 A 侧基数约束 Max=1，B 侧无约束”的标准世界。
type testEnv struct {
	store *Store
	eng   *Engine
	tA    string // 约束持有侧链接类型
	inst  string // 受约束实例
}

const testLinkType = "owns"

func newConstrainedEnv(t *testing.T, maxAttempts int, hook SettleHook, max int) *testEnv {
	t.Helper()
	st := NewStore()
	if err := st.RegisterLinkType(LinkType{
		ID:           testLinkType,
		CardinalityA: &Cardinality{Max: max},
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateInstance("x"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 16; i++ {
		id := "y" + itoa(i)
		if err := st.CreateInstance(id); err != nil {
			t.Fatal(err)
		}
	}
	return &testEnv{store: st, eng: NewEngine(st, maxAttempts, hook), tA: testLinkType, inst: "x"}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func addReq(other string) Request {
	return Request{Instance: "x", Baseline: 0, Ops: []Op{{TypeID: testLinkType, Side: SideA, Other: other, Add: true}}}
}

func (env *testEnv) seedLinks(t *testing.T, others ...string) {
	t.Helper()
	for _, o := range others {
		if !env.store.InstanceExists(o) {
			if err := env.store.CreateInstance(o); err != nil {
				t.Fatal(err)
			}
		}
		ok, err := env.store.commitRaider("x", []Op{{TypeID: testLinkType, Side: SideA, Other: o, Add: true}})
		if err != nil || !ok {
			t.Fatalf("seed %s: ok=%v err=%v", o, ok, err)
		}
	}
}

func (env *testEnv) version(t *testing.T) uint64 {
	t.Helper()
	v, err := env.store.readVersion("x")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// raiderHook 在指定尝试编号上让另一条链接抢先提交（确定性插队）。
type raiderHook struct {
	raids map[int]string // attemptIndex -> 抢先建立的对方实例
	store *Store
	calls []int
}

func (h *raiderHook) hook(req Request, attempt int, readVersion uint64) {
	h.calls = append(h.calls, attempt)
	if other, ok := h.raids[attempt]; ok {
		if _, err := h.store.commitRaider(req.Instance, []Op{{TypeID: req.Ops[0].TypeID, Side: SideA, Other: other, Add: true}}); err != nil {
			panic(err)
		}
	}
}

// toggleHook 在指定尝试上插队执行一组任意操作（可加可减），用于制造基数反复变化。
type toggleHook struct {
	schedule map[int][]Op
	store    *Store
}

func (h *toggleHook) hook(req Request, attempt int, readVersion uint64) {
	if ops, ok := h.schedule[attempt]; ok {
		// 每个尝试编号对应一次原子插队提交：ops 内的移除+新增在同一临界区净结算，
		// 因此“先空出再占用”不会因瞬时超额而被基数校验拒绝。
		if _, err := h.store.commitRaider(req.Instance, ops); err != nil {
			panic(err)
		}
	}
}
