package rotate_test

import (
	"errors"
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

func mustConnect(t *testing.T, cn *conn.Service, dev, serial string, now int64) []string {
	t.Helper()
	kicked, err := cn.Connect(dev, serial, now)
	if err != nil {
		t.Fatalf("Connect(%s,%s,now=%d): %v", dev, serial, now, err)
	}
	return kicked
}

func wantErr(t *testing.T, what string, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: err=%v, want %v", what, err, want)
	}
}

func stateOf(t *testing.T, st *cert.Store, serial string) cert.State {
	t.Helper()
	c, ok := st.Lookup(serial)
	if !ok {
		t.Fatalf("cert %s not found", serial)
	}
	return c.State
}

func certOf(t *testing.T, st *cert.Store, serial string) cert.Cert {
	t.Helper()
	c, ok := st.Lookup(serial)
	if !ok {
		t.Fatalf("cert %s not found", serial)
	}
	return c
}

// 规范示例：G=40，T=100，W=100 的完整走查。
func TestSpecExampleWalkthrough(t *testing.T) {
	st, r, cn := newSystem(t, 40, 100, 100)

	mustIssue(t, r, "d", "s1", 0, 1000, 0)
	if got := stateOf(t, st, "s1"); got != cert.Active {
		t.Fatalf("s1=%s, want Active（首次签发直接上位）", got)
	}

	_, err := r.Issue("d", "s2", 950, 2000, 899)
	wantErr(t, "now=899 未到续期窗口", err, cert.ErrTooEarly)

	mustIssue(t, r, "d", "s2", 950, 2000, 900)
	if got := certOf(t, st, "s2").LapseAt; got != 1050 {
		t.Fatalf("lapseAt=%d, want 1050（max(900,950)+100）", got)
	}

	_, err = cn.Connect("d", "s2", 949)
	wantErr(t, "now=949 未生效", err, cert.ErrNotYet)

	mustConnect(t, cn, "d", "s2", 950)
	if got := stateOf(t, st, "s2"); got != cert.Active {
		t.Fatalf("s2=%s, want Active（用后确认上位）", got)
	}
	if c := certOf(t, st, "s1"); c.State != cert.Retiring || c.RetireAt != 990 {
		t.Fatalf("s1=%s retireAt=%d, want Retiring retireAt=990（min(950+40,1000)）", c.State, c.RetireAt)
	}

	if kicked := mustConnect(t, cn, "d", "s1", 989); len(kicked) != 0 {
		t.Fatalf("接管会话不应进入踢线清单, kicked=%v", kicked)
	}

	// now=990：s1 在入口结算中应成为 Retired，但本次 Connect 被拒，不落地。
	kicked, err := cn.Connect("d", "s1", 990)
	wantErr(t, "now=990 s1 已退", err, cert.ErrRetired)
	if kicked != nil {
		t.Fatalf("被拒操作不应报告 Kicked, got %v", kicked)
	}
	if got := stateOf(t, st, "s1"); got != cert.Retiring {
		t.Fatalf("被拒后 s1=%s, want Retiring（结算不落地）", got)
	}
	if got := st.Now(); got != 989 {
		t.Fatalf("被拒后 now=%d, want 989（时钟不推进）", got)
	}

	// 其后第一个被接受的操作把 s1 结算为 Retired 并报告 Kicked=[d]。
	kicked, err = r.Issue("d2", "x1", 0, 10, 990)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kicked, []string{"d"}) {
		t.Fatalf("kicked=%v, want [d]", kicked)
	}
	if got := stateOf(t, st, "s1"); got != cert.Retired {
		t.Fatalf("s1=%s, want Retired", got)
	}
	if _, ok := st.SessionOf("d"); ok {
		t.Fatal("d 的会话应已被踢")
	}
}

// 续期窗口取等：now == na-W 恰等允许，早一秒拒绝。
func TestIssueRenewalWindowBoundary(t *testing.T) {
	cases := []struct {
		name    string
		now     int64
		wantErr error
	}{
		{"早一秒", 899, cert.ErrTooEarly},
		{"恰等窗口", 900, nil},
		{"窗口内", 950, nil},
		{"Active已过期也允许", 1500, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, r, _ := newSystem(t, 40, 100, 100)
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
			_, err := r.Issue("d", "s2", 0, 2000, tc.now)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("now=%d: %v", tc.now, err)
				}
			} else {
				wantErr(t, "Issue", err, tc.wantErr)
			}
		})
	}
}

// lapseAt 以 max(now, nb) 起算。
func TestIssueLapseAtBase(t *testing.T) {
	cases := []struct {
		name string
		nb   int64
		now  int64
		want int64
	}{
		{"nb大于now", 950, 900, 1050}, // max(900,950)+100
		{"now大于nb", 100, 500, 600},  // max(500,100)+100
		{"now等于nb", 500, 500, 600},  // max(500,500)+100
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, r, _ := newSystem(t, 40, 100, 1000)
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
			mustIssue(t, r, "d", "s2", tc.nb, 2000, tc.now)
			if got := certOf(t, st, "s2").LapseAt; got != tc.want {
				t.Fatalf("lapseAt=%d, want %d", got, tc.want)
			}
		})
	}
}

// retireAt 被原 Active 的 na 封顶：min(now+G, na)。
func TestConfirmRetireAtCappedByNa(t *testing.T) {
	cases := []struct {
		name       string
		connectNow int64
		want       int64
	}{
		{"宽限内", 950, 990},    // min(950+40, 1000)
		{"被na封顶", 970, 1000}, // min(970+40, 1000)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, r, cn := newSystem(t, 40, 100, 100)
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
			mustIssue(t, r, "d", "s2", 0, 2000, 900)
			mustConnect(t, cn, "d", "s2", tc.connectNow)
			if c := certOf(t, st, "s1"); c.State != cert.Retiring || c.RetireAt != tc.want {
				t.Fatalf("s1=%s retireAt=%d, want Retiring retireAt=%d", c.State, c.RetireAt, tc.want)
			}
		})
	}
}

// 三证并存时确认新证：最老的 Retiring 立即成为 Retired 并踢线。
func TestThreeCertsOldestRetiredImmediately(t *testing.T) {
	st, r, cn := newSystem(t, 40, 100, 2000)
	mustIssue(t, r, "d", "s1", 0, 1000, 0)
	mustIssue(t, r, "d", "s2", 0, 2000, 0)
	mustConnect(t, cn, "d", "s2", 50) // s2 Active，s1 Retiring(retireAt=90)
	mustIssue(t, r, "d", "s3", 0, 3000, 60)
	mustConnect(t, cn, "d", "s1", 60) // 会话改用 s1（Retiring 仍可用）

	kicked := mustConnect(t, cn, "d", "s3", 70) // 确认 s3：s1 立即 Retired
	if !reflect.DeepEqual(kicked, []string{"d"}) {
		t.Fatalf("kicked=%v, want [d]（使用 s1 的会话被踢）", kicked)
	}
	if got := stateOf(t, st, "s1"); got != cert.Retired {
		t.Fatalf("s1=%s, want Retired", got)
	}
	if c := certOf(t, st, "s2"); c.State != cert.Retiring || c.RetireAt != 110 {
		t.Fatalf("s2=%s retireAt=%d, want Retiring retireAt=110", c.State, c.RetireAt)
	}
	if got := stateOf(t, st, "s3"); got != cert.Active {
		t.Fatalf("s3=%s, want Active", got)
	}
	if serial, ok := st.SessionOf("d"); !ok || serial != "s3" {
		t.Fatalf("session=%q,%v, want s3", serial, ok)
	}
}

// 吊销 Active 后 Pending 上位：不产生 Retiring。
func TestRevokeActiveThenPendingPromotes(t *testing.T) {
	st, r, cn := newSystem(t, 40, 100, 2000)
	mustIssue(t, r, "d", "s1", 0, 1000, 0)
	mustIssue(t, r, "d", "s2", 0, 2000, 0)
	mustConnect(t, cn, "d", "s1", 10)

	kicked, err := r.Revoke("s1", 20)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(kicked, []string{"d"}) {
		t.Fatalf("kicked=%v, want [d]（使用 s1 的会话被踢）", kicked)
	}
	if got := stateOf(t, st, "s2"); got != cert.Pending {
		t.Fatalf("吊销 Active 后 s2=%s, want Pending（保留原状）", got)
	}

	mustConnect(t, cn, "d", "s2", 30)
	if got := stateOf(t, st, "s2"); got != cert.Active {
		t.Fatalf("s2=%s, want Active", got)
	}
	if c := certOf(t, st, "s1"); c.State != cert.Revoked {
		t.Fatalf("s1=%s, want Revoked（不转为 Retiring）", c.State)
	}
}

// 吊销 Active 不会使 Retiring 回升；设备无 Active 时新证直接 Active。
func TestRevokeActiveRetiringNotPromoted(t *testing.T) {
	st, r, cn := newSystem(t, 40, 100, 2000)
	mustIssue(t, r, "d", "s1", 0, 1000, 0)
	mustIssue(t, r, "d", "s2", 0, 2000, 0)
	mustConnect(t, cn, "d", "s2", 50) // s2 Active，s1 Retiring(retireAt=90)

	if _, err := r.Revoke("s2", 60); err != nil {
		t.Fatal(err)
	}
	if c := certOf(t, st, "s1"); c.State != cert.Retiring || c.RetireAt != 90 {
		t.Fatalf("s1=%s retireAt=%d, want Retiring retireAt=90（不回升）", c.State, c.RetireAt)
	}

	// 设备没有 Active，新证直接 Active。
	mustIssue(t, r, "d", "s3", 0, 3000, 70)
	if got := stateOf(t, st, "s3"); got != cert.Active {
		t.Fatalf("s3=%s, want Active", got)
	}

	// 到 retireAt 后 s1 照常成为 Retired。
	if _, err := r.Issue("d2", "x1", 0, 10, 90); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, st, "s1"); got != cert.Retired {
		t.Fatalf("s1=%s, want Retired", got)
	}
}

// Issue 拒绝次序：ErrInvalid > ErrClockBack > ErrDupSerial > ErrPendingExists > ErrTooEarly。
func TestIssueRejectOrder(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, r *rotate.Service)
		dev   string
		ser   string
		nb    int64
		na    int64
		now   int64
		want  error
	}{
		{"空序列号", nil, "d", "", 0, 10, 0, cert.ErrInvalid},
		{"空设备", nil, "", "s", 0, 10, 0, cert.ErrInvalid},
		{"nb>=na", nil, "d", "s", 10, 10, 0, cert.ErrInvalid},
		{"now越界", nil, "d", "s", 0, 10, -1, cert.ErrInvalid},
		{"非法优先于时钟回退", func(t *testing.T, r *rotate.Service) {
			mustIssue(t, r, "d", "s1", 0, 1000, 100)
		}, "d", "", 0, 10, 50, cert.ErrInvalid},
		{"时钟回退", func(t *testing.T, r *rotate.Service) {
			mustIssue(t, r, "d", "s1", 0, 1000, 100)
		}, "d", "s2", 0, 10, 50, cert.ErrClockBack},
		{"时钟回退优先于重复序列号", func(t *testing.T, r *rotate.Service) {
			mustIssue(t, r, "d", "s1", 0, 1000, 100)
		}, "d", "s1", 0, 10, 50, cert.ErrClockBack},
		{"重复序列号", func(t *testing.T, r *rotate.Service) {
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
		}, "d", "s1", 0, 10, 0, cert.ErrDupSerial},
		{"重复序列号含终态", func(t *testing.T, r *rotate.Service) {
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
			if _, err := r.Revoke("s1", 10); err != nil {
				t.Fatal(err)
			}
		}, "d", "s1", 0, 10, 20, cert.ErrDupSerial},
		{"已有Pending", func(t *testing.T, r *rotate.Service) {
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
			mustIssue(t, r, "d", "s2", 0, 2000, 900)
		}, "d", "s3", 0, 3000, 950, cert.ErrPendingExists},
		{"未到续期窗口", func(t *testing.T, r *rotate.Service) {
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
		}, "d", "s2", 0, 2000, 899, cert.ErrTooEarly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, r, _ := newSystem(t, 40, 100, 100)
			if tc.setup != nil {
				tc.setup(t, r)
			}
			_, err := r.Issue(tc.dev, tc.ser, tc.nb, tc.na, tc.now)
			wantErr(t, "Issue", err, tc.want)
		})
	}
}

// 被拒的 Issue 不推进时钟。
func TestRejectedIssueKeepsClock(t *testing.T) {
	st, r, _ := newSystem(t, 40, 100, 100)
	mustIssue(t, r, "d", "s1", 0, 1000, 100)
	if _, err := r.Issue("d", "s1", 0, 10, 5000); !errors.Is(err, cert.ErrDupSerial) {
		t.Fatalf("want ErrDupSerial, got %v", err)
	}
	if got := st.Now(); got != 100 {
		t.Fatalf("now=%d, want 100（被拒不推进）", got)
	}
	// 时钟未推进到 5000，now=900 仍被接受。
	mustIssue(t, r, "d", "s2", 0, 2000, 900)
}

// 吊销：未知序列号报 ErrUnknown，终态证书报 ErrFinal。
func TestRevokeErrors(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) string
		want  error
	}{
		{"未知序列号", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) string {
			return "nope"
		}, cert.ErrUnknown},
		{"已Revoked", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) string {
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
			if _, err := r.Revoke("s1", 10); err != nil {
				t.Fatal(err)
			}
			return "s1"
		}, cert.ErrFinal},
		{"已Retired", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) string {
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
			mustIssue(t, r, "d", "s2", 0, 2000, 900)
			mustConnect(t, cn, "d", "s2", 950) // s1 Retiring(retireAt=990)
			mustIssue(t, r, "e", "x1", 0, 10, 990)
			if got := stateOf(t, st, "s1"); got != cert.Retired {
				t.Fatalf("s1=%s, want Retired", got)
			}
			return "s1"
		}, cert.ErrFinal},
		{"已Lapsed", func(t *testing.T, st *cert.Store, r *rotate.Service, cn *conn.Service) string {
			mustIssue(t, r, "d", "s1", 0, 1000, 0)
			mustIssue(t, r, "d", "s2", 950, 2000, 900) // lapseAt=1050
			mustIssue(t, r, "e", "x1", 0, 10, 1050)
			if got := stateOf(t, st, "s2"); got != cert.Lapsed {
				t.Fatalf("s2=%s, want Lapsed", got)
			}
			return "s2"
		}, cert.ErrFinal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st, r, cn := newSystem(t, 40, 100, 100)
			serial := tc.setup(t, st, r, cn)
			_, err := r.Revoke(serial, 2000)
			wantErr(t, "Revoke", err, tc.want)
		})
	}
}

// Pending 弃置超期成为 Lapsed 后，设备可再 Issue。
func TestPendingLapseThenReissue(t *testing.T) {
	st, r, cn := newSystem(t, 40, 100, 100)
	mustIssue(t, r, "d", "s1", 0, 1000, 0)
	mustIssue(t, r, "d", "s2", 950, 2000, 900) // lapseAt=1050

	mustIssue(t, r, "d2", "x1", 0, 10, 1050) // 入口结算使 s2 Lapsed
	if got := stateOf(t, st, "s2"); got != cert.Lapsed {
		t.Fatalf("s2=%s, want Lapsed", got)
	}
	_, err := cn.Connect("d", "s2", 1050)
	wantErr(t, "Lapsed 证书准入", err, cert.ErrLapsed)

	// s1 仍 Active，可再签发 Pending。
	mustIssue(t, r, "d", "s3", 0, 3000, 1050)
	if got := stateOf(t, st, "s3"); got != cert.Pending {
		t.Fatalf("s3=%s, want Pending", got)
	}
}
