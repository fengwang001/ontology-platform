package api_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"ontology/api"
	"ontology/conn"
)

type ev func(c *api.Conn) error

func cls(c *api.Conn) error { return c.Close() }
func dat(c *api.Conn) error { return c.RecvData() }
func rst(c *api.Conn) error { return c.RecvRST() }
func ack(n int64) ev        { return func(c *api.Conn) error { return c.RecvACK(n) } }
func fin(n int64) ev        { return func(c *api.Conn) error { return c.RecvFIN(n) } }
func tick(n int64) ev       { return func(c *api.Conn) error { return c.Tick(n) } }

type step struct {
	ev    ev
	state string
	enter int64
	f, a  int
}

// mk 依次执行事件，构造指定状态的连接。
func mk(events ...ev) *api.Conn {
	c := api.New(50)
	for _, e := range events {
		e(c)
	}
	return c
}

func snap(c *api.Conn) string {
	return fmt.Sprintf("%s/%d/%d/%d", c.State(), c.EnterTime(), c.SentFIN(), c.SentACK())
}

func run(t *testing.T, c *api.Conn, steps []step) {
	for i, s := range steps {
		if err := s.ev(c); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if want := fmt.Sprintf("%s/%d/%d/%d", s.state, s.enter, s.f, s.a); snap(c) != want {
			t.Fatalf("step %d: got %s want %s", i, snap(c), want)
		}
	}
}

// TestTransitionTable 不变量 1：事件序列后的状态与转移表逐步推演一致。
func TestTransitionTable(t *testing.T) {
	cases := map[string][]step{
		"active-close":         {{cls, "FIN_WAIT_1", 0, 1, 0}, {ack(0), "FIN_WAIT_2", 0, 1, 0}, {fin(10), "TIME_WAIT", 10, 1, 1}, {tick(110), "CLOSED", 10, 1, 1}},
		"passive-close":        {{fin(0), "CLOSE_WAIT", 0, 0, 1}, {cls, "LAST_ACK", 0, 1, 1}, {ack(1), "CLOSED", 0, 1, 1}},
		"simultaneous-close":   {{cls, "FIN_WAIT_1", 0, 1, 0}, {fin(1), "CLOSING", 0, 1, 1}, {ack(2), "TIME_WAIT", 2, 1, 1}, {tick(102), "CLOSED", 2, 1, 1}},
		"rst-from-established": {{rst, "CLOSED", 0, 0, 0}},
	}
	for name, steps := range cases {
		t.Run(name, func(t *testing.T) { run(t, api.New(50), steps) })
	}
}

// TestHalfCloseAndClosed 不变量 2：半关闭可收数据；CLOSED 拒绝一切报文。
func TestHalfCloseAndClosed(t *testing.T) {
	for name, c := range map[string]*api.Conn{"FIN_WAIT_2": mk(cls, ack(0)), "CLOSE_WAIT": mk(fin(0))} {
		if err := c.RecvData(); err != nil || c.State() != name {
			t.Fatalf("%s RecvData: err=%v state=%s", name, err, c.State())
		}
	}
	closed := mk(rst)
	for _, e := range []ev{ack(1), fin(1), dat, rst} {
		if e(closed) == nil {
			t.Fatal("CLOSED must reject ACK/FIN/DATA/RST")
		}
	}
}

// TestTimeWaitRules 不变量 3：TIME_WAIT 仅由到期 Tick 或 RST 离开；迟到 FIN 重启计时。
func TestTimeWaitRules(t *testing.T) {
	run(t, api.New(50), []step{ // 2MSL=100
		{cls, "FIN_WAIT_1", 0, 1, 0}, {ack(0), "FIN_WAIT_2", 0, 1, 0}, {fin(100), "TIME_WAIT", 100, 1, 1},
		{fin(150), "TIME_WAIT", 150, 1, 2}, {tick(249), "TIME_WAIT", 150, 1, 2}, {tick(250), "CLOSED", 150, 1, 2},
	})
	if r := mk(cls, ack(0), fin(0)); r.RecvRST() != nil || r.State() != "CLOSED" {
		t.Fatalf("RST must leave TIME_WAIT without waiting: %s", r.State())
	}
}

// TestIllegalNoSideEffect 不变量 4：四类错误互不相同，拒绝后零副作用且仍可使用。
func TestIllegalNoSideEffect(t *testing.T) {
	cases := []struct {
		name string
		c    *api.Conn
		ev   ev
		want error
	}{
		{"illegal-ack", mk(), ack(0), conn.ErrIllegalACK},
		{"illegal-fin-closewait", mk(fin(0)), fin(1), conn.ErrIllegalFIN},
		{"illegal-fin-lastack", mk(fin(0), cls), fin(1), conn.ErrIllegalFIN},
		{"illegal-data-closing", mk(cls, fin(0)), dat, conn.ErrIllegalData},
		{"illegal-data-timewait", mk(cls, ack(0), fin(0)), dat, conn.ErrIllegalData},
		{"clock-backwards", mk(fin(10)), tick(9), conn.ErrClockBack},
	}
	sentinels := map[error]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := snap(tc.c)
			if err := tc.ev(tc.c); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			sentinels[tc.want] = true
			if snap(tc.c) != before {
				t.Fatal("illegal event left side effects")
			}
			if err := tc.c.RecvRST(); err != nil || tc.c.State() != "CLOSED" {
				t.Fatalf("conn unusable after rejection: %v %s", err, tc.c.State())
			}
		})
	}
	if len(sentinels) != 4 {
		t.Fatalf("four error kinds must be distinct, got %d", len(sentinels))
	}
}

// TestConcurrentReaders 并发只读：N 个 goroutine 的快照逐字段相同。
func TestConcurrentReaders(t *testing.T) {
	c := mk(cls, ack(0), fin(7))
	read := func() string { return snap(c) + fmt.Sprint(api.SelfCheck()) }
	want := read()
	if api.SelfCheck() != nil {
		t.Fatal("SelfCheck failed")
	}
	res := make([]string, 64)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range res {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; res[i] = read() }()
	}
	close(start)
	wg.Wait()
	for _, g := range res {
		if g != want {
			t.Fatal("mismatched concurrent snapshot")
		}
	}
}
