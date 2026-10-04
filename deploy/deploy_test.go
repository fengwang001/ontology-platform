package deploy

import (
	"errors"
	"reflect"
	"testing"
)

func TestSpecScenarios(t *testing.T) {
	tests := []struct {
		name   string
		envs   []EnvConfig
		ops    []op
		checks map[int64]Status
		cur    map[string]int64
	}{
		{
			name: "cascade timeout then late finish is status error",
			envs: []EnvConfig{{Name: "prod", K: 1, TTL: 100, T: 50}},
			ops: []op{
				{name: "request", now: 0, env: "prod", ver: 5, caller: alice},
				{name: "approve", now: 10, id: 1, caller: bob},
				{name: "request", now: 20, env: "prod", ver: 6, caller: alice},
				{name: "approve", now: 25, id: 2, caller: bob},
				{name: "finish", now: 130, id: 1, ok: true, caller: alice},
			},
			checks: map[int64]Status{1: TimedOut, 2: TimedOut},
			cur:    map[string]int64{"prod": 0},
		},
		{
			name: "approval exactly expires at grant",
			envs: []EnvConfig{{Name: "prod", K: 1, TTL: 35, T: 50}},
			ops: []op{
				{name: "request", now: 0, env: "prod", ver: 5, caller: alice},
				{name: "approve", now: 10, id: 1, caller: bob},
				{name: "request", now: 20, env: "prod", ver: 6, caller: alice},
				{name: "approve", now: 25, id: 2, caller: bob},
				{name: "tick", now: 130},
			},
			checks: map[int64]Status{1: TimedOut, 2: Expired},
			cur:    map[string]int64{"prod": 0},
		},
		{
			name: "approval valid one second before grant",
			envs: []EnvConfig{{Name: "prod", K: 1, TTL: 36, T: 50}},
			ops: []op{
				{name: "request", now: 0, env: "prod", ver: 5, caller: alice},
				{name: "approve", now: 10, id: 1, caller: bob},
				{name: "request", now: 20, env: "prod", ver: 6, caller: alice},
				{name: "approve", now: 25, id: 2, caller: bob},
				{name: "tick", now: 109},
				{name: "tick", now: 110},
			},
			checks: map[int64]Status{1: TimedOut, 2: TimedOut},
			cur:    map[string]int64{"prod": 0},
		},
		{
			name: "fifo stale before expired and rollback needs two votes",
			envs: []EnvConfig{{Name: "prod", K: 1, TTL: 1000, T: 50}},
			ops: []op{
				{name: "request", now: 0, env: "prod", ver: 5, caller: alice},
				{name: "approve", now: 0, id: 1, caller: bob},
				{name: "request", now: 0, env: "prod", ver: 6, caller: alice},
				{name: "request", now: 0, env: "prod", ver: 7, caller: alice},
				{name: "approve", now: 0, id: 3, caller: bob},
				{name: "approve", now: 0, id: 2, caller: bob},
				{name: "finish", now: 1, id: 1, ok: true, caller: alice},
				{name: "finish", now: 2, id: 3, ok: true, caller: alice},
			},
			checks: map[int64]Status{1: Succeeded, 2: Stale, 3: Succeeded},
			cur:    map[string]int64{"prod": 7},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, err := New(test.envs)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			model := newNaiveModel(test.envs)
			for index, in := range test.ops {
				beforeTick := in.name == "tick" && in.now == 109
				got := applyProduction(c, in)
				want := model.apply(in)
				t.Logf("step=%d input=%s output={id:%d err:%s} basis=%s",
					index, describeOp(in), got.id, errorName(got.err), "spec table")
				if got.id != want.id || !sameError(got.err, want.err) {
					t.Fatalf("step %d result = {id:%d err:%v}, want {id:%d err:%v}",
						index, got.id, got.err, want.id, want.err)
				}
				if beforeTick {
					info, ok := c.Get(2)
					if !ok || info.Status != Running {
						t.Fatalf("at 109 request 2 = %v ok=%v, want Running", info.Status, ok)
					}
				}
			}
			for id, status := range test.checks {
				info, ok := c.Get(id)
				if !ok || info.Status != status {
					t.Fatalf("id %d status = %v ok=%v, want %v", id, info.Status, ok, status)
				}
			}
			for envName, cur := range test.cur {
				if c.envs[envName].cur != cur {
					t.Fatalf("%s cur = %d, want %d", envName, c.envs[envName].cur, cur)
				}
			}
			if !reflect.DeepEqual(coordinatorSnapshot(c), model.snapshot()) {
				t.Fatalf("snapshots differ\ngot %#v\nwant %#v",
					coordinatorSnapshot(c), model.snapshot())
			}
		})
	}
}

func TestDetailedRules(t *testing.T) {
	t.Run("expired approval can be replaced", func(t *testing.T) {
		c, _ := New([]EnvConfig{{Name: "prod", K: 2, TTL: 10, T: 100}})
		model := newNaiveModel([]EnvConfig{{Name: "prod", K: 2, TTL: 10, T: 100}})
		steps := []op{
			{name: "request", now: 0, env: "prod", ver: 5, caller: alice},
			{name: "approve", now: 0, id: 1, caller: bob},
			{name: "tick", now: 10},
			{name: "approve", now: 10, id: 1, caller: bob},
			{name: "approve", now: 10, id: 1, caller: carol},
		}
		for index, in := range steps {
			got := applyProduction(c, in)
			want := model.apply(in)
			t.Logf("step=%d input=%s output={id:%d err:%s} basis=%s",
				index, describeOp(in), got.id, errorName(got.err), "expired ticket replacement")
			if got != want {
				t.Fatalf("got {id:%d err:%v}, want {id:%d err:%v}", got.id, got.err, want.id, want.err)
			}
		}
		info, _ := c.Get(1)
		if info.Status != Running || info.Start != 10 || len(info.Approvals) != 2 {
			t.Fatalf("request = %#v, want Running at 10", info)
		}
	})

	t.Run("rollback requires one additional distinct approver", func(t *testing.T) {
		configs := []EnvConfig{{Name: "prod", K: 1, TTL: 1000, T: 100}}
		c, _ := New(configs)
		model := newNaiveModel(configs)
		steps := []op{
			{name: "request", now: 0, env: "prod", ver: 5, caller: alice},
			{name: "approve", now: 0, id: 1, caller: bob},
			{name: "finish", now: 0, id: 1, ok: true, caller: alice},
			{name: "request", now: 0, env: "prod", ver: 6, caller: alice},
			{name: "approve", now: 0, id: 2, caller: bob},
			{name: "finish", now: 0, id: 2, ok: true, caller: alice},
			{name: "request", now: 0, env: "prod", ver: 5, rollback: true, caller: alice},
			{name: "approve", now: 0, id: 3, caller: bob},
			{name: "approve", now: 0, id: 3, caller: bob},
			{name: "approve", now: 0, id: 3, caller: carol},
		}
		for index, in := range steps {
			got := applyProduction(c, in)
			want := model.apply(in)
			t.Logf("step=%d input=%s output={id:%d err:%s} basis=%s",
				index, describeOp(in), got.id, errorName(got.err), "rollback need K+1 distinct votes")
			if got.id != want.id || !sameError(got.err, want.err) {
				t.Fatalf("step %d got %#v, want %#v", index, got, want)
			}
		}
		info, _ := c.Get(3)
		if info.Status != Running {
			t.Fatalf("rollback status = %v, want Running", info.Status)
		}
	})

	t.Run("non owner cannot cancel but admin can", func(t *testing.T) {
		configs := []EnvConfig{{Name: "prod", K: 1, TTL: 1000, T: 100}}
		c, _ := New(configs)
		id, err := c.Request(0, "prod", 5, false, alice)
		if err != nil || id != 1 {
			t.Fatalf("request = %d,%v", id, err)
		}
		if err := c.Cancel(0, 1, dave); !errors.Is(err, ErrNotOwner) {
			t.Fatalf("non-owner cancel = %v, want ErrNotOwner", err)
		}
		info, _ := c.Get(1)
		if info.Status != Pending {
			t.Fatalf("status after rejected cancel = %v, want Pending", info.Status)
		}
		adminOnly := Caller{User: "root", Permissions: Admin}
		if err := c.Cancel(0, 1, adminOnly); err != nil {
			t.Fatalf("admin cancel = %v", err)
		}
		info, _ = c.Get(1)
		if info.Status != Canceled {
			t.Fatalf("status = %v, want Canceled", info.Status)
		}
	})

	t.Run("status rejection still lands timeouts", func(t *testing.T) {
		configs := []EnvConfig{{Name: "prod", K: 0, TTL: 1, T: 10}}
		c, _ := New(configs)
		id, err := c.Request(0, "prod", 5, false, alice)
		if err != nil || id != 1 {
			t.Fatalf("request = %d,%v", id, err)
		}
		err = c.Finish(20, 999, false, dave)
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("finish missing id = %v, want ErrNotFound", err)
		}
		info, _ := c.Get(1)
		if info.Status != TimedOut {
			t.Fatalf("request status after rejected operation = %v, want TimedOut", info.Status)
		}
	})
}

func TestStaleThenExpiredInSameGrant(t *testing.T) {
	configs := []EnvConfig{{Name: "prod", K: 1, TTL: 5, T: 10}}
	c, _ := New(configs)
	model := newNaiveModel(configs)
	steps := []op{
		{name: "request", now: 0, env: "prod", ver: 8, caller: alice},
		{name: "approve", now: 0, id: 1, caller: bob},
		{name: "request", now: 0, env: "prod", ver: 8, caller: alice},
		{name: "request", now: 0, env: "prod", ver: 10, caller: alice},
		{name: "request", now: 0, env: "prod", ver: 9, caller: alice},
		{name: "approve", now: 0, id: 2, caller: bob},
		{name: "approve", now: 0, id: 3, caller: bob},
		{name: "approve", now: 0, id: 4, caller: bob},
		{name: "finish", now: 9, id: 1, ok: true, caller: alice},
	}
	for index, in := range steps {
		got := applyProduction(c, in)
		want := model.apply(in)
		t.Logf("step=%d input=%s output={id:%d err:%s} basis=%s",
			index, describeOp(in), got.id, errorName(got.err),
			"grant removes stale ver=8 before rejecting expired ver=9")
		if got.id != want.id || !sameError(got.err, want.err) {
			t.Fatalf("step %d got {id:%d err:%v}, want {id:%d err:%v}",
				index, got.id, got.err, want.id, want.err)
		}
	}
	info2, _ := c.Get(2)
	info3, _ := c.Get(3)
	if info2.Status != Stale {
		t.Fatalf("request 2 = %v, want Stale", info2.Status)
	}
	if info3.Status != Expired {
		t.Fatalf("request 3 = %v, want Expired", info3.Status)
	}
	if !reflect.DeepEqual(coordinatorSnapshot(c), model.snapshot()) {
		t.Fatalf("snapshots differ")
	}
}

func TestConstructorAndBoundaryValidation(t *testing.T) {
	tests := []struct {
		name string
		envs []EnvConfig
		want error
	}{
		{"empty envs", nil, ErrInvalidArgument},
		{"empty name", []EnvConfig{{Name: "", K: 0, TTL: 1, T: 1}}, ErrInvalidArgument},
		{"K too high", []EnvConfig{{Name: "x", K: 6, TTL: 1, T: 1}}, ErrInvalidArgument},
		{"TTL zero", []EnvConfig{{Name: "x", K: 0, TTL: 0, T: 1}}, ErrInvalidArgument},
		{"T too high", []EnvConfig{{Name: "x", K: 0, TTL: 1, T: 1_000_000_001}}, ErrInvalidArgument},
		{"duplicate env", []EnvConfig{
			{Name: "x", K: 0, TTL: 1, T: 1},
			{Name: "x", K: 0, TTL: 1, T: 1},
		}, ErrInvalidArgument},
		{"valid boundaries", []EnvConfig{{Name: "x", K: 5, TTL: 1_000_000_000, T: 1_000_000_000}}, nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(test.envs)
			if !sameError(err, test.want) {
				t.Fatalf("New error = %v, want %v", err, test.want)
			}
		})
	}
}
