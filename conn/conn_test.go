package conn_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"ontology/cert"
	"ontology/conn"
	"ontology/rotate"
)

func newSystem(t *testing.T, g, confirm, window int64) (*cert.Store, *rotate.Service, *conn.Service) {
	t.Helper()
	st, err := cert.NewStore(g, confirm, window)
	if err != nil {
		t.Fatal(err)
	}
	return st, rotate.New(st), conn.New(st)
}

func mustIssue(t *testing.T, r *rotate.Service, dev, serial string, nb, na, now int64) {
	t.Helper()
	if _, err := r.Issue(dev, serial, nb, na, now); err != nil {
		t.Fatalf("Issue(%s,%s,nb=%d,na=%d,now=%d): %v", dev, serial, nb, na, now, err)
	}
}

func wantConnectErr(t *testing.T, cn *conn.Service, dev, serial string, now int64, want error) {
	t.Helper()
	kicked, err := cn.Connect(dev, serial, now)
	if !errors.Is(err, want) {
		t.Fatalf("Connect(%s,%s,now=%d): err=%v, want %v", dev, serial, now, err, want)
	}
	if kicked != nil {
		t.Fatalf("被拒操作不应报告 Kicked, got %v", kicked)
	}
}

// 准入拒绝次序：ErrUnknown > ErrMismatch > ErrRevoked > ErrRetired >
// ErrLapsed > ErrNotYet > ErrExpired，只报第一个。
func TestConnectAdmissionOrder(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (dev, serial string, now int64)
		want  error
	}{
		{"序列号不存在", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			return "d", "nope", 0
		}, cert.ErrUnknown},
		{"不属于该设备", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			mustIssue(t, r, "d1", "s1", 0, 1000, 0)
			return "d2", "s1", 10
		}, cert.ErrMismatch},
		{"Mismatch优先于Revoked", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			mustIssue(t, r, "d1", "s1", 0, 1000, 0)
			if _, err := r.Revoke("s1", 5); err != nil {
				t.Fatal(err)
			}
			return "d2", "s1", 10
		}, cert.ErrMismatch},
		{"已吊销", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
			if _, err := r.Revoke("s1", 5); err != nil {
				t.Fatal(err)
			}
			return "d", "s1", 10
		}, cert.ErrRevoked},
		{"Revoked优先于Expired", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			mustIssue(t, r, "d", "s1", 0, 100, 0)
			if _, err := r.Revoke("s1", 5); err != nil {
				t.Fatal(err)
			}
			return "d", "s1", 200
		}, cert.ErrRevoked},
		{"已退役", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
			mustIssue(t, r, "d", "s2", 0, 2000, 900)
			if _, err := cn.Connect("d", "s2", 950); err != nil {
				t.Fatal(err)
			}
			mustIssue(t, r, "e", "x1", 0, 10, 990) // 入口结算使 s1 Retired
			return "d", "s1", 990
		}, cert.ErrRetired},
		{"Retired优先于Expired", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			mustIssue(t, r, "d", "s1", 0, 985, 0)
			mustIssue(t, r, "d", "s2", 0, 2000, 900)
			if _, err := cn.Connect("d", "s2", 950); err != nil {
				t.Fatal(err)
			} // s1 Retiring retireAt=min(990,985)=985
			return "d", "s1", 1000 // 同时满足 now>=retireAt 与 now>=na
		}, cert.ErrRetired},
		{"已弃置", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
			mustIssue(t, r, "d", "s2", 950, 2000, 900) // lapseAt=1050
			mustIssue(t, r, "e", "x1", 0, 10, 1050)
			return "d", "s2", 1050
		}, cert.ErrLapsed},
		{"未生效", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			mustIssue(t, r, "d", "s1", 100, 1000, 0)
			return "d", "s1", 99
		}, cert.ErrNotYet},
		{"已过期", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			mustIssue(t, r, "d", "s1", 0, 100, 0)
			return "d", "s1", 100
		}, cert.ErrExpired},
		{"参数非法居前", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			return "", "s1", 0
		}, cert.ErrInvalid},
		{"时钟回退居前", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) (string, string, int64) {
			mustIssue(t, r, "d", "s1", 0, 1000, 100)
			return "d", "nope", 50 // 未知序列号，但时钟回退先报
		}, cert.ErrClockBack},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, r, cn := newSystem(t, 40, 100, 100)
			dev, serial, now := tc.setup(t, st, r, cn)
			wantConnectErr(t, cn, dev, serial, now, tc.want)
		})
	}
}

// 会话接管：旧会话不进入踢线清单。
func TestSessionTakeoverNotKicked(t *testing.T) {
	st, r, cn := newSystem(t, 40, 100, 100)
	mustIssue(t, r, "d", "s1", 0, 1000, 0)
	mustIssue(t, r, "d", "s2", 0, 2000, 900)

	kicked, err := cn.Connect("d", "s1", 950)
	if err != nil || len(kicked) != 0 {
		t.Fatalf("kicked=%v err=%v", kicked, err)
	}
	kicked, err = cn.Connect("d", "s2", 960) // 确认 s2 并接管 s1 的会话
	if err != nil {
		t.Fatal(err)
	}
	if len(kicked) != 0 {
		t.Fatalf("接管不应踢线, kicked=%v", kicked)
	}
	if serial, _ := st.SessionOf("d"); serial != "s2" {
		t.Fatalf("session=%s, want s2", serial)
	}
}

// Disconnect：无会话报 ErrNoSession，断开后可重新连接。
func TestDisconnect(t *testing.T) {
	st, r, cn := newSystem(t, 40, 100, 100)
	mustIssue(t, r, "d", "s1", 0, 1000, 0)

	if _, err := cn.Disconnect("d", 10); !errors.Is(err, cert.ErrNoSession) {
		t.Fatalf("err=%v, want ErrNoSession", err)
	}
	if _, err := cn.Connect("d", "s1", 20); err != nil {
		t.Fatal(err)
	}
	if _, err := cn.Disconnect("d", 30); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.SessionOf("d"); ok {
		t.Fatal("会话应已结束")
	}
	if _, err := cn.Disconnect("d", 40); !errors.Is(err, cert.ErrNoSession) {
		t.Fatalf("err=%v, want ErrNoSession", err)
	}
	if _, err := cn.Connect("d", "s1", 50); err != nil {
		t.Fatal(err)
	}
}

// 使用 Retiring 证书的会话在 retireAt 前有效，到期结算时被踢。
func TestSessionOnRetiringCert(t *testing.T) {
	st, r, cn := newSystem(t, 40, 100, 100)
	mustIssue(t, r, "d", "s1", 0, 1000, 0)
	mustIssue(t, r, "d", "s2", 0, 2000, 900)
	if _, err := cn.Connect("d", "s2", 950); err != nil {
		t.Fatal(err)
	} // s1 Retiring(retireAt=990)
	if _, err := cn.Connect("d", "s1", 960); err != nil {
		t.Fatal(err)
	} // 会话改用 s1

	// 被拒操作不落地踢线：now=990 的 Connect 报 ErrRetired，结算不生效。
	wantConnectErr(t, cn, "d", "s1", 990, cert.ErrRetired)
	if _, ok := st.SessionOf("d"); !ok {
		t.Fatal("被拒操作不应踢掉会话")
	}
	if c, _ := st.Lookup("s1"); c.State != cert.Retiring {
		t.Fatalf("s1=%s, want Retiring（未落地）", c.State)
	}

	// 被拒操作不算数：e 无会话、d 的会话在入口结算中被踢后又随回滚复原。
	kicked, err := cn.Disconnect("e", 990)
	if !errors.Is(err, cert.ErrNoSession) {
		t.Fatalf("err=%v, want ErrNoSession", err)
	}
	if kicked != nil {
		t.Fatalf("被拒操作不报告 Kicked, got %v", kicked)
	}
	kicked, err = cn.Disconnect("d", 990) // d 的会话已在入口结算中被踢
	if !errors.Is(err, cert.ErrNoSession) {
		t.Fatalf("err=%v, want ErrNoSession（会话已被结算踢掉）", err)
	}
	if kicked != nil {
		t.Fatalf("被拒操作不落地, got %v", kicked)
	}
	// 其后第一个被接受的操作把 s1 结算为 Retired 并报告 Kicked=[d]。
	kicked, err = r.Issue("e", "x1", 0, 10, 990)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kicked, []string{"d"}) {
		t.Fatalf("kicked=%v, want [d]", kicked)
	}
	if c, _ := st.Lookup("s1"); c.State != cert.Retired {
		t.Fatalf("s1=%s, want Retired", c.State)
	}
}

// 被拒的 Connect 不推进时钟。
func TestRejectedConnectKeepsClock(t *testing.T) {
	st, r, cn := newSystem(t, 40, 100, 100)
	mustIssue(t, r, "d", "s1", 0, 1000, 100)
	wantConnectErr(t, cn, "d", "s1", 5000, cert.ErrExpired)
	if got := st.Now(); got != 100 {
		t.Fatalf("now=%d, want 100（被拒不推进）", got)
	}
	if _, err := cn.Connect("d", "s1", 500); err != nil {
		t.Fatalf("时钟未推进到 5000，now=500 应被接受: %v", err)
	}
}

// 并发调用：等价于某个串行顺序，不变量保持 —— 每台设备至多一张
// Active/Pending/Retiring，会话所用证书处于 Active 或 Retiring。
func TestConcurrentOps(t *testing.T) {
	st, r, cn := newSystem(t, 40, 100, 100)
	const workers = 8
	const opsEach = 200
	done := make(chan struct{}, workers)
	for w := 0; w < workers; w++ {
		go func(seed int64) {
			defer func() { done <- struct{}{} }()
			rng := rand.New(rand.NewSource(seed))
			now := int64(0)
			for i := 0; i < opsEach; i++ {
				now += rng.Int63n(50)
				dev := fmt.Sprintf("d%d", rng.Intn(6))
				serial := fmt.Sprintf("s%d", rng.Intn(40))
				switch rng.Intn(4) {
				case 0:
					_, _ = r.Issue(dev, fmt.Sprintf("w%d-%d", seed, i), now, now+1+rng.Int63n(1000), now)
				case 1:
					_, _ = cn.Connect(dev, serial, now)
				case 2:
					_, _ = cn.Disconnect(dev, now)
				default:
					_, _ = r.Revoke(serial, now)
				}
			}
		}(int64(w))
	}
	for w := 0; w < workers; w++ {
		<-done
	}
	certs, sessions, _ := st.Dump()
	slots := make(map[string]map[cert.State]int)
	bySerial := make(map[string]cert.Cert, len(certs))
	for _, c := range certs {
		bySerial[c.Serial] = c
		if !c.State.Final() {
			m := slots[c.Dev]
			if m == nil {
				m = make(map[cert.State]int)
				slots[c.Dev] = m
			}
			m[c.State]++
			if m[c.State] > 1 {
				t.Fatalf("设备 %s 状态 %s 超过一张", c.Dev, c.State)
			}
		}
	}
	for dev, serial := range sessions {
		c, ok := bySerial[serial]
		if !ok {
			t.Fatalf("设备 %s 会话引用未知证书 %s", dev, serial)
		}
		if c.Dev != dev {
			t.Fatalf("设备 %s 会话引用他机证书 %s", dev, serial)
		}
		if c.State != cert.Active && c.State != cert.Retiring {
			t.Fatalf("设备 %s 会话证书 %s 状态 %s", dev, serial, c.State)
		}
	}
}
