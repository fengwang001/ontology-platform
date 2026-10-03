package notify_test

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"sync"
	"testing"

	"ontology/alertstore"
	"ontology/notify"
	"ontology/suppress"
)

func TestOracleFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(20261003))
	pool := []alertstore.Labels{
		{"svc": "db", "sev": "warn", "i": "1"},
		{"svc": "db", "sev": "crit", "i": "2"},
		{"svc": "web", "sev": "warn", "i": "3"},
		{"svc": "web", "sev": "crit", "i": "4"},
		{"svc": "db", "sev": "ok", "i": "5", "zone": "z"},
	}
	for iter := 0; iter < 300; iter++ {
		cfg := notify.Config{GroupLabels: []string{"svc"},
			Wait: int64(rng.Intn(8)), Repeat: int64(rng.Intn(12)), MaxAlerts: 3 + rng.Intn(4)}
		if rng.Intn(2) == 0 {
			cfg.Rules = []suppress.InhibitRule{{Source: suppress.Matcher{"sev": "crit"},
				Target: suppress.Matcher{"sev": "warn"}, Equal: []string{"svc"}}}
		}
		n, o := mustNew(t, cfg), newOracle(cfg)
		var rl []notify.Notification
		failSet := map[int]bool{}
		send := func(note notify.Notification) error {
			if failSet[len(rl)] {
				return errors.New("boom")
			}
			rl = append(rl, note)
			return nil
		}
		var now int64
		for step := 0; step < 60; step++ {
			now += int64(rng.Intn(5))
			l := pool[rng.Intn(len(pool))]
			switch op := rng.Intn(10); {
			case op < 5:
				if _, e := n.Fire(now, l); e == nil {
					o.fire(l, now)
				}
			case op < 7:
				_, e := n.Resolve(now, l)
				if a := o.alerts[alertstore.Fingerprint(l)]; e == nil && a != nil {
					a.firing, o.now, o.hasTime = false, now, true
				}
			default:
				if rng.Intn(3) == 0 && now < 50 {
					id := fmt.Sprintf("s%d", step)
					_ = n.AddSilence(now, id, suppress.Matcher{"sev": "warn"}, 5, now+10)
					o.silInt[id], o.silM[id] = [2]int64{5, now + 10}, map[string]string{"sev": "warn"}
					o.now, o.hasTime = now, true
				}
				failSet[len(rl)] = rng.Intn(3) == 0
				res, err := n.Tick(now, send)
				if err != nil {
					t.Fatal(err)
				}
				os, of := oTick(o, now, func(i int) bool { return failSet[i] })
				if !eqStr(res.Sent, os) || !eqStr(res.Failed, of) {
					t.Fatalf("iter=%d step=%d now=%d 分组不一致 real(s=%v f=%v) ora(s=%v f=%v)",
						iter, step, now, res.Sent, res.Failed, os, of)
				}
				if len(rl) != len(o.log) {
					t.Fatalf("iter=%d step=%d 通知数不一致", iter, step)
				}
				for i := range o.log {
					if !noteEqual(rl[i], o.log[i]) {
						t.Fatalf("iter=%d step=%d 通知不一致\nreal=%+v\nora=%+v", iter, step, rl[i], o.log[i])
					}
				}
				if step == 0 && (len(os) > 0 || len(of) > 0) {
					log.Printf("fuzz iter=%d now=%d 发送=%d 失败=%d", iter, now, len(os), len(of))
				}
			}
		}
	}
}

// TestConcurrentStress 并发调用所有变更与 Tick，验证无竞态、无死锁、上限不被突破。
func TestConcurrentStress(t *testing.T) {
	n := mustNew(t, notify.Config{GroupLabels: []string{"svc"}, Wait: 1, Repeat: 3, MaxAlerts: 50})
	send := func(note notify.Notification) error {
		seen := map[string]bool{}
		for _, f := range note.Firing {
			if seen[f] {
				t.Errorf("Firing 重复指纹 %s", f)
			}
			seen[f] = true
		}
		for _, f := range note.Resolved {
			if seen[f] {
				t.Errorf("Firing 与 Resolved 相交: %s", f)
			}
		}
		if len(note.Firing) == 0 && len(note.Resolved) == 0 {
			t.Error("空通知不应被发送")
		}
		return nil
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id*7 + 1)))
			for i := 0; i < 200; i++ {
				now := int64(i * 3)
				l := alertstore.Labels{"svc": []string{"db", "web"}[r.Intn(2)],
					"i": fmt.Sprintf("%d-%d", id, r.Intn(40))}
				switch r.Intn(4) {
				case 0:
					_, _ = n.Fire(now, l)
				case 1:
					_, _ = n.Resolve(now, l)
				case 2:
					_, _ = n.Tick(now, send)
				default:
					_ = n.AddSilence(now, fmt.Sprintf("z%d-%d", id, i),
						suppress.Matcher{"svc": "db"}, now, now+1)
				}
			}
		}(w)
	}
	wg.Wait()
	log.Printf("并发压力: 8 goroutine x 200 操作完成, 无竞态/死锁")
}
