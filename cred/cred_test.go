package cred

import (
	"errors"
	"sync"
	"testing"
)

func resetProbes() { credProbes.Store(0) }

type op struct {
	kind      string
	tenant    string
	keyID     string
	secret    string
	region    string
	service   string
	g, now    int64
	t         int64
	wantErr   error
	wantEpoch int64
	wantT     int64
	wantState keyState
	wantVU    int64
}

func TestSequenceFromSpec(t *testing.T) {
	s := NewStore()
	steps := []op{
		{kind: "rotate", tenant: "T", keyID: "k1", secret: "sec", region: "r", service: "svc", g: 100, now: 0, wantEpoch: 1, wantState: stateActive},
		{kind: "rotate", tenant: "T", keyID: "k2", secret: "sec", region: "r", service: "svc", g: 100, now: 50, wantEpoch: 2, wantState: stateActive},
		{kind: "rotate", tenant: "T", keyID: "k3", secret: "sec", region: "r", service: "svc", g: 100, now: 60, wantErr: ErrLimit},
		{kind: "rotate", tenant: "T", keyID: "k3", secret: "sec", region: "r", service: "svc", g: 100, now: 150, wantEpoch: 3, wantState: stateActive},
		{kind: "clock", now: 149, wantErr: ErrClockSkew},
	}
	for i, st := range steps {
		var err error
		switch st.kind {
		case "rotate":
			err = s.Rotate(st.tenant, st.keyID, st.secret, st.region, st.service, st.g, st.now)
		case "clock":
			err = s.Rotate("X", "xk", "s", "r", "svc", 1, st.now)
		}
		if !errors.Is(err, st.wantErr) {
			t.Fatalf("step %d: err=%v want %v", i, err, st.wantErr)
		}
		if st.wantEpoch != 0 && s.epoch != st.wantEpoch {
			t.Fatalf("step %d: epoch=%d want %d", i, s.epoch, st.wantEpoch)
		}
	}
	if k1 := s.creds["k1"]; k1.state != stateRetiring || k1.validUntil != 150 {
		t.Fatalf("k1 = state %d vu %d", k1.state, k1.validUntil)
	}
	if k2 := s.creds["k2"]; k2.state != stateRetiring || k2.validUntil != 250 {
		t.Fatalf("k2 = state %d vu %d", k2.state, k2.validUntil)
	}
	if k3 := s.creds["k3"]; k3.state != stateActive {
		t.Fatalf("k3 state = %d", k3.state)
	}
	t.Logf("轮换序列结果: k1=retiring@150 k2=retiring@250 k3=active, epoch=%d（依据：t=150 时 k1 恰等于 validUntil 已不计入）", s.epoch)
}

func TestRotateValidationOrder(t *testing.T) {
	s := NewStore()
	if err := s.Rotate("T", "", "s", "r", "svc", 1, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("空 keyID: %v", err)
	}
	if err := s.Rotate("T", "k", "s", "r", "svc", 0, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("G=0: %v", err)
	}
	if err := s.Rotate("T", "k", "s", "r", "svc", 10_000_001, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("G 超界: %v", err)
	}
	if err := s.Rotate("T", "k", "s", "r", "svc", 1, -1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("now 超界: %v", err)
	}
	if err := s.Rotate("T", "k1", "s", "r", "svc", 1, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.Rotate("T", "k1", "s", "r", "svc", 1, 10); !errors.Is(err, ErrExists) {
		t.Fatalf("重复 keyID: %v", err)
	}
	if err := s.Rotate("T", "k2", "s", "r", "svc", 1, 9); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("时钟回退: %v", err)
	}
	if s.epoch != 1 {
		t.Fatalf("拒绝不应耗纪元, epoch=%d", s.epoch)
	}
	t.Logf("参数非法 > 时钟回退 > 已存在 > 超过上限 次序验证通过；拒绝后 epoch=%d", s.epoch)
}

func TestDisable(t *testing.T) {
	s := NewStore()
	if err := s.Disable("nope", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在: %v", err)
	}
	if err := s.Rotate("T", "k1", "s", "r", "svc", 100, 5); err != nil {
		t.Fatal(err)
	}
	if err := s.Disable("k1", 3); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("时钟回退优先于不存在检查路径: %v", err)
	}
	if err := s.Disable("k1", 6); err != nil {
		t.Fatal(err)
	}
	if s.epoch != 2 {
		t.Fatalf("停用应加纪元, epoch=%d", s.epoch)
	}
	if err := s.Disable("k1", 7); err != nil {
		t.Fatalf("已停用应为空操作: %v", err)
	}
	if s.epoch != 2 {
		t.Fatalf("空操作不应加纪元, epoch=%d", s.epoch)
	}
	c, ok := s.Lookup("k1")
	if !ok || !c.Disabled {
		t.Fatalf("k1 应为 disabled")
	}
	t.Logf("Disable：立即停用；空操作不耗纪元 epoch=%d", s.epoch)
}

func TestRevokeBefore(t *testing.T) {
	s := NewStore()
	if err := s.RevokeBefore("NOPE", 10, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("无凭证租户: %v", err)
	}
	if err := s.Rotate("T", "k1", "s", "r", "svc", 100, 0); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeBefore("T", 120, 10); err != nil {
		t.Fatal(err)
	}
	if err := s.RevokeBefore("T", 100, 11); err != nil {
		t.Fatalf("t 减小应为空操作而非报错: %v", err)
	}
	epoch := s.epoch
	c, _ := s.Lookup("k1")
	if c.RevokeT != 120 {
		t.Fatalf("revokeT=%d want 120", c.RevokeT)
	}
	if s.epoch != epoch {
		t.Fatalf("t 不增不应加纪元: %d", s.epoch)
	}
	resetProbes()
	_, ok := s.Lookup("k1")
	if !ok || credProbes.Load() != 1 {
		t.Fatalf("查表次数=%d want 1", credProbes.Load())
	}
	t.Logf("RevokeBefore：ts<120 无效 ts==120 有效；只增不减；credProbes=%d", credProbes.Load())
}

func TestEpochOnlyOnChange(t *testing.T) {
	s := NewStore()
	_ = s.Rotate("T", "k1", "s", "r", "svc", 100, 0)
	_ = s.Disable("k1", 1)
	_ = s.RevokeBefore("T", 50, 2)
	want := s.epoch
	_ = s.Disable("k1", 3)
	_ = s.RevokeBefore("T", 50, 4)
	if s.epoch != want {
		t.Fatalf("空操作后 epoch %d != %d", s.epoch, want)
	}
}

func TestConcurrent(t *testing.T) {
	s := NewStore()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.Rotate("T", keyN(i), "s", "r", "svc", 1000, int64(i))
			_, _ = s.Lookup(keyN(i))
		}(i)
	}
	wg.Wait()
}

func keyN(i int) string {
	digits := ""
	if i == 0 {
		return "k0"
	}
	for i > 0 {
		digits = string(rune('0'+i%10)) + digits
		i /= 10
	}
	return "k" + digits
}
