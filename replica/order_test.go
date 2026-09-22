package replica

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/bus"
	"ontology/version"
)

// dataView 是条目数据平面的末态快照（计数器属于观测信息，不参与比较）。
type dataView struct {
	Exists       bool
	State        string
	Version      version.Version
	Invalidated  version.Version
	Found        bool
	TTLRemaining int64
}

func viewOf(r *Replica, key string) dataView {
	info := r.Inspect(key)
	return dataView{
		Exists:       info.Exists,
		State:        info.State.String(),
		Version:      info.Version,
		Invalidated:  info.Invalidated,
		Found:        info.Found,
		TTLRemaining: int64(info.TTLRemaining),
	}
}

func setupFilledReplica(t *testing.T, keys []string, vers version.Version) *Replica {
	t.Helper()
	b := newBackend()
	for _, k := range keys {
		for v := version.Version(1); v <= vers; v++ {
			b.write(k, fmt.Sprintf("%s-v%d", k, v))
		}
	}
	r, _ := newTestReplica(b, 1<<40)
	for _, k := range keys {
		mustGet(t, r, k)
	}
	return r
}

func notificationsFor(keys []string, from, to version.Version) []bus.Notification {
	var ns []bus.Notification
	for _, k := range keys {
		for v := from; v <= to; v++ {
			ns = append(ns, bus.Notification{Key: k, Version: v})
		}
	}
	return ns
}

func deliver(r *Replica, ns []bus.Notification) {
	b := bus.New(0)
	r.Attach(b)
	if err := b.PublishAll(ns); err != nil {
		panic(err)
	}
	b.Flush()
}

// 规则 1：同一组通知随机打乱 100 次，末态与顺序投递逐字段相同。
func TestShuffledDeliveryConvergesToSequential(t *testing.T) {
	keys := []string{"a", "b", "c"}
	const hi = 6
	ns := notificationsFor(keys, 1, hi)

	base := setupFilledReplica(t, keys, 3)
	deliver(base, ns) // 顺序投递 v1..v6
	want := map[string]dataView{}
	for _, k := range keys {
		want[k] = viewOf(base, k)
	}

	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 100; trial++ {
		shuffled := append([]bus.Notification(nil), ns...)
		rng.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
		r := setupFilledReplica(t, keys, 3)
		deliver(r, shuffled)
		for _, k := range keys {
			if got := viewOf(r, k); got != want[k] {
				t.Fatalf("trial %d key %s: got %+v want %+v", trial, k, got, want[k])
			}
		}
	}
}

// 规则 2：重复通知幂等，实际失效计数不增长。
func TestDuplicateNotificationIdempotent(t *testing.T) {
	r := setupFilledReplica(t, []string{"k"}, 3)
	b := bus.New(0)
	r.Attach(b)
	for i := 0; i < 5; i++ {
		if err := b.Publish(bus.Notification{Key: "k", Version: 5}); err != nil {
			t.Fatal(err)
		}
	}
	b.Flush()
	if got := r.Stats().Invalidations; got != 1 {
		t.Fatalf("invalidations=%d want 1", got)
	}
	if got := r.Inspect("k").Invalidations; got != 1 {
		t.Fatalf("per-key invalidations=%d want 1", got)
	}
	// 再次投递同一条：仍然不增长。
	deliver(r, []bus.Notification{{Key: "k", Version: 5}})
	if got := r.Stats().Invalidations; got != 1 {
		t.Fatalf("invalidations=%d want 1 after redelivery", got)
	}
}

// 规则 8：8 个副本各自收到乱序、重复、部分丢失的通知，
// 投递完毕并过期一轮后收敛到同一版本。
func TestEightReplicasConverge(t *testing.T) {
	const nReplicas = 8
	b := newBackend()
	b.write("k", "v1")
	clock := NewManualClock()

	replicas := make([]*Replica, nReplicas)
	for i := range replicas {
		replicas[i] = New(Config{TTL: 100, Loader: b.load, Clock: clock.Clock()})
		mustGet(t, replicas[i], "k") // 各副本先持有 v1
	}

	var ns []bus.Notification
	for i := 2; i <= 7; i++ {
		ns = append(ns, bus.Notification{Key: "k", Version: b.write("k", fmt.Sprintf("v%d", i))})
	}

	rng := rand.New(rand.NewSource(7))
	for _, r := range replicas {
		plan := append([]bus.Notification(nil), ns...)
		rng.Shuffle(len(plan), func(a, c int) { plan[a], plan[c] = plan[c], plan[a] })
		plan = append(plan, plan[rng.Intn(len(plan))])   // 注入重复
		plan = append(plan[:2], plan[2+rng.Intn(2):]...) // 随机丢失一段
		pb := bus.New(0)
		r.Attach(pb)
		if err := pb.PublishAll(plan); err != nil {
			t.Fatal(err)
		}
		pb.Flush()
	}

	clock.Advance(100) // 到期一轮：无论通知是否丢失都必须重新回源
	var want version.Version
	for i, r := range replicas {
		mustGet(t, r, "k")
		got := viewOf(r, "k").Version
		if i == 0 {
			want = got
		} else if got != want {
			t.Fatalf("replica %d diverged: got %v want %v", i, got, want)
		}
	}
	if want != 7 {
		t.Fatalf("converged version=%v want 7", want)
	}
	for _, r := range replicas {
		if v, _ := mustGet(t, r, "k"); v != "v7" {
			t.Fatalf("value=%q want v7", v)
		}
	}
}
