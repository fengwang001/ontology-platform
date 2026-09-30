package conntrack

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type testLogger struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *testLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(&l.buf, format+"\n", args...)
}

func (l *testLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func ep(addr string, port uint16) Endpoint { return Endpoint{Addr: addr, Port: port} }

func key(srcPort, dstPort uint16) ConnKey {
	return ConnKey{Src: ep("10.0.0.1", srcPort), Dst: ep("203.0.113.9", dstPort)}
}

func newTestTable(t *testing.T, cfg Config, logger Logger) *Table {
	t.Helper()
	if cfg.Lo == 0 && cfg.Hi == 0 {
		cfg.Lo, cfg.Hi = 4000, 4099
	}
	if cfg.N == 0 {
		cfg.N = 64
	}
	if cfg.HalfOpenTimeout == 0 {
		cfg.HalfOpenTimeout = 5
	}
	if cfg.EstablishedTimeout == 0 {
		cfg.EstablishedTimeout = 100
	}
	if cfg.ClosedTimeout == 0 {
		cfg.ClosedTimeout = 10
	}
	cfg.Logger = logger
	tab, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return tab
}

func TestInvalidConfig(t *testing.T) {
	base := Config{Lo: 4000, Hi: 4099, N: 10, HalfOpenTimeout: 5, EstablishedTimeout: 100, ClosedTimeout: 10}
	cases := []struct {
		name string
		mod  func(*Config)
	}{
		{"lo>hi", func(c *Config) { c.Lo, c.Hi = 5000, 4000 }},
		{"N zero", func(c *Config) { c.N = 0 }},
		{"N negative", func(c *Config) { c.N = -1 }},
		{"Th zero", func(c *Config) { c.HalfOpenTimeout = 0 }},
		{"Th negative", func(c *Config) { c.HalfOpenTimeout = -1 }},
		{"Te zero", func(c *Config) { c.EstablishedTimeout = 0 }},
		{"Te negative", func(c *Config) { c.EstablishedTimeout = -3 }},
		{"Tc zero", func(c *Config) { c.ClosedTimeout = 0 }},
		{"Tc negative", func(c *Config) { c.ClosedTimeout = -2 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := base
			tc.mod(&cfg)
			tab, err := New(cfg)
			if err == nil || tab != nil {
				t.Fatalf("expected rejection for %s, got tab=%v err=%v", tc.name, tab, err)
			}
		})
	}
}

func TestLifecycleAndTimeouts(t *testing.T) {
	tab := newTestTable(t, Config{}, nil)
	k := key(100, 9000)

	// 出站 SYN 新建半开连接，到期为 now+Th。
	o, err := tab.ProcessOutbound(0, k, PktSYN)
	if err != nil || !o.Created || o.State != StateHalfOpen || o.ExpireAt != 5 {
		t.Fatalf("outbound SYN: %+v %v", o, err)
	}

	// 半开时放行的出站包刷新到期为 now+Th。
	if o, _ = tab.ProcessOutbound(3, k, PktDATA); o.ExpireAt != 8 || o.State != StateHalfOpen {
		t.Fatalf("half-open outbound refresh: %+v", o)
	}

	// 入站 SYN 使其已建立，到期为 now+Te。
	if o, err = tab.ProcessInbound(4, 4000, k.Dst, PktSYN); err != nil ||
		o.State != StateEstablished || o.ExpireAt != 104 {
		t.Fatalf("inbound SYN establish: %+v %v", o, err)
	}

	// 已建立时放行的包刷新到期为 now+Te。
	if o, _ = tab.ProcessOutbound(10, k, PktDATA); o.ExpireAt != 110 {
		t.Fatalf("established refresh: %+v", o)
	}

	// FIN 使非关闭连接进入关闭，到期为 now+Tc。
	if o, _ = tab.ProcessOutbound(20, k, PktFIN); o.State != StateClosed || o.ExpireAt != 30 {
		t.Fatalf("FIN close: %+v", o)
	}

	// 关闭后放行的包（入站/出站）不刷新到期。
	if o, _ = tab.ProcessInbound(25, 4000, k.Dst, PktDATA); o.ExpireAt != 30 || o.State != StateClosed {
		t.Fatalf("closed inbound must not refresh: %+v", o)
	}
	if o, _ = tab.ProcessOutbound(26, k, PktDATA); o.ExpireAt != 30 {
		t.Fatalf("closed outbound must not refresh: %+v", o)
	}

	// 到期前一刻仍存活；恰在到期时刻失效，视为不存在。
	if info, ok := tab.Lookup(k, 29); !ok || info.ExpireAt != 30 {
		t.Fatalf("connection must be live at t=29: %+v %v", info, ok)
	}
	if _, ok := tab.Lookup(k, 30); ok {
		t.Fatal("connection must be expired exactly at expireAt=30")
	}
	if _, err := tab.ProcessInbound(30, 4000, k.Dst, PktDATA); !errors.Is(err, ErrNoConnection) {
		t.Fatalf("inbound exactly at expiry: mapping exists but expired conn is gone, got %v", err)
	}
}

func TestHalfOpenInboundDataEstablishes(t *testing.T) {
	tab := newTestTable(t, Config{}, nil)
	k := key(100, 9000)
	if _, err := tab.ProcessOutbound(0, k, PktSYN); err != nil {
		t.Fatal(err)
	}
	o, err := tab.ProcessInbound(1, 4000, k.Dst, PktDATA)
	if err != nil || o.State != StateEstablished || o.ExpireAt != 101 {
		t.Fatalf("inbound DATA should establish: %+v %v", o, err)
	}
}

func TestExpiryBoundaryHalfOpen(t *testing.T) {
	tab := newTestTable(t, Config{}, nil)
	k := key(100, 9000)
	if _, err := tab.ProcessOutbound(0, k, PktSYN); err != nil { // 半开，到期 t=5
		t.Fatal(err)
	}
	// 恰在到期时刻 t=5 的入站包必须被拒绝（连接已不存在，拒绝不触发清扫）。
	if _, err := tab.ProcessInbound(5, 4000, k.Dst, PktDATA); !errors.Is(err, ErrNoConnection) {
		t.Fatalf("inbound exactly at half-open expiry must fail, got %v", err)
	}
}

func TestSameEndpointSharesExternalPort(t *testing.T) {
	tab := newTestTable(t, Config{}, nil)
	src := ep("10.0.0.1", 1234)
	k1 := ConnKey{Src: src, Dst: ep("1.1.1.1", 80)}
	k2 := ConnKey{Src: src, Dst: ep("2.2.2.2", 443)}
	k3 := ConnKey{Src: src, Dst: ep("3.3.3.3", 22)}

	o1, err := tab.ProcessOutbound(0, k1, PktSYN)
	if err != nil {
		t.Fatal(err)
	}
	o2, _ := tab.ProcessOutbound(1, k2, PktSYN)
	o3, _ := tab.ProcessOutbound(2, k3, PktSYN)
	if o1.ExtPort != 4000 || o2.ExtPort != 4000 || o3.ExtPort != 4000 {
		t.Fatalf("all connections of one endpoint must share port: %d %d %d", o1.ExtPort, o2.ExtPort, o3.ExtPort)
	}
	if s := tab.Stats(2); s.Connections != 3 || s.UsedPorts != 1 {
		t.Fatalf("stats: %+v", s)
	}

	// 三条连接消失两条，端口仍不释放；最后一条消失才释放。
	if _, err := tab.ProcessOutbound(3, k1, PktRST); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.ProcessOutbound(4, k2, PktRST); err != nil {
		t.Fatal(err)
	}
	if s := tab.Stats(4); s.Connections != 1 || s.UsedPorts != 1 {
		t.Fatalf("port must stay until the last connection is gone: %+v", s)
	}
	if _, err := tab.ProcessOutbound(5, k3, PktRST); err != nil {
		t.Fatal(err)
	}
	if s := tab.Stats(5); s.Connections != 0 || s.UsedPorts != 0 {
		t.Fatalf("port must be released with last connection: %+v", s)
	}
}

func TestSmallestFreePortAndReuse(t *testing.T) {
	tab := newTestTable(t, Config{Lo: 4000, Hi: 4002, N: 100}, nil)
	kA := ConnKey{Src: ep("10.0.0.1", 1), Dst: ep("1.1.1.1", 80)}
	kB := ConnKey{Src: ep("10.0.0.2", 2), Dst: ep("1.1.1.1", 80)}
	kC := ConnKey{Src: ep("10.0.0.3", 3), Dst: ep("1.1.1.1", 80)}

	oA, _ := tab.ProcessOutbound(0, kA, PktSYN)
	oB, _ := tab.ProcessOutbound(0, kB, PktSYN)
	if oA.ExtPort != 4000 || oB.ExtPort != 4001 {
		t.Fatalf("expect smallest free ports 4000/4001, got %d/%d", oA.ExtPort, oB.ExtPort)
	}
	// 释放中间端口 4001，新端点应取最小空闲端口 4001 重用。
	if _, err := tab.ProcessOutbound(0, kB, PktRST); err != nil {
		t.Fatal(err)
	}
	oC, err := tab.ProcessOutbound(1, kC, PktSYN)
	if err != nil || oC.ExtPort != 4001 {
		t.Fatalf("freed port 4001 must be reused as smallest free, got %d (%v)", oC.ExtPort, err)
	}
	// 到期回收后端口同样可被重用：A(t=250 过期)、C(t=111 过期)。
	if _, err := tab.ProcessInbound(2, 4000, kA.Dst, PktSYN); err != nil { // A 已建立, expire 102
		t.Fatal(err)
	}
	kD := ConnKey{Src: ep("10.0.0.4", 4), Dst: ep("4.4.4.4", 80)}
	// t=111: C(半开 expire=6) 与 A(已建立 expire=102)…… 显式等到两者过期。
	if _, ok := tab.Lookup(kC, 200); ok {
		t.Fatal("kC should be expired at t=200")
	}
	if _, ok := tab.Lookup(kA, 200); ok {
		t.Fatal("kA should be expired at t=200")
	}
	// 触发清扫后，4000/4001 都应重新可分配，最小为 4000。
	if o, err := tab.ProcessOutbound(201, kD, PktSYN); err != nil || o.ExtPort != 4000 {
		t.Fatalf("after expiry reuse smallest port 4000, got %d (%v)", o.ExtPort, err)
	}
}

func TestPortPoolExhaustion(t *testing.T) {
	tab := newTestTable(t, Config{Lo: 4000, Hi: 4001, N: 1000}, nil)
	for i := 0; i < 2; i++ {
		k := ConnKey{Src: ep("10.0.0.x", uint16(10+i)), Dst: ep("1.1.1.1", 80)}
		if _, err := tab.ProcessOutbound(int64(i), k, PktSYN); err != nil {
			t.Fatalf("conn %d: %v", i, err)
		}
	}
	kExtra := ConnKey{Src: ep("10.0.0.9", 99), Dst: ep("1.1.1.1", 80)}
	if _, err := tab.ProcessOutbound(2, kExtra, PktSYN); !errors.Is(err, ErrPortPoolExhausted) {
		t.Fatalf("expected ErrPortPoolExhausted, got %v", err)
	}
}

func TestTableFull(t *testing.T) {
	tab := newTestTable(t, Config{Lo: 4000, Hi: 4999, N: 2}, nil)
	k1 := ConnKey{Src: ep("10.0.0.1", 1), Dst: ep("1.1.1.1", 80)}
	k2 := ConnKey{Src: ep("10.0.0.2", 2), Dst: ep("2.2.2.2", 80)}
	k3 := ConnKey{Src: ep("10.0.0.3", 3), Dst: ep("3.3.3.3", 80)}
	if _, err := tab.ProcessOutbound(0, k1, PktSYN); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.ProcessOutbound(0, k2, PktSYN); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.ProcessOutbound(0, k3, PktSYN); !errors.Is(err, ErrTableFull) {
		t.Fatalf("expected ErrTableFull, got %v", err)
	}
	// 删除一条后释放连接槽，可再建。
	if _, err := tab.ProcessOutbound(1, k1, PktRST); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.ProcessOutbound(2, k3, PktSYN); err != nil {
		t.Fatalf("slot should be free after RST: %v", err)
	}
}

func TestOutboundRejectionOrder(t *testing.T) {
	tab := newTestTable(t, Config{Lo: 4000, Hi: 4010, N: 2}, nil)
	kKnown := key(100, 9000)
	if _, err := tab.ProcessOutbound(10, kKnown, PktSYN); err != nil {
		t.Fatal(err)
	}

	// 1) 时钟回拨最先报，即使报文同时也是“无连接的非 SYN”。
	kUnknown := key(101, 9001)
	if _, err := tab.ProcessOutbound(9, kUnknown, PktDATA); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("clock rewind must be checked first, got %v", err)
	}
	// 2) 无连接且非 SYN。
	if _, err := tab.ProcessOutbound(11, kUnknown, PktDATA); !errors.Is(err, ErrNotSYN) {
		t.Fatalf("expected ErrNotSYN, got %v", err)
	}
	if _, err := tab.ProcessOutbound(11, kUnknown, PktFIN); !errors.Is(err, ErrNotSYN) {
		t.Fatalf("FIN without connection expected ErrNotSYN, got %v", err)
	}
	if _, err := tab.ProcessOutbound(11, kUnknown, PktRST); !errors.Is(err, ErrNotSYN) {
		t.Fatalf("RST without connection expected ErrNotSYN, got %v", err)
	}

	// 3) 需新建连接而表满：N=2，已有 1 条；占满后第 3 个新端点应报表满（此时端口池仍空）。
	k2 := ConnKey{Src: ep("10.0.0.2", 2), Dst: ep("2.2.2.2", 80)}
	if _, err := tab.ProcessOutbound(12, k2, PktSYN); err != nil {
		t.Fatal(err)
	}
	k3 := ConnKey{Src: ep("10.0.0.3", 3), Dst: ep("3.3.3.3", 80)}
	if _, err := tab.ProcessOutbound(13, k3, PktSYN); !errors.Is(err, ErrTableFull) {
		t.Fatalf("table full must precede port availability, got %v", err)
	}

	// 4) 拒绝不得改变任何连接的状态与到期。
	infoBefore, ok := tab.Lookup(kKnown, 13)
	if !ok {
		t.Fatal("known conn missing")
	}
	_, _ = tab.ProcessOutbound(9, kUnknown, PktDATA)
	_, _ = tab.ProcessOutbound(11, kUnknown, PktDATA)
	_, _ = tab.ProcessOutbound(13, k3, PktSYN)
	infoAfter, _ := tab.Lookup(kKnown, 13)
	if infoBefore != infoAfter {
		t.Fatalf("rejected ops mutated state: before=%+v after=%+v", infoBefore, infoAfter)
	}
	if s := tab.Stats(13); s.Connections != 2 {
		t.Fatalf("rejected ops changed connection count: %+v", s)
	}
}

func TestInboundRejectionsAndRST(t *testing.T) {
	tab := newTestTable(t, Config{}, nil)
	k := key(100, 9000)
	if _, err := tab.ProcessOutbound(10, k, PktSYN); err != nil {
		t.Fatal(err)
	}

	// 时钟回拨优先。
	if _, err := tab.ProcessInbound(9, 4000, k.Dst, PktDATA); !errors.Is(err, ErrClockRewind) {
		t.Fatalf("inbound clock rewind first, got %v", err)
	}
	// 外部端口无映射。
	if _, err := tab.ProcessInbound(11, 5555, k.Dst, PktDATA); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("expected ErrNoMapping, got %v", err)
	}
	// 有映射但远端不匹配。
	wrongRemote := ep("198.51.100.7", 7777)
	if _, err := tab.ProcessInbound(11, 4000, wrongRemote, PktDATA); !errors.Is(err, ErrNoConnection) {
		t.Fatalf("expected ErrNoConnection, got %v", err)
	}

	// 拒绝不修改状态与到期。
	infoBefore, _ := tab.Lookup(k, 11)
	_, _ = tab.ProcessInbound(9, 4000, k.Dst, PktDATA)
	_, _ = tab.ProcessInbound(11, 5555, k.Dst, PktDATA)
	_, _ = tab.ProcessInbound(11, 4000, wrongRemote, PktDATA)
	infoAfter, _ := tab.Lookup(k, 11)
	if infoBefore != infoAfter {
		t.Fatalf("rejected inbound mutated state: %+v vs %+v", infoBefore, infoAfter)
	}

	// 入站 RST 立即删除连接。
	if o, err := tab.ProcessInbound(12, 4000, k.Dst, PktRST); err != nil || !o.Deleted {
		t.Fatalf("inbound RST should delete: %+v %v", o, err)
	}
	if _, ok := tab.Lookup(k, 12); ok {
		t.Fatal("connection must be gone after inbound RST")
	}
	if _, err := tab.ProcessInbound(13, 4000, k.Dst, PktDATA); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("after RST port is released, expected ErrNoMapping, got %v", err)
	}
}

func TestFinInHalfOpenGoesClosed(t *testing.T) {
	tab := newTestTable(t, Config{}, nil)
	k := key(100, 9000)
	if _, err := tab.ProcessOutbound(0, k, PktSYN); err != nil {
		t.Fatal(err)
	}
	// 半开时入站 FIN：非关闭连接 -> 关闭，到期 now+Tc（不经过已建立）。
	o, err := tab.ProcessInbound(1, 4000, k.Dst, PktFIN)
	if err != nil || o.State != StateClosed || o.ExpireAt != 11 {
		t.Fatalf("FIN in half-open: %+v %v", o, err)
	}
	// 再次 FIN 不刷新到期。
	if o, _ = tab.ProcessOutbound(5, k, PktFIN); o.ExpireAt != 11 {
		t.Fatalf("FIN while closed must keep expiry, got %+v", o)
	}
}

func TestLoggerShowsInputOutputBasis(t *testing.T) {
	lg := &testLogger{}
	tab := newTestTable(t, Config{}, lg)
	k := key(100, 9000)
	if _, err := tab.ProcessOutbound(0, k, PktSYN); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.ProcessInbound(1, 4000, k.Dst, PktSYN); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.ProcessOutbound(2, k, PktFIN); err != nil {
		t.Fatal(err)
	}
	if _, err := tab.ProcessInbound(99, 4000, k.Dst, PktDATA); err == nil {
		t.Fatal("expected rejection at t=99")
	}
	log := lg.String()
	for _, want := range []string{"OUT", "IN", "src=10.0.0.1:100", "dst=203.0.113.9:9000",
		"CREATE", "HALF_OPEN->ESTABLISHED", "->CLOSED", "REJECT", "basis"} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}

func TestConcurrentInvariants(t *testing.T) {
	const endpoints = 60
	tab := newTestTable(t, Config{
		Lo: 4000, Hi: 4100, N: endpoints * 3,
		HalfOpenTimeout: 1 << 30, EstablishedTimeout: 1 << 30, ClosedTimeout: 1 << 30,
	}, nil)

	var wg sync.WaitGroup
	var clock atomic.Int64
	var writeMu sync.Mutex
	for i := 0; i < endpoints; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			src := ep("10.0.0.x", uint16(1000+i))
			// 每个端点访问两个不同远端，应共用同一个外部端口。
			k1 := ConnKey{Src: src, Dst: ep("1.1.1.1", 80)}
			k2 := ConnKey{Src: src, Dst: ep("2.2.2.2", 81)}
			// 写操作经 writeMu 串行：领取的单调时刻与进入表的顺序一致，
			// 不会因“领取时刻早、拿到表锁晚”触发时钟回拨。
			var p1 uint16
			writeMu.Lock()
			o1, err := tab.ProcessOutbound(clock.Add(1), k1, PktSYN)
			if err == nil {
				var o2 Outcome
				o2, err = tab.ProcessOutbound(clock.Add(1), k2, PktSYN)
				if err == nil {
					p1 = o2.ExtPort
					if o1.ExtPort != p1 {
						err = fmt.Errorf("ep=%d ports differ %d/%d", i, o1.ExtPort, p1)
					}
				}
			}
			writeMu.Unlock()
			if err != nil {
				t.Errorf("create ep=%d: %v", i, err)
				return
			}
			// 查询与写操作、查询彼此真正并发；时刻取当前值加偏移，
			// 既不回拨也远小于超时，连接仍存活。
			wg.Add(1)
			go func() {
				defer wg.Done()
				now := clock.Load() + 100000
				if _, ok := tab.Lookup(k1, now); !ok {
					t.Errorf("lookup ep=%d failed", i)
				}
				_ = tab.Stats(now)
			}()
			writeMu.Lock()
			if _, err := tab.ProcessInbound(clock.Add(1), p1, k1.Dst, PktDATA); err != nil {
				t.Errorf("inbound ep=%d: %v", i, err)
			}
			if _, err := tab.ProcessOutbound(clock.Add(1), k2, PktDATA); err != nil {
				t.Errorf("outbound data ep=%d: %v", i, err)
			}
			writeMu.Unlock()
		}(i)
	}
	wg.Wait()

	tab.mu.Lock()
	defer tab.mu.Unlock()
	if len(tab.conns) > tab.n {
		t.Fatalf("connection count %d exceeds N=%d", len(tab.conns), tab.n)
	}
	// 每个外部端口至多属于一个内部端点，且反向索引与正向映射一致。
	for port, owner := range tab.extOwner {
		set := tab.ownerConns[owner]
		if len(set) == 0 {
			t.Fatalf("port %d owned by %v but endpoint has no connections", port, owner)
		}
		byRemote := tab.extIndex[port]
		if len(byRemote) != len(set) {
			t.Fatalf("port %d index size %d != owner conn count %d", port, len(byRemote), len(set))
		}
		for k := range set {
			c := tab.conns[k]
			if c == nil || c.extPort != port {
				t.Fatalf("connection %v missing or port mismatch", k)
			}
			if rk, ok := byRemote[k.Dst]; !ok || rk != k {
				t.Fatalf("reverse index missing connection %v on port %d", k, port)
			}
		}
	}
}

type recordedOp struct {
	outbound bool
	now      int64
	k        ConnKey
	ext      uint16
	remote   Endpoint
	kind     PacketKind
}

// TestEventLogDemo 使用 testing 日志器打印每次事件的输入、输出与判定依据，
// 通过 `go test -v -run TestEventLogDemo ./conntrack` 可直接查看。
func TestEventLogDemo(t *testing.T) {
	tab := newTestTable(t, Config{}, log.New(testWriter{t}, "", 0))
	k := key(100, 9000)
	mustOut := func(now int64, k ConnKey, kind PacketKind) {
		t.Helper()
		if _, err := tab.ProcessOutbound(now, k, kind); err != nil {
			t.Fatalf("outbound %s: %v", kind, err)
		}
	}
	mustIn := func(now int64, port uint16, remote Endpoint, kind PacketKind) {
		t.Helper()
		if _, err := tab.ProcessInbound(now, port, remote, kind); err != nil {
			t.Fatalf("inbound %s: %v", kind, err)
		}
	}
	mustOut(0, k, PktSYN)
	mustOut(2, k, PktDATA)
	mustIn(3, 4000, k.Dst, PktSYN)
	mustOut(10, k, PktDATA)
	mustOut(20, k, PktFIN)
	mustIn(25, 4000, k.Dst, PktDATA)
	if _, err := tab.ProcessInbound(30, 4000, k.Dst, PktDATA); err == nil {
		t.Fatal("packet exactly at expiry must be rejected")
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}

func TestDeterministicReplay(t *testing.T) {
	script := []recordedOp{
		{outbound: true, now: 0, k: key(1, 80), kind: PktSYN},
		{outbound: true, now: 0, k: ConnKey{Src: ep("10.0.0.2", 2), Dst: ep("1.1.1.1", 80)}, kind: PktSYN},
		{outbound: false, now: 1, ext: 4000, remote: key(1, 80).Dst, kind: PktSYN},
		{outbound: true, now: 2, k: key(1, 80), kind: PktDATA},
		{outbound: false, now: 3, ext: 4001, remote: ep("1.1.1.1", 80), kind: PktDATA},
		{outbound: true, now: 4, k: ConnKey{Src: ep("10.0.0.3", 3), Dst: ep("9.9.9.9", 53)}, kind: PktSYN},
		{outbound: false, now: 5, ext: 9999, remote: ep("9.9.9.9", 53), kind: PktDATA},
		{outbound: true, now: 4, k: ConnKey{Src: ep("10.0.0.4", 4), Dst: ep("8.8.8.8", 53)}, kind: PktDATA},
		{outbound: true, now: 6, k: key(1, 80), kind: PktFIN},
		{outbound: false, now: 7, ext: 4000, remote: key(1, 80).Dst, kind: PktDATA},
		{outbound: true, now: 8, k: key(1, 80), kind: PktRST},
	}

	run := func() string {
		tab := newTestTable(t, Config{}, nil)
		var b strings.Builder
		for _, op := range script {
			if op.outbound {
				o, err := tab.ProcessOutbound(op.now, op.k, op.kind)
				fmt.Fprintf(&b, "OUT %d %s: %+v err=%v\n", op.now, op.kind, o, err)
			} else {
				o, err := tab.ProcessInbound(op.now, op.ext, op.remote, op.kind)
				fmt.Fprintf(&b, "IN  %d %s: %+v err=%v\n", op.now, op.kind, o, err)
			}
		}
		fmt.Fprintf(&b, "stats=%+v\n", tab.Stats(8))
		return b.String()
	}

	first, second := run(), run()
	if first != second {
		t.Fatalf("replay differs:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}
