package deploy

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func TestRandomReplay(t *testing.T) {
	configs := []EnvConfig{
		{Name: "prod", K: 2, TTL: 12, T: 10},
		{Name: "stage", K: 1, TTL: 20, T: 15},
		{Name: "dev", K: 0, TTL: 1, T: 8},
	}
	users := []Caller{
		{User: "alice", Permissions: Deploy | Rollback | Admin},
		{User: "bob", Permissions: Deploy | Approve},
		{User: "carol", Permissions: Approve},
		{User: "dave", Permissions: Deploy},
		{User: "guest", Permissions: Approve | Rollback},
		{User: ""},
	}
	for seed := int64(1); seed <= 1500; seed++ {
		t.Run(fmt.Sprintf("seed=%04d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			c, _ := New(configs)
			model := newNaiveModel(configs)
			now := int64(0)
			for step := 0; step < 40; step++ {
				in := op{
					name:   []string{"request", "approve", "finish", "cancel", "tick"}[r.Intn(5)],
					now:    now,
					env:    configs[r.Intn(len(configs))].Name,
					id:     int64(1 + r.Intn(6)),
					ver:    int64(1 + r.Intn(9)),
					caller: users[r.Intn(len(users))],
					ok:     r.Intn(2) == 0,
				}
				if r.Intn(6) == 0 {
					in.rollback = true
				}
				if r.Intn(8) == 0 {
					in.now += int64(r.Intn(35))
				} else if r.Intn(20) == 0 {
					in.now--
				}
				if in.now < 0 {
					in.now = 0
				}
				if in.name == "tick" {
					in.caller = Caller{}
				}
				got := applyProduction(c, in)
				want := model.apply(in)
				t.Logf("seed=%d step=%d input=%s output={id:%d err:%s} oracle={id:%d err:%s} basis=cur=%v statuses=%v timers=%d",
					seed, step, describeOp(in), got.id, errorName(got.err), want.id, errorName(want.err),
					model.snapshot().cur, statusList(model.snapshot()), len(model.timers))
				if got.id != want.id || !sameError(got.err, want.err) ||
					!reflect.DeepEqual(coordinatorSnapshot(c), model.snapshot()) {
					t.Fatalf("mismatch\ngot result=%#v state=%#v\nwant result=%#v state=%#v",
						got, coordinatorSnapshot(c), want, model.snapshot())
				}
				if in.now > now && got.err == nil {
					now = in.now
				} else if in.now > now && got.err != nil &&
					!sameError(got.err, ErrInvalidArgument) && !sameError(got.err, ErrClockRewind) {
					now = in.now
				}
			}
		})
	}
}

func statusList(s snapshot) []Status {
	out := make([]Status, 0, len(s.entries))
	for _, entry := range s.entries {
		out = append(out, entry.status)
	}
	return out
}

func TestConcurrentLinearizable(t *testing.T) {
	c, _ := New([]EnvConfig{{Name: "prod", K: 0, TTL: 1000, T: 100}})
	var wg sync.WaitGroup
	for ver := int64(1); ver <= 20; ver++ {
		wg.Add(1)
		go func(ver int64) {
			defer wg.Done()
			if _, err := c.Request(0, "prod", ver, false, alice); err != nil {
				t.Errorf("request: %v", err)
			}
		}(ver)
	}
	wg.Wait()
	if countStatus(c, Running) > 1 {
		t.Fatal("more than one Running")
	}
	if err := c.Tick(2000); err != nil {
		t.Fatalf("tick: %v", err)
	}
	if countStatus(c, TimedOut) != 20 || c.envs["prod"].cur != 0 {
		t.Fatalf("state after cascade: timedOut=%d cur=%d", countStatus(c, TimedOut), c.envs["prod"].cur)
	}
}

func countStatus(c *Coordinator, status Status) int {
	count := 0
	for _, req := range c.entries {
		if req.status == status {
			count++
		}
	}
	return count
}

func TestRejectionPriority(t *testing.T) {
	c, _ := New([]EnvConfig{{Name: "prod", K: 1, TTL: 10, T: 10}})
	_, _ = c.Request(0, "prod", 5, false, alice)
	_ = c.Approve(0, 1, bob)
	_ = c.Finish(0, 1, true, dave)
	_, _ = c.Request(0, "prod", 6, false, alice)
	tests := []struct {
		name string
		in   op
		want error
	}{
		{"invalid", op{name: "request", now: -1, env: "prod", ver: 7, caller: alice}, ErrInvalidArgument},
		{"permission", op{name: "request", now: 0, env: "missing", ver: 7, caller: noperm}, ErrPermission},
		{"not found", op{name: "cancel", now: 0, id: 99, caller: dave}, ErrNotFound},
		{"not owner", op{name: "cancel", now: 0, id: 1, caller: dave}, ErrNotOwner},
		{"status", op{name: "finish", now: 0, id: 2, caller: dave}, ErrStatus},
		{"stale", op{name: "request", now: 0, env: "prod", ver: 99, rollback: true, caller: alice}, ErrStale},
		{"unknown", op{name: "request", now: 0, env: "prod", ver: 4, rollback: true, caller: alice}, ErrUnknownVersion},
		{"self", op{name: "approve", now: 0, id: 2, caller: Caller{User: "alice", Permissions: Approve}}, ErrSelfApproval},
		{"duplicate", op{name: "approve", now: 0, id: 1, caller: bob}, ErrStatus},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := applyProduction(c, test.in)
			t.Logf("input=%s output=%s basis=%v", describeOp(test.in), errorName(got.err), test.want)
			if !sameError(got.err, test.want) {
				t.Fatalf("got %v, want %v", got.err, test.want)
			}
		})
	}
}

func TestExaminedIndependentOfEnvironmentCount(t *testing.T) {
	for _, envCount := range []int{100, 10000} {
		t.Run(fmt.Sprintf("%d-envs", envCount), func(t *testing.T) {
			configs := make([]EnvConfig, envCount)
			for i := range configs {
				configs[i] = EnvConfig{Name: fmt.Sprintf("env-%05d", i), K: 0, TTL: 1, T: 10}
				if i >= 1 && i <= 3 {
					configs[i].T = 100
				}
			}
			c, _ := New(configs)
			for _, ver := range []int64{5, 6, 7} {
				if _, err := c.Request(0, configs[0].Name, ver, false, alice); err != nil {
					t.Fatal(err)
				}
			}
			for _, i := range []int{1, 2, 3} {
				if _, err := c.Request(0, configs[i].Name, 9, false, alice); err != nil {
					t.Fatal(err)
				}
			}
			_ = c.Tick(9)
			if c.examined != 1 {
				t.Fatalf("before deadline examined=%d, want 1", c.examined)
			}
			_ = c.Tick(30)
			if c.examined != 4 || c.examined > 3+1 {
				t.Fatalf("three landings examined=%d, want 4", c.examined)
			}
			_ = c.Tick(100)
			if c.examined != 3 {
				t.Fatalf("remaining landings examined=%d, want 3", c.examined)
			}
		})
	}
}
