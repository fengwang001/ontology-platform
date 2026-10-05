package restart

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// step 是场景测试的一步操作；logs 为本步追加的日志（restore 步为完整日志）。
type step struct {
	op     string // bind|unbind|down|up|eor|restore
	fec    string
	client uint32
	now    int64
	want   int
	err    error
	logs   []string
}

func formatLog(entries []LogEntry) []string {
	out := []string{}
	for _, e := range entries {
		op := "ALLOC"
		if e.Op == OpFree {
			op = "FREE"
		}
		out = append(out, fmt.Sprintf("%s %s %d %d", op, e.Fec, e.Label, e.Time))
	}
	return out
}

func runSteps(t *testing.T, m *Manager, steps []step) {
	t.Helper()
	var full []string
	for i, s := range steps {
		var err error
		got := 0
		switch s.op {
		case "bind":
			got, err = m.Bind([]byte(s.fec), s.client, s.now)
		case "unbind":
			err = m.Unbind([]byte(s.fec), s.client, s.now)
		case "down":
			err = m.ClientDown(s.client, s.now)
		case "up":
			err = m.ClientUp(s.client, s.now)
		case "eor":
			err = m.EndOfRib(s.client, s.now)
		case "restore":
			err = m.Restore(m.Log(), s.now)
		default:
			t.Fatalf("step %d: unknown op %q", i, s.op)
		}
		if !errors.Is(err, s.err) && !(err == nil && s.err == nil) {
			t.Fatalf("step %d (%s %s c=%d t=%d): err=%v, want %v",
				i, s.op, s.fec, s.client, s.now, err, s.err)
		}
		if s.op == "bind" && s.err == nil && got != s.want {
			t.Fatalf("step %d: label=%d, want %d", i, got, s.want)
		}
		if s.op == "restore" {
			full = append([]string(nil), s.logs...)
		} else {
			full = append(full, s.logs...)
		}
		if gotLog := formatLog(m.Log()); !reflect.DeepEqual(gotLog, full) {
			t.Fatalf("step %d (%s): log=%v, want %v", i, s.op, gotLog, full)
		}
	}
}

func mustManager(t *testing.T, lo, hi int, hd, r int64, q int) *Manager {
	t.Helper()
	m, err := New(lo, hi, hd, r, q)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return m
}

// 规范示例的公共前缀：t=0 三个绑定，t=10 释放 f2，t=20 绑定 f3。
// 得到 f1:{A,B}@100，f3:{A}@102，101 隔离至 60。
func prefixT20() []step {
	return []step{
		{op: "bind", fec: "f1", client: 1, now: 0, want: 100, logs: []string{"ALLOC f1 100 0"}},
		{op: "bind", fec: "f2", client: 1, now: 0, want: 101, logs: []string{"ALLOC f2 101 0"}},
		{op: "bind", fec: "f1", client: 2, now: 0, want: 100},
		{op: "unbind", fec: "f2", client: 1, now: 10, logs: []string{"FREE f2 101 10"}},
		{op: "bind", fec: "f3", client: 1, now: 20, want: 102, logs: []string{"ALLOC f3 102 20"}},
	}
}

func TestScenarios(t *testing.T) {
	cases := []struct {
		name   string
		lo, hi int
		hd, r  int64
		q      int
		steps  []step
	}{
		{
			name: "基本绑定与幂等",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: []step{
				{op: "bind", fec: "f1", client: 1, now: 0, want: 100, logs: []string{"ALLOC f1 100 0"}},
				{op: "bind", fec: "f2", client: 1, now: 0, want: 101, logs: []string{"ALLOC f2 101 0"}},
				{op: "bind", fec: "f1", client: 2, now: 0, want: 100},
				{op: "bind", fec: "f1", client: 1, now: 0, want: 100},
			},
		},
		{
			name: "亲和取回",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "bind", fec: "f2", client: 2, now: 30, want: 101, logs: []string{"ALLOC f2 101 30"}}),
		},
		{
			name: "隔离小1不可取",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "bind", fec: "f4", client: 2, now: 59, want: 103, logs: []string{"ALLOC f4 103 59"}}),
		},
		{
			name: "隔离恰等即可用且亲和失效",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "bind", fec: "f4", client: 2, now: 60, want: 101, logs: []string{"ALLOC f4 101 60"}}),
		},
		{
			name: "属主超限先于标签耗尽",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "bind", fec: "f5", client: 1, now: 25, want: 103, logs: []string{"ALLOC f5 103 25"}},
				step{op: "bind", fec: "f6", client: 1, now: 26, err: ErrOwnerLimit}),
		},
		{
			name: "重启刷新与EndOfRib取now",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "down", client: 1, now: 40},
				step{op: "up", client: 1, now: 45},
				step{op: "bind", fec: "f1", client: 1, now: 45, want: 100},
				step{op: "eor", client: 1, now: 50, logs: []string{"FREE f3 102 50"}},
				step{op: "bind", fec: "f4", client: 3, now: 99, want: 101, logs: []string{"ALLOC f4 101 99"}},
				step{op: "bind", fec: "f5", client: 3, now: 100, want: 102, logs: []string{"ALLOC f5 102 100"}}),
		},
		{
			name: "到期落地FREE取截止时刻",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "down", client: 1, now: 40},
				step{op: "up", client: 1, now: 45},
				step{op: "bind", fec: "f1", client: 1, now: 45, want: 100},
				step{op: "bind", fec: "f9", client: 3, now: 75, want: 101,
					logs: []string{"FREE f3 102 70", "ALLOC f9 101 75"}}),
		},
		{
			name: "共享绑定只剩一方不释放",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "down", client: 1, now: 40},
				step{op: "bind", fec: "f9", client: 3, now: 75, want: 101,
					logs: []string{"FREE f3 102 70", "ALLOC f9 101 75"}},
				step{op: "bind", fec: "f1", client: 4, now: 80, want: 100}),
		},
		{
			name: "二次重启不延长旧截止",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "down", client: 1, now: 40},
				step{op: "up", client: 1, now: 45},
				step{op: "bind", fec: "f1", client: 1, now: 45, want: 100},
				step{op: "down", client: 1, now: 60},
				step{op: "bind", fec: "f9", client: 3, now: 75, want: 101,
					logs: []string{"FREE f3 102 70", "ALLOC f9 101 75"}},
				step{op: "bind", fec: "f10", client: 3, now: 95, want: 103,
					logs: []string{"ALLOC f10 103 95"}}),
		},
		{
			name: "离线期间到期照样移除",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "down", client: 1, now: 40},
				step{op: "bind", fec: "f9", client: 3, now: 80, want: 101,
					logs: []string{"FREE f3 102 70", "ALLOC f9 101 80"}},
				step{op: "bind", fec: "f1", client: 1, now: 85, err: ErrClientOffline},
				step{op: "unbind", fec: "f1", client: 1, now: 85, err: ErrClientOffline},
				step{op: "eor", client: 1, now: 85, err: ErrClientOffline},
				step{op: "down", client: 1, now: 85, err: ErrState},
				step{op: "up", client: 1, now: 90},
				step{op: "eor", client: 1, now: 95},
				step{op: "bind", fec: "f1", client: 1, now: 100, want: 100}),
		},
		{
			name: "占位属主认领",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "down", client: 1, now: 40},
				step{op: "up", client: 1, now: 45},
				step{op: "bind", fec: "f1", client: 1, now: 45, want: 100},
				step{op: "down", client: 1, now: 60},
				step{op: "restore", now: 80, logs: []string{
					"ALLOC f1 100 0", "ALLOC f2 101 0", "FREE f2 101 10", "ALLOC f3 102 20"}},
				step{op: "bind", fec: "f1", client: 2, now: 90, want: 100},
				step{op: "bind", fec: "f9", client: 3, now: 115, want: 101,
					logs: []string{"FREE f3 102 110", "ALLOC f9 101 115"}}),
		},
		{
			name: "占位属主过期",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "down", client: 1, now: 40},
				step{op: "up", client: 1, now: 45},
				step{op: "bind", fec: "f1", client: 1, now: 45, want: 100},
				step{op: "down", client: 1, now: 60},
				step{op: "restore", now: 80, logs: []string{
					"ALLOC f1 100 0", "ALLOC f2 101 0", "FREE f2 101 10", "ALLOC f3 102 20"}},
				step{op: "bind", fec: "f9", client: 3, now: 115, want: 101,
					logs: []string{"FREE f1 100 110", "FREE f3 102 110", "ALLOC f9 101 115"}}),
		},
		{
			name: "Restore后客户端为正常态",
			lo:   100, hi: 103, hd: 50, r: 30, q: 3,
			steps: append(prefixT20(),
				step{op: "down", client: 1, now: 40},
				step{op: "restore", now: 50, logs: []string{
					"ALLOC f1 100 0", "ALLOC f2 101 0", "FREE f2 101 10", "ALLOC f3 102 20"}},
				// 101 隔离至 60，t=55 仍不可取；恰等可用。
				step{op: "bind", fec: "f9", client: 1, now: 55, want: 103,
					logs: []string{"ALLOC f9 103 55"}},
				step{op: "bind", fec: "f10", client: 1, now: 60, want: 101,
					logs: []string{"ALLOC f10 101 60"}}),
		},
		{
			name: "陈旧关系计入Q",
			lo:   100, hi: 103, hd: 50, r: 30, q: 2,
			steps: []step{
				{op: "bind", fec: "f1", client: 1, now: 0, want: 100, logs: []string{"ALLOC f1 100 0"}},
				{op: "bind", fec: "f2", client: 1, now: 0, want: 101, logs: []string{"ALLOC f2 101 0"}},
				{op: "down", client: 1, now: 10},
				{op: "up", client: 1, now: 15},
				{op: "bind", fec: "f3", client: 1, now: 20, err: ErrOwnerLimit},
			},
		},
		{
			name: "被拒不落地不推进时钟",
			lo:   100, hi: 102, hd: 50, r: 30, q: 5,
			steps: []step{
				{op: "bind", fec: "f1", client: 1, now: 0, want: 100, logs: []string{"ALLOC f1 100 0"}},
				{op: "bind", fec: "f1", client: 2, now: 0, want: 100},
				{op: "bind", fec: "f2", client: 1, now: 0, want: 101, logs: []string{"ALLOC f2 101 0"}},
				{op: "bind", fec: "f3", client: 2, now: 0, want: 102, logs: []string{"ALLOC f3 102 0"}},
				{op: "down", client: 1, now: 10},
				// 到期落地会释放 f2@101（隔离到 90），t=50 仍无可用标签 → 拒绝且不落地。
				{op: "bind", fec: "f4", client: 2, now: 50, err: ErrExhausted},
				// 时钟未被拒绝操作推进：t=45 仍被接受；被接受的操作先落地到期，
				// FREE 时刻取截止时刻 40 而非 45。
				{op: "unbind", fec: "f3", client: 2, now: 45,
					logs: []string{"FREE f2 101 40", "FREE f3 102 45"}},
				{op: "bind", fec: "f5", client: 2, now: 100, want: 101,
					logs: []string{"ALLOC f5 101 100"}},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := mustManager(t, c.lo, c.hi, c.hd, c.r, c.q)
			runSteps(t, m, c.steps)
		})
	}
}

// 前缀 Restore：任意前缀重建出的 fec→标签映射等于写完该前缀时的映射。
func TestPrefixRestoreMapping(t *testing.T) {
	const lo, hi = 100, 103
	m := mustManager(t, lo, hi, 50, 30, 5)
	ops := []step{
		{op: "bind", fec: "f1", client: 1, now: 0, want: 100},
		{op: "bind", fec: "f2", client: 1, now: 0, want: 101},
		{op: "bind", fec: "f1", client: 2, now: 0, want: 100},
		{op: "unbind", fec: "f2", client: 1, now: 10},
		{op: "bind", fec: "f3", client: 1, now: 20, want: 102},
		{op: "bind", fec: "f2", client: 2, now: 30, want: 101},
		{op: "down", client: 1, now: 40},
		{op: "up", client: 1, now: 45},
		{op: "bind", fec: "f1", client: 1, now: 45, want: 100},
		{op: "eor", client: 1, now: 50},
		{op: "bind", fec: "f4", client: 3, now: 60, want: 103},
		{op: "unbind", fec: "f1", client: 2, now: 70},
		{op: "bind", fec: "f5", client: 4, now: 130, want: 102},
	}
	type snap struct {
		logLen  int
		now     int64
		mapping map[string]int
	}
	var snaps []snap
	run := func(m *Manager, s step) {
		switch s.op {
		case "bind":
			m.Bind([]byte(s.fec), s.client, s.now)
		case "unbind":
			m.Unbind([]byte(s.fec), s.client, s.now)
		case "down":
			m.ClientDown(s.client, s.now)
		case "up":
			m.ClientUp(s.client, s.now)
		case "eor":
			m.EndOfRib(s.client, s.now)
		}
	}
	for _, s := range ops {
		run(m, s)
		snaps = append(snaps, snap{len(m.Log()), s.now, mappingOf(m)})
	}
	fullLog := m.Log()
	seen := map[int]bool{0: true}
	// 空前缀也要可 Restore。
	check := func(prefix int, now int64, want map[string]int) {
		t.Helper()
		m2 := mustManager(t, lo, hi, 50, 30, 5)
		if err := m2.Restore(fullLog[:prefix], now); err != nil {
			t.Fatalf("Restore(prefix=%d): %v", prefix, err)
		}
		if got := mappingOf(m2); !reflect.DeepEqual(got, want) {
			t.Fatalf("prefix=%d: mapping=%v, want %v", prefix, got, want)
		}
	}
	check(0, 0, map[string]int{})
	for _, s := range snaps {
		if seen[s.logLen] {
			continue
		}
		seen[s.logLen] = true
		check(s.logLen, s.now, s.mapping)
	}
}

func mappingOf(m *Manager) map[string]int {
	out := map[string]int{}
	for _, f := range m.tbl.Fecs() {
		e, _ := m.tbl.Get(f)
		out[f] = e.Label
	}
	return out
}

// Bind/Unbind/EndOfRib 的拒绝次序逐项验证。
func TestRejectOrder(t *testing.T) {
	m := mustManager(t, 100, 103, 50, 30, 1)
	bind := func(fec string, c uint32, now int64) error {
		_, err := m.Bind([]byte(fec), c, now)
		return err
	}
	// 参数非法 > 时钟回退。
	if _, err := m.Bind(nil, 1, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty fec: %v", err)
	}
	if err := bind("f1", 1, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Bind(nil, 1, 5); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid > clock: %v", err)
	}
	// 时钟回退 > 客户端离线。
	if err := m.ClientDown(1, 20); err != nil {
		t.Fatal(err)
	}
	if err := bind("f1", 1, 15); !errors.Is(err, ErrClock) {
		t.Fatalf("clock > offline: %v", err)
	}
	// 客户端离线 > 已是属主。
	if err := bind("f1", 1, 25); !errors.Is(err, ErrClientOffline) {
		t.Fatalf("offline > owner: %v", err)
	}
	// 恢复后刷新陈旧关系：Q=1 下新关系超限，已是属主则直接成功。
	if err := m.ClientUp(1, 30); err != nil {
		t.Fatal(err)
	}
	if err := bind("f1", 1, 35); err != nil {
		t.Fatalf("refresh stale: %v", err)
	}
	if err := bind("f2", 1, 40); !errors.Is(err, ErrOwnerLimit) {
		t.Fatalf("owner limit: %v", err)
	}
	if err := bind("f1", 1, 41); err != nil {
		t.Fatalf("owner shortcut beats Q: %v", err)
	}
	// Unbind：参数非法 > 时钟回退 > 客户端离线 > 绑定不存在。
	if err := m.Unbind(nil, 1, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unbind invalid: %v", err)
	}
	if err := m.Unbind([]byte("f1"), 1, 5); !errors.Is(err, ErrClock) {
		t.Fatalf("unbind clock: %v", err)
	}
	if err := m.ClientDown(1, 50); err != nil {
		t.Fatal(err)
	}
	if err := m.Unbind([]byte("f1"), 1, 55); !errors.Is(err, ErrClientOffline) {
		t.Fatalf("unbind offline: %v", err)
	}
	if err := m.ClientUp(1, 60); err != nil {
		t.Fatal(err)
	}
	if err := m.Unbind([]byte("f9"), 1, 65); !errors.Is(err, ErrNoBinding) {
		t.Fatalf("unbind no binding: %v", err)
	}
	if err := m.Unbind([]byte("f1"), 2, 65); !errors.Is(err, ErrNoBinding) {
		t.Fatalf("unbind not owner: %v", err)
	}
	// EndOfRib：非恢复中报状态不符；ClientUp 非离线和 ClientDown 已离线同理。
	if err := m.EndOfRib(2, 70); !errors.Is(err, ErrState) {
		t.Fatalf("eor normal client: %v", err)
	}
	if err := m.ClientUp(2, 70); !errors.Is(err, ErrState) {
		t.Fatalf("up normal client: %v", err)
	}
	// 恢复中的客户端可再次 ClientDown；已离线再 Down 报状态不符。
	if err := m.ClientDown(1, 75); err != nil {
		t.Fatalf("down recovering client: %v", err)
	}
	if err := m.ClientDown(1, 80); !errors.Is(err, ErrState) {
		t.Fatalf("down offline client: %v", err)
	}
}

func TestParamValidation(t *testing.T) {
	newCases := []struct {
		lo, hi int
		hd, r  int64
		q      int
		ok     bool
	}{
		{16, 1048575, 0, 0, 1, true},
		{16, 1048575, 1_000_000_000, 1_000_000_000, 1_000_000, true},
		{15, 100, 0, 0, 1, false},
		{16, 1048576, 0, 0, 1, false},
		{200, 100, 0, 0, 1, false},
		{16, 100, -1, 0, 1, false},
		{16, 100, 0, 1_000_000_001, 1, false},
		{16, 100, 0, 0, 0, false},
		{16, 100, 0, 0, 1_000_001, false},
	}
	for i, c := range newCases {
		_, err := New(c.lo, c.hi, c.hd, c.r, c.q)
		if got := err == nil; got != c.ok {
			t.Errorf("case %d: ok=%v, want %v", i, got, c.ok)
		}
	}
	m := mustManager(t, 100, 103, 50, 30, 3)
	if _, err := m.Bind([]byte(strings.Repeat("x", 65)), 1, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("fec too long: %v", err)
	}
	if _, err := m.Bind([]byte("f1"), 0, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("client 0: %v", err)
	}
	if _, err := m.Bind([]byte("f1"), 10001, 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("client too large: %v", err)
	}
	if _, err := m.Bind([]byte("f1"), 1, -1); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative now: %v", err)
	}
	if _, err := m.Bind([]byte("f1"), 1, 1_000_000_000_001); !errors.Is(err, ErrInvalid) {
		t.Fatalf("now too large: %v", err)
	}
	if _, err := m.Bind([]byte(strings.Repeat("x", 64)), 10000, 1_000_000_000_000); err != nil {
		t.Fatalf("boundary values should be accepted: %v", err)
	}
}

func TestRestoreValidation(t *testing.T) {
	m := mustManager(t, 100, 103, 50, 30, 3)
	if _, err := m.Bind([]byte("f1"), 1, 0); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		log  []LogEntry
		now  int64
		err  error
	}{
		{"重复ALLOC", []LogEntry{{OpAlloc, "f1", 100, 0}, {OpAlloc, "f1", 101, 1}}, 10, ErrCorrupt},
		{"标签冲突", []LogEntry{{OpAlloc, "f1", 100, 0}, {OpAlloc, "f2", 100, 1}}, 10, ErrCorrupt},
		{"FREE未绑定", []LogEntry{{OpFree, "f9", 100, 0}}, 10, ErrCorrupt},
		{"FREE标签不符", []LogEntry{{OpAlloc, "f1", 100, 0}, {OpFree, "f1", 101, 1}}, 10, ErrCorrupt},
		{"标签越界", []LogEntry{{OpAlloc, "f1", 999, 0}}, 10, ErrInvalid},
		{"非法Op", []LogEntry{{LogOp(9), "f1", 100, 0}}, 10, ErrInvalid},
		{"空fec", []LogEntry{{OpAlloc, "", 100, 0}}, 10, ErrInvalid},
		{"时钟小于日志", []LogEntry{{OpAlloc, "f1", 100, 50}}, 10, ErrClock},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := m.Restore(c.log, c.now); !errors.Is(err, c.err) {
				t.Fatalf("got %v, want %v", err, c.err)
			}
		})
	}
	// 校验失败的 Restore 不改状态：原绑定仍在。
	if got, err := m.Bind([]byte("f1"), 2, 100); err != nil || got != 100 {
		t.Fatalf("state after failed restores: (%d,%v)", got, err)
	}
	// 成功 Restore 后占位属主截止 now+R。
	if err := m.Restore(m.Log(), 200); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Bind([]byte("f1"), 3, 210); err != nil || got != 100 {
		t.Fatalf("claim after restore: (%d,%v)", got, err)
	}
}

// 相同操作序列重放得到相同标签与日志。
func TestReplayDeterminism(t *testing.T) {
	script := []step{
		{op: "bind", fec: "f1", client: 1, now: 0, want: 100},
		{op: "bind", fec: "f2", client: 1, now: 0, want: 101},
		{op: "unbind", fec: "f2", client: 1, now: 10},
		{op: "bind", fec: "f3", client: 2, now: 20, want: 102},
		{op: "down", client: 1, now: 30},
		{op: "bind", fec: "f4", client: 3, now: 70, want: 101},
		{op: "restore", now: 80},
		{op: "bind", fec: "f1", client: 2, now: 90, want: 100},
	}
	m1 := mustManager(t, 100, 103, 50, 30, 3)
	m2 := mustManager(t, 100, 103, 50, 30, 3)
	runOne := func(m *Manager, s step) {
		switch s.op {
		case "bind":
			m.Bind([]byte(s.fec), s.client, s.now)
		case "unbind":
			m.Unbind([]byte(s.fec), s.client, s.now)
		case "down":
			m.ClientDown(s.client, s.now)
		case "up":
			m.ClientUp(s.client, s.now)
		case "eor":
			m.EndOfRib(s.client, s.now)
		case "restore":
			m.Restore(m.Log(), s.now)
		}
	}
	for _, s := range script {
		runOne(m1, s)
		runOne(m2, s)
	}
	if !reflect.DeepEqual(m1.Log(), m2.Log()) {
		t.Fatalf("logs differ:\n%v\n%v", m1.Log(), m2.Log())
	}
	if !reflect.DeepEqual(mappingOf(m1), mappingOf(m2)) {
		t.Fatal("mappings differ")
	}
}

// 并发调用等价于某串行顺序：不变式为标签↔fec 双射，且日志可重放。
func TestConcurrent(t *testing.T) {
	m := mustManager(t, 16, 1015, 10, 20, 50)
	var clock int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				now := atomic.AddInt64(&clock, 1)
				fec := []byte{byte('a' + (g*7+i)%23)}
				client := uint32(g%5 + 1)
				switch i % 5 {
				case 0:
					m.Unbind(fec, client, now)
				case 1:
					m.ClientDown(client, now)
				case 2:
					m.ClientUp(client, now)
				case 3:
					m.EndOfRib(client, now)
				default:
					m.Bind(fec, client, now)
				}
			}
		}(g)
	}
	wg.Wait()
	// 标签↔fec 双射。
	used := map[int]string{}
	for f, l := range mappingOf(m) {
		if prev, ok := used[l]; ok {
			t.Fatalf("label %d bound to both %q and %q", l, prev, f)
		}
		used[l] = f
	}
	// 日志可重放且映射一致。
	m2 := mustManager(t, 16, 1015, 10, 20, 50)
	if err := m2.Restore(m.Log(), atomic.LoadInt64(&clock)); err != nil {
		t.Fatalf("restore concurrent log: %v", err)
	}
	if !reflect.DeepEqual(mappingOf(m), mappingOf(m2)) {
		t.Fatal("restored mapping differs")
	}
}
