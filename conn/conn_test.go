package conn_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/cert"
	"ontology/conn"
	"ontology/rotate"
)

func newPair(t *testing.T, g, ttl, w int64) (*rotate.Service, *conn.Manager) {
	t.Helper()
	s, err := rotate.New(g, ttl, w)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s, conn.New(s)
}

func isErr(t *testing.T, err error, target error, msg string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: got %v want %v", msg, err, target)
	}
}

// TestAdmitOrder 严格验证准入七连判的优先级。
func TestAdmitOrder(t *testing.T) {
	s, m := newPair(t, 1000, 1000, 1_000_000_000)

	// 序列号不存在。
	_, err := m.Connect("d", "ghost", 0)
	isErr(t, err, cert.ErrUnknown, "unknown")

	// 准备一张属于 other 的 Active 证（nb=0,na=100000）。
	if _, err := s.Issue("other", "c", 0, 100000, 0); err != nil {
		t.Fatal(err)
	}
	// 不属于该设备（在其它状态判定之前）。
	_, err = m.Connect("d", "c", 500)
	isErr(t, err, cert.ErrMismatch, "mismatch")

	// 吊销：Revoked 优先于时间相关判定（刻意选过期时间）。
	if _, err := s.Revoke("c", 1); err != nil {
		t.Fatal(err)
	}
	_, err = m.Connect("other", "c", 200000)
	isErr(t, err, cert.ErrRevoked, "revoked beats expired")

	// 构造一张会 Retired 的证：确认后宽限到期。
	s2, m2 := newPair(t, 10, 1000, 1_000_000_000)
	if _, err := s2.Issue("d", "a", 0, 100000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.Issue("d", "b", 0, 100000, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := m2.Connect("d", "b", 10); err != nil { // a Retiring, retireAt=min(20,...)=20
		t.Fatal(err)
	}
	if _, err := s2.Issue("z", "zz", 0, 10, 20); err != nil { // 结算 a -> Retired
		t.Fatal(err)
	}
	_, err = m2.Connect("d", "a", 20)
	isErr(t, err, cert.ErrRetired, "retired beats notyet/expired")

	// Lapsed 优先于时间判定。
	s3, m3 := newPair(t, 100, 50, 1000)
	if _, err := s3.Issue("d", "a", 0, 1000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s3.Issue("d", "p", 0, 2000, 500); err != nil { // lapseAt=550
		t.Fatal(err)
	}
	if _, err := s3.Issue("tick", "t1", 0, 10, 550); err != nil { // 结算 p -> Lapsed
		t.Fatal(err)
	}
	_, err = m3.Connect("d", "p", 550)
	isErr(t, err, cert.ErrLapsed, "lapsed beats time")

	// not yet vs expired 次序。
	s4, m4 := newPair(t, 100, 100, 100)
	if _, err := s4.Issue("d", "f", 100, 200, 0); err != nil {
		t.Fatal(err)
	}
	_, err = m4.Connect("d", "f", 99)
	isErr(t, err, cert.ErrNotYet, "not yet")
	_, err = m4.Connect("d", "f", 200)
	isErr(t, err, cert.ErrExpired, "expired at equality")
	if _, err := m4.Connect("d", "f", 100); err != nil { // nb 取等允许
		t.Fatalf("nb equality must pass: %v", err)
	}
}

// TestSessionTakeoverAndKick 覆盖接管不踢、Disconnect、踢线唯一性。
func TestSessionTakeoverAndKick(t *testing.T) {
	s, m := newPair(t, 1000, 1000, 1_000_000_000)
	if _, err := s.Issue("d", "a", 0, 100000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Issue("d", "b", 0, 100000, 10); err != nil {
		t.Fatal(err)
	}
	if k, err := m.Connect("d", "a", 20); err != nil || len(k) != 0 {
		t.Fatalf("connect a: %v %v", err, k)
	}
	if k, err := m.Connect("d", "b", 30); err != nil || len(k) != 0 {
		t.Fatalf("takeover by b must not kick old session: %v %v", err, k)
	}
	if ser, ok := m.SessionSerial("d"); !ok || ser != "b" {
		t.Fatalf("session=%q,%v", ser, ok)
	}

	// 吊销当前会话证 -> 踢线。
	k, err := s.Revoke("b", 40)
	if err != nil || !reflect.DeepEqual(k, []string{"d"}) {
		t.Fatalf("revoke b: %v %v", err, k)
	}
	if _, ok := m.SessionSerial("d"); ok {
		t.Fatal("session must be gone")
	}

	// 无会话 Disconnect。
	_, err = m.Disconnect("d", 50)
	isErr(t, err, cert.ErrNoSession, "no session")

	// 再连接成功后正常断开。
	if _, err := m.Connect("d", "a", 60); err != nil { // a 是 Retiring
		t.Fatalf("reconnect via retiring: %v", err)
	}
	if k, err := m.Disconnect("d", 70); err != nil || len(k) != 0 {
		t.Fatalf("disconnect: %v %v", err, k)
	}
	if _, ok := m.SessionSerial("d"); ok {
		t.Fatal("session still present")
	}
}

// TestRejectedSettlementRollsBack 被拒操作的入口结算不落地、不踢线。
func TestRejectedSettlementRollsBack(t *testing.T) {
	s, m := newPair(t, 40, 100, 100)
	if _, err := s.Issue("d", "s1", 0, 1000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Issue("d", "s2", 950, 2000, 900); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Connect("d", "s2", 950); err != nil { // s1 Retiring@990
		t.Fatal(err)
	}
	if _, err := m.Connect("d", "s1", 989); err != nil { // 会话挂在 s1
		t.Fatal(err)
	}

	// now=990 去断一个“其它设备”的会话：入口会结算 s1->Retired 并踢线，
	// 但本操作因 ErrNoSession 被拒，全部回滚（状态、会话、时钟、踢线）。
	k, err := m.Disconnect("other", 990)
	if !errors.Is(err, cert.ErrNoSession) {
		t.Fatalf("want ErrNoSession, got %v", err)
	}
	if len(k) != 0 {
		t.Fatalf("rejected op must not report kicks, got %v", k)
	}
	if ser, ok := m.SessionSerial("d"); !ok || ser != "s1" {
		t.Fatalf("session rolled back? ser=%q ok=%v", ser, ok)
	}
	// 990 仍未被接受，989 之后的时钟保持 989；989 再操作合法。
	if _, err := m.Disconnect("d", 989); err != nil {
		t.Fatalf("clock must still accept 989: %v", err)
	}
}

// TestInvalidAndClockBack 校验 Connect/Disconnect 的参数与时钟回退居前。
func TestInvalidAndClockBack(t *testing.T) {
	s, m := newPair(t, 10, 10, 10)
	if _, err := s.Issue("d", "a", 0, 1000, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Connect("", "a", 0); !errors.Is(err, cert.ErrInvalid) {
		t.Fatalf("empty dev: %v", err)
	}
	if _, err := m.Connect("d", "", 0); !errors.Is(err, cert.ErrInvalid) {
		t.Fatalf("empty serial: %v", err)
	}
	if _, err := m.Connect("d", "a", -1); !errors.Is(err, cert.ErrInvalid) {
		t.Fatalf("bad time: %v", err)
	}
	if _, err := m.Disconnect("d", 0); !errors.Is(err, cert.ErrNoSession) { // 无会话但时钟合法
		t.Fatalf("want ErrNoSession, got %v", err)
	}
	if _, err := m.Connect("d", "a", 0); err != nil {
		t.Fatal(err)
	}
	// 时钟回退在状态判定之前。
	if _, err := m.Connect("d", "a", -1); !errors.Is(err, cert.ErrInvalid) {
		t.Fatalf("invalid beats clockback: %v", err)
	}
}
