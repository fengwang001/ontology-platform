package admission

import (
	"testing"
)

func benchConfig(total int64) *Config {
	return &Config{
		TotalSeats: total,
		Levels:     []LevelConfig{{Name: "L", Kind: LevelLimited, Share: 1, QueueLimit: 1 << 20, Timeout: 1 << 40}},
		Rules:      []Rule{{Name: "r", Priority: 0, Match: MatchConditions{}, TargetLevel: "L", Distinguish: FlowByUser}},
	}
}

func benchReq(i int) *Request {
	return &Request{
		ID:        benchID(i),
		UserGroup: "",
		Verb:      "put",
		Resource:  "obj",
		User:      benchUser(i % 4096),
		Namespace: "ns",
		Seats:     1,
	}
}

func benchUser(i int) string {
	const base = "user-"
	out := []byte(base)
	for x := i; x > 0; x /= 26 {
		out = append(out, byte('a'+x%26))
	}
	return string(out)
}

func benchID(i int) string {
	return "req-" + benchUser(i)
}

// BenchmarkSubmitQueued 衡量大量排队入队：每个请求只做 map/链表/堆的 O(1) 操作。
func BenchmarkSubmitQueued(b *testing.B) {
	for _, n := range []int{1024, 8192, 65536} {
		b.Run("N="+itoaBench(n), func(b *testing.B) {
			for k := 0; k < b.N; k++ {
				c, err := NewController(benchConfig(1))
				if err != nil {
					b.Fatal(err)
				}
				now := Time(0)
				for i := 0; i < n; i++ {
					res := c.Submit(benchReq(i), now)
					if res.Submit.Err != nil {
						b.Fatalf("submit %d: %v", i, res.Submit.Err)
					}
				}
			}
		})
	}
}

// BenchmarkCompleteDrain 衡量完成释放后的出队成本（每个完成至多触发常数个链表/堆操作）。
func BenchmarkCompleteDrain(b *testing.B) {
	for _, n := range []int{1024, 8192, 65536} {
		b.Run("N="+itoaBench(n), func(b *testing.B) {
			for k := 0; k < b.N; k++ {
				c, _ := NewController(benchConfig(int64(n)))
				now := Time(0)
				var running []string
				for i := 0; i < n; i++ {
					res := c.Submit(benchReq(i), now)
					if res.Submit.Decision == DecisionExecuted {
						running = append(running, benchReq(i).ID)
					}
				}
				for _, id := range running {
					c.Complete(id, now)
				}
			}
		})
	}
}

func itoaBench(v int) string {
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	return string(b)
}
