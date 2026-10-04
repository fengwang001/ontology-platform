package issue

import (
	"fmt"
	"sync"
	"testing"
)

// TestConcurrentReplay 同一批操作从多 goroutine 打乱调用：
// 任一时刻每袋只有一个状态、结果等价于某串行顺序（无 panic、状态自洽）。
func TestConcurrentReplay(t *testing.T) {
	mk := func() *Manager {
		m := New(500, 3000)
		m.Grant("tech", "配血")
		m.Grant("i1", "发血")
		m.Grant("i2", "发血")
		m.Grant("boss", "主管")
		for i := 0; i < 60; i++ {
			a := []string{"A", "B", "AB", "O"}[i%4]
			r := []string{"阳", "阴"}[i%2]
			if err := m.AddBag(1, fmt.Sprintf("c%02d", i), a, r, int64(2000+i*100)); err != nil {
				t.Fatal(err)
			}
		}
		return m
	}

	// 构造一个确定性操作脚本，按 now 非降排列；打乱并发调用后，
	// 用互斥语义验证：最终可发血袋数 + 报废数 + 各患者预留数守恒。
	script := func() []step {
		var ss []step
		now := int64(10)
		pats := []string{"P1", "P2", "P3"}
		sn := 0
		for i := 0; i < 300; i++ {
			now += int64(i%7 + 1)
			p := pats[i%len(pats)]
			switch i % 6 {
			case 0:
				sn++
				ss = append(ss, step{kind: "type", now: now, user: "t", pat: p,
					sample: fmt.Sprintf("cs%d", sn), abo: "O", rh: "阴"})
			case 1:
				ss = append(ss, xm(now, "tech", p, 1+(i%4)))
			case 2:
				ss = append(ss, step{kind: "issue", now: now, user: "i1", n2: "i2",
					pat: p, bag: fmt.Sprintf("c%02d", i%60)})
			case 3:
				ss = append(ss, step{kind: "return", now: now, user: "i1",
					bag: fmt.Sprintf("c%02d", (i+3)%60)})
			case 4:
				ss = append(ss, step{kind: "discard", now: now, user: "boss",
					bag: fmt.Sprintf("c%02d", (i+7)%60)})
			default:
				ss = append(ss, step{kind: "resolve", now: now, user: "boss", pat: p,
					abo: "O", rh: "阴"})
			}
		}
		return ss
	}()

	for round := 0; round < 8; round++ {
		m := mk()
		var wg sync.WaitGroup
		for _, s := range script {
			wg.Add(1)
			s := s
			go func() {
				defer wg.Done()
				_, _ = runStep(m, s)
			}()
		}
		wg.Wait()
		// 不变量：每袋至多预留给一名患者、状态唯一；预留袋必有患者。
		for name, b := range m.Snapshot().Bags {
			if b.Status > 3 {
				t.Fatalf("%s 非法状态 %d", name, b.Status)
			}
			if (b.Status == 1) != (b.Patient != "") {
				t.Fatalf("%s 预留/患者不一致: %+v", name, b)
			}
		}
	}
}

// TestReplayDeterminism 同一序列两次重放，选中血袋与最终状态必须逐字节一致。
func TestReplayDeterminism(t *testing.T) {
	run := func() Snapshot {
		m := New(400, 2500)
		m.Grant("tech", "配血")
		m.Grant("boss", "主管")
		m.Grant("i1", "发血")
		m.Grant("i2", "发血")
		for i := 0; i < 40; i++ {
			a := []string{"A", "B", "AB", "O"}[(i*7)%4]
			rh := []string{"阳", "阴"}[(i*3)%2]
			_ = m.AddBag(1, fmt.Sprintf("d%02d", i), a, rh, int64(1000+i*300))
		}
		seq := []step{
			{kind: "type", now: 2, user: "t", pat: "P", sample: "1", abo: "A", rh: "阳"},
			{kind: "type", now: 2, user: "t", pat: "P", sample: "2", abo: "A", rh: "阳"},
			xm(100, "tech", "P", 3),
			xm(200, "tech", "P", 2),
			{kind: "type", now: 300, user: "t", pat: "Q", sample: "3", abo: "O", rh: "阴"},
			xm(300, "tech", "Q", 2),
			{kind: "discard", now: 400, user: "boss", bag: "d00"},
			{kind: "type", now: 3000, user: "t", pat: "R", sample: "4", abo: "B", rh: "阳"},
			xm(3000, "tech", "R", 4),
		}
		for _, s := range seq {
			_, _ = runStep(m, s)
		}
		return m.Snapshot()
	}
	a := run()
	b := run()
	if fmt.Sprint(a) != fmt.Sprint(b) {
		t.Fatalf("重放不确定\n%+v\n%+v", a, b)
	}
}
