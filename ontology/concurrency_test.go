package ontology

import (
	"sync"
	"testing"
)

// TestConcurrentContendedCreate 多个 goroutine 竞争同一个仅余 1 个名额的槽位：
// 恰好一个成功，其余全部得到同一类基数错误，绝不超发。
func TestConcurrentContendedCreate(t *testing.T) {
	s, _ := mustRegister(t)

	// LT2: u1 起点 ExactlyOne。先不创建，N 个 goroutine 同时抢唯一名额（不同终点）。
	const n = 64
	for i := 0; i < n+100; i++ {
		if err := s.RegisterObject("v"+itoa2(i), "T2"); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	kinds := make([]ErrKind, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			err := s.CreateLink("LT2", Pair{"u1", "v" + itoa2(i)})
			kinds[i] = KindOf(err)
		}(i)
	}
	close(start)
	wg.Wait()

	accepted, srcRej := 0, 0
	for _, k := range kinds {
		switch k {
		case KindOK:
			accepted++
		case KindSourceCardinality:
			srcRej++
		default:
			t.Fatalf("出现意料之外的结果类别: %s", k)
		}
	}
	t.Logf("输入=%d个并发创建竞争u1的ExactlyOne名额 实际输出=成功%d 起点超限%d 依据=上限1", n, accepted, srcRej)
	if accepted != 1 || srcRej != n-1 {
		t.Fatalf("必须恰好1个成功，得到 accepted=%d rejected=%d", accepted, srcRej)
	}
	if used := s.UsedSource("LT2", "u1"); used != 1 {
		t.Fatalf("实际占用不得超过声明上限：%d", used)
	}
}

// TestDeleteThenConcurrentCreate 题目点名竞争：先占满名额，一个 goroutine
// 删除释放，其余并发创建抢占；释放与删除同时刻生效，最终占用恰好回到上限，
// 且每个创建结论都能在某个全局串行顺序下解释。
func TestDeleteThenConcurrentCreate(t *testing.T) {
	s, _ := mustRegister(t)
	if err := s.CreateLink("LT2", Pair{"u1", "v1"}); err != nil {
		t.Fatal(err)
	}

	const n = 64
	for i := 100; i < 100+n; i++ {
		if err := s.RegisterObject("v"+itoa2(i), "T2"); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	kinds := make([]ErrKind, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			kinds[i] = KindOf(s.CreateLink("LT2", Pair{"u1", "v" + itoa2(i+100)}))
		}(i)
	}

	// 与创建同时刻触发删除。
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_ = s.DeleteLink("LT2", Pair{"u1", "v1"})
	}()
	close(start)
	wg.Wait()

	accepted := 0
	for _, k := range kinds {
		if k == KindOK {
			accepted++
		} else if k != KindSourceCardinality {
			t.Fatalf("意外类别 %s", k)
		}
	}
	used := s.UsedSource("LT2", "u1")
	t.Logf("输入=1删除+%d并发创建 实际输出=创建成功%d 最终占用%d", n, accepted, used)
	// 可线性化结局只有两种，二者必居其一：
	//  (a) 删除先于所有创建：释放的唯一名额被一个创建抢到，accepted=1,used=1；
	//  (b) 删除晚于所有创建：全部创建先被拒，随后删除生效，accepted=0,used=0。
	if !((accepted == 1 && used == 1) || (accepted == 0 && used == 0)) {
		t.Fatalf("竞争结果无法对应任何全局串行顺序：accepted=%d used=%d", accepted, used)
	}

	// 重放确定性：把系统收敛到空，再用固定串行序列重放，结论必须稳定一致。
	if winner := s.winnerTarget("u1"); winner != "" {
		if err := s.DeleteLink("LT2", Pair{"u1", winner}); err != nil {
			t.Fatalf("清理重放失败: %v", err)
		}
	}
	if err := s.CreateLink("LT2", Pair{"u1", "v1"}); err != nil {
		t.Fatalf("删除后重放创建应成功: %v", err)
	}
	if used := s.UsedSource("LT2", "u1"); used != 1 {
		t.Fatalf("重放后占用应为1，得到 %d", used)
	}
	t.Logf("判定依据=结局(a)/(b)都可映射到全局串行顺序；收敛后固定序列重放得占用1，状态确定")
}

// winnerTarget 从快照中找出 u1 当前唯一现存链接的终点。
func (s *Service) winnerTarget(source string) string {
	for _, l := range s.Snapshot() {
		if l.LinkType == "LT2" && l.Source == source {
			return l.Target
		}
	}
	return ""
}

func itoa2(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	p := len(b)
	for i > 0 {
		p--
		b[p] = byte('0' + i%10)
		i /= 10
	}
	return string(b[p:])
}
