package mailbox

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

// checkInvariants 校验规格要求的结构性不变量。
func checkInvariants(t *testing.T, b *Mailbox) {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.msgs) > b.lmax {
		t.Fatalf("消息总数 %d 超过 Lmax %d", len(b.msgs), b.lmax)
	}
	if len(b.byCK) > b.k {
		t.Fatalf("折叠键种类 %d 超过 K %d", len(b.byCK), b.k)
	}
	if b.nonCol > b.p {
		t.Fatalf("不可折叠条数 %d 超过 P %d", b.nonCol, b.p)
	}
	seenCK := map[string]bool{}
	seenID := map[string]bool{}
	nonCol := 0
	for _, m := range b.msgs {
		if seenID[m.id] {
			t.Fatalf("id %q 重复", m.id)
		}
		seenID[m.id] = true
		if m.ck != "" {
			if seenCK[m.ck] {
				t.Fatalf("ck %q 对应多条消息", m.ck)
			}
			seenCK[m.ck] = true
		} else {
			nonCol++
		}
	}
	if nonCol != b.nonCol {
		t.Fatalf("不可折叠计数不一致：%d != %d", nonCol, b.nonCol)
	}
	if len(b.byID) != len(b.msgs) || len(b.byCK) != len(seenCK) {
		t.Fatalf("索引不一致：byID=%d msgs=%d byCK=%d kinds=%d",
			len(b.byID), len(b.msgs), len(b.byCK), len(seenCK))
	}
}

func sameItems(a, b []Item) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRandomDifferential 用 2000 组随机操作序列与朴素模拟对拍，
// 日志打印每步输入、输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	ids := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
	cks := []string{"", "", "x", "y", "z", "w"}
	ttls := []int64{0, 1, 2, 3, 5, 9, 40, 100, maxTTL}

	for trial := 0; trial < 2000; trial++ {
		k := 1 + rng.Intn(4)
		p := 1 + rng.Intn(5)
		lmax := 1 + rng.Intn(6)
		real, err := NewMailbox(k, p, lmax)
		if err != nil {
			t.Fatalf("trial %d: NewMailbox err = %v", trial, err)
		}
		sim := newNaive(k, p, lmax)
		prefix := fmt.Sprintf("trial %d (K=%d,P=%d,Lmax=%d)", trial, k, p, lmax)
		t.Logf("%s 开始", prefix)

		var clock int64
		ops := 5 + rng.Intn(36)
		for op := 0; op < ops; op++ {
			// 多数时候推进时钟，偶尔原地或回退。
			var now int64
			switch r := rng.Intn(12); {
			case r == 0 && clock > 0:
				now = clock - 1 // 时钟回退
			case r <= 2:
				now = clock
			default:
				clock += int64(rng.Intn(4))
				now = clock
			}

			switch rng.Intn(10) {
			case 0, 1: // Drain
				cnt := 1 + rng.Intn(5)
				if rng.Intn(20) == 0 {
					cnt = 0 // 非法 cnt
				}
				got, gotErr := real.Drain(now, cnt)
				want, wantErr := sim.drain(now, cnt)
				t.Logf("%s op %d: Drain(now=%d,cnt=%d) -> %v, %v | 模拟 %v, %v",
					prefix, op, now, cnt, describeAll(got), gotErr, describeAll(want), wantErr)
				if !errors.Is(gotErr, wantErr) || !sameItems(got, want) {
					t.Fatalf("%s op %d: Drain 不一致：got %v,%v want %v,%v",
						prefix, op, describeAll(got), gotErr, describeAll(want), wantErr)
				}
			case 2: // Peek，随后 Drain 验证顺序一致
				got, gotErr := real.Peek(now)
				want, wantErr := sim.peek(now)
				t.Logf("%s op %d: Peek(now=%d) -> %v, %v | 模拟 %v, %v",
					prefix, op, now, describeAll(got), gotErr, describeAll(want), wantErr)
				if !errors.Is(gotErr, wantErr) || !sameItems(got, want) {
					t.Fatalf("%s op %d: Peek 不一致：got %v,%v want %v,%v",
						prefix, op, describeAll(got), gotErr, describeAll(want), wantErr)
				}
				if gotErr == nil {
					drained, err := real.Drain(now, maxCnt)
					if err != nil {
						t.Fatalf("%s op %d: Drain err = %v", prefix, op, err)
					}
					simDrained, _ := sim.drain(now, maxCnt)
					if !sameItems(got, drained) {
						t.Fatalf("%s op %d: Peek %v 与随后 Drain %v 不一致",
							prefix, op, describeAll(got), describeAll(drained))
					}
					if !sameItems(drained, simDrained) {
						t.Fatalf("%s op %d: Drain %v 与模拟 %v 不一致",
							prefix, op, describeAll(drained), describeAll(simDrained))
					}
				}
			default: // Enqueue
				id := ids[rng.Intn(len(ids))]
				ck := cks[rng.Intn(len(cks))]
				prio := rng.Intn(2)
				ttl := ttls[rng.Intn(len(ttls))]
				switch rng.Intn(30) {
				case 0:
					id = "" // 非法 id
				case 1:
					prio = 2 // 非法 prio
				case 2:
					ttl = maxTTL + 1 // 非法 ttl
				case 3:
					ck = string(make([]byte, 65)) // 非法 ck
				}
				got, gotErr := real.Enqueue(id, ck, prio, ttl, now)
				want, wantErr, reason := sim.enqueue(id, ck, prio, ttl, now)
				t.Logf("%s op %d: Enqueue(id=%q,ck=%q,prio=%d,ttl=%d,now=%d) -> %v, %v | 模拟 %v, %v | 依据: %s",
					prefix, op, id, ck, prio, ttl, now, got, gotErr, want, wantErr, reason)
				if !errors.Is(gotErr, wantErr) || got != want {
					t.Fatalf("%s op %d: Enqueue 不一致：got %v,%v want %v,%v",
						prefix, op, got, gotErr, want, wantErr)
				}
			}

			// 守恒：丢弃的存活消息数（含触发溢出的新消息与被自己淘汰的
			// 新消息）恒等于标记累计 n（含已取走标记）。
			if sim.discarded != sim.marked {
				t.Fatalf("%s op %d: 守恒破坏 discarded=%d marked=%d",
					prefix, op, sim.discarded, sim.marked)
			}
			checkInvariants(t, real)
		}

		// 序列结束，最终状态一致。
		got, _ := real.Peek(clock)
		want, _ := sim.peek(clock)
		if !sameItems(got, want) {
			t.Fatalf("%s 最终状态不一致：got %v want %v",
				prefix, describeAll(got), describeAll(want))
		}
		t.Logf("%s 通过，共 %d 步", prefix, ops)
	}
}
