package tlb

import (
	"errors"
	"reflect"
	"testing"
)

func mustNew(t *testing.T, cfg Config) *Model {
	t.Helper()
	m, err := New(cfg)
	if err != nil {
		t.Fatalf("New(%+v): %v", cfg, err)
	}
	return m
}

func mustCreateMMs(t *testing.T, m *Model, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		id, err := m.CreateMM()
		if err != nil {
			t.Fatalf("CreateMM #%d: %v", i, err)
		}
		if id != i {
			t.Fatalf("CreateMM #%d: got id %d", i, id)
		}
	}
}

type switchRes struct {
	asid    uint32
	gen     uint64
	flushed bool
	rolled  bool
}

func mustSwitch(t *testing.T, m *Model, cpu, mm int, want switchRes) {
	t.Helper()
	asid, gen, flushed, rolled, err := m.Switch(cpu, mm)
	if err != nil {
		t.Fatalf("Switch(%d,%d): %v", cpu, mm, err)
	}
	got := switchRes{asid, gen, flushed, rolled}
	if got != want {
		t.Fatalf("Switch(%d,%d) = %+v, want %+v", cpu, mm, got, want)
	}
}

func mustTLB(t *testing.T, m *Model, cpu int, want []Entry) {
	t.Helper()
	got := m.TLBEntries(cpu)
	if len(want) == 0 && len(got) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TLB(%d) = %v, want %v", cpu, got, want)
	}
}

func ent(asid, vpn, pfn uint32) Entry {
	return Entry{Key: Key{ASID: asid, VPN: vpn}, PFN: pfn}
}

// TestSpecExample 逐步复现题目给出的完整示例。
func TestSpecExample(t *testing.T) {
	m := mustNew(t, Config{ASIDs: 3, CPUs: 2, TLBCap: 2, MaxMMs: 5})
	mustCreateMMs(t, m, 4) // X=0, Y=1, Z=2, W=3
	const (
		X = 0
		Y = 1
		Z = 2
		W = 3
	)

	mustSwitch(t, m, 0, X, switchRes{1, 1, false, false})

	if ev, ok, err := m.Fill(0, 5, 50); err != nil || ok {
		t.Fatalf("Fill(0,5,50) = (%v,%v,%v)", ev, ok, err)
	}
	if ev, ok, err := m.Fill(0, 6, 60); err != nil || ok {
		t.Fatalf("Fill(0,6,60) = (%v,%v,%v)", ev, ok, err)
	}
	if pfn, hit, err := m.Lookup(0, 5); err != nil || !hit || pfn != 50 {
		t.Fatalf("Lookup(0,5) = (%v,%v,%v)", pfn, hit, err)
	}
	// TLB(0) 容量 2，(1,5) 刚被使用，(1,6) 最久未用。
	ev, ok, err := m.Fill(0, 7, 70)
	if err != nil || !ok || ev != (Key{ASID: 1, VPN: 6}) {
		t.Fatalf("Fill(0,7,70) evict = (%v,%v,%v), want ((1,6),true,nil)", ev, ok, err)
	}

	mustSwitch(t, m, 1, Y, switchRes{2, 1, false, false})
	mustSwitch(t, m, 0, Z, switchRes{3, 1, false, false})

	// 标签已是 3，Lookup(0,5) 未命中且不改状态。
	if pfn, hit, err := m.Lookup(0, 5); err != nil || hit || pfn != 0 {
		t.Fatalf("Lookup(0,5) after switch = (%v,%v,%v)", pfn, hit, err)
	}
	mustTLB(t, m, 0, []Entry{ent(1, 7, 70), ent(1, 5, 50)})

	// taken 已满：回绕，G=2，reserved 为 cpu0 的 (3,1) 与 cpu1 的 (2,1)，
	// taken={2,3}，W 分得 1，cpu1 被刷新。
	mustSwitch(t, m, 1, W, switchRes{1, 2, true, true})
	if g := m.Gen(); g != 2 {
		t.Fatalf("G = %d, want 2", g)
	}
	for _, a := range []uint32{2, 3} {
		if !m.Taken(a) {
			t.Fatalf("asid %d should be taken", a)
		}
	}
	_, r0, _ := m.CPUState(0)
	_, r1, _ := m.CPUState(1)
	if r0 != (Pair{3, 1}) || r1 != (Pair{2, 1}) {
		t.Fatalf("reserved = %v,%v", r0, r1)
	}

	// X 的 (1,1) 既非当前世代也不在 reserved，taken 又满：再次回绕，
	// G=3，reserved 为 (3,1) 与 (1,2)，taken={1,3}，X 分得 2，cpu0 被刷新。
	mustSwitch(t, m, 0, X, switchRes{2, 3, true, true})
	if g := m.Gen(); g != 3 {
		t.Fatalf("G = %d, want 3", g)
	}
	mustTLB(t, m, 0, nil)
	// Y 原来的 (2,1) 不再保留。
	_, r1, _ = m.CPUState(1)
	if r1 != (Pair{1, 2}) {
		t.Fatalf("cpu1 reserved = %v, want (1,2)", r1)
	}

	// Z 的 (3,1) 等于 cpu0 的 reserved 对，被提升为 (3,3)；
	// cpu1 的 pending 仍为真故刷新。
	mustSwitch(t, m, 1, Z, switchRes{3, 3, true, false})
	if a, g, _ := m.MM(Z); a != 3 || g != 3 {
		t.Fatalf("Z = (%d,%d), want (3,3)", a, g)
	}

	// 此时 G=3，taken={1,2,3}，两个 pending 均为假。
	if _, _, p0 := m.CPUState(0); p0 {
		t.Fatal("cpu0 pending should be false")
	}
	if _, _, p1 := m.CPUState(1); p1 {
		t.Fatal("cpu1 pending should be false")
	}

	// DestroyMM(Y)：世代已旧，只标记。
	rel, n, err := m.DestroyMM(Y)
	if err != nil || rel || n != 0 {
		t.Fatalf("DestroyMM(Y) = (%v,%v,%v), want (false,0,nil)", rel, n, err)
	}

	if _, _, err := m.Fill(0, 9, 90); err != nil {
		t.Fatalf("Fill(0,9,90): %v", err)
	}
	if _, _, err := m.Fill(1, 9, 91); err != nil {
		t.Fatalf("Fill(1,9,91): %v", err)
	}

	// DestroyMM(Z)：cpu1 仍活动在 (3,3)，报 ErrBusy 且状态不变。
	if _, _, err := m.DestroyMM(Z); !errors.Is(err, ErrBusy) {
		t.Fatalf("DestroyMM(Z) err = %v, want ErrBusy", err)
	}
	if a, g, alive := m.MM(Z); a != 3 || g != 3 || !alive {
		t.Fatalf("Z after ErrBusy = (%d,%d,%v)", a, g, alive)
	}

	// W 的 (1,2) 等于 cpu1 的 reserved 对，被提升为 (1,3)。
	mustSwitch(t, m, 0, W, switchRes{1, 3, false, false})
	mustTLB(t, m, 0, []Entry{ent(2, 9, 90)})

	// DestroyMM(X)：asid 2 不属任何 reserved 对，释放并清 TLB。
	rel, n, err = m.DestroyMM(X)
	if err != nil || !rel || n != 1 {
		t.Fatalf("DestroyMM(X) = (%v,%v,%v), want (true,1,nil)", rel, n, err)
	}
	if m.Taken(2) {
		t.Fatal("asid 2 should be freed")
	}
	mustTLB(t, m, 0, nil)

	// 新 mm 分得最小空闲 asid 2，旧条目不得命中。
	id, err := m.CreateMM()
	if err != nil || id != 4 {
		t.Fatalf("CreateMM = (%d,%v), want (4,nil)", id, err)
	}
	mustSwitch(t, m, 0, id, switchRes{2, 3, false, false})
	if _, hit, err := m.Lookup(0, 9); err != nil || hit {
		t.Fatalf("Lookup(0,9) after reuse = hit=%v err=%v, want miss", hit, err)
	}
}

// TestConfigValidation 检查构造参数非法时整体拒绝。
func TestConfigValidation(t *testing.T) {
	bad := []Config{
		{ASIDs: 1, CPUs: 1, TLBCap: 1, MaxMMs: 1},      // A < 2
		{ASIDs: 4097, CPUs: 1, TLBCap: 1, MaxMMs: 1},   // A > 4096
		{ASIDs: 2, CPUs: 0, TLBCap: 1, MaxMMs: 1},      // C < 1
		{ASIDs: 2, CPUs: 17, TLBCap: 1, MaxMMs: 1},     // C > 16
		{ASIDs: 2, CPUs: 1, TLBCap: 0, MaxMMs: 1},      // T < 1
		{ASIDs: 2, CPUs: 1, TLBCap: 65, MaxMMs: 1},     // T > 64
		{ASIDs: 2, CPUs: 1, TLBCap: 1, MaxMMs: 0},      // M < 1
		{ASIDs: 2, CPUs: 1, TLBCap: 1, MaxMMs: 100001}, // M > 1e5
		{ASIDs: 2, CPUs: 2, TLBCap: 1, MaxMMs: 1},      // A 不大于 C
		{ASIDs: 4, CPUs: 4, TLBCap: 1, MaxMMs: 1},      // A == C
	}
	for i, cfg := range bad {
		if _, err := New(cfg); !errors.Is(err, ErrBadConfig) {
			t.Errorf("case %d: New(%+v) err = %v, want ErrBadConfig", i, cfg, err)
		}
	}
	if _, err := New(Config{ASIDs: 2, CPUs: 1, TLBCap: 1, MaxMMs: 1}); err != nil {
		t.Errorf("minimal valid config rejected: %v", err)
	}
	if _, err := New(Config{ASIDs: 4096, CPUs: 16, TLBCap: 64, MaxMMs: 100000}); err != nil {
		t.Errorf("maximal valid config rejected: %v", err)
	}
}
