package register

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// bruteForce 是穷举参照：枚举未结束可放入操作的全部子集与
// 全部排列，逐一检验实时先后约束与寄存器语义。
func bruteForce(ops []opRecord) bool {
	var finished, pending []int
	for i := range ops {
		if ops[i].finished {
			finished = append(finished, i)
		} else if ops[i].op.Kind != Read {
			pending = append(pending, i)
		}
	}
	// 枚举未结束可放入操作的全部子集。
	for mask := 0; mask < 1<<uint(len(pending)); mask++ {
		set := append([]int{}, finished...)
		for j, idx := range pending {
			if mask&(1<<uint(j)) != 0 {
				set = append(set, idx)
			}
		}
		if searchPermutation(ops, set, 0, make([]int, 0, len(set)), make([]bool, len(set))) {
			return true
		}
	}
	return false
}

// searchPermutation 枚举 set 的全部排列并检验。
func searchPermutation(ops []opRecord, set []int, depth int, perm []int, used []bool) bool {
	if depth == len(set) {
		return validLinearization(ops, perm)
	}
	for i := range set {
		if used[i] {
			continue
		}
		used[i] = true
		if searchPermutation(ops, set, depth+1, append(perm, set[i]), used) {
			return true
		}
		used[i] = false
	}
	return false
}

// validLinearization 检验排列是否同时满足实时先后与寄存器语义。
func validLinearization(ops []opRecord, perm []int) bool {
	pos := make(map[int]int, len(perm))
	for i, idx := range perm {
		pos[idx] = i
	}
	for _, a := range perm {
		if !ops[a].finished {
			continue
		}
		for _, b := range perm {
			if ops[a].ret < ops[b].invoke && pos[a] > pos[b] {
				return false
			}
		}
	}
	value := 0
	for _, idx := range perm {
		next, ok := apply(ops[idx], value)
		if !ok {
			return false
		}
		value = next
	}
	return true
}

// randomHistory 生成一段随机历史：至多 8 个操作、至多 2 个未结束。
func randomHistory(rng *rand.Rand) *Checker {
	c := NewChecker()
	clients := []string{"alice", "bob", "carol"}
	n := 1 + rng.Intn(8)
	pending := 0
	for i := 0; i < n; i++ {
		client := clients[rng.Intn(len(clients))]
		var op Operation
		switch rng.Intn(3) {
		case 0:
			op = NewWrite(rng.Intn(3))
		case 1:
			op = NewRead()
		default:
			op = NewCAS(rng.Intn(3), rng.Intn(3))
		}
		invoke := rng.Intn(10)
		id, err := c.Begin(client, op, invoke)
		if err != nil {
			continue // 客户端忙或时序非法，跳过
		}
		// 至多 2 个操作保持未结束。
		if pending < 2 && rng.Intn(4) == 0 {
			pending++
			continue
		}
		ret := invoke + rng.Intn(4)
		var res Result
		switch op.Kind {
		case Write:
			res = WriteResult()
		case Read:
			res = ReadResult(rng.Intn(3))
		case CAS:
			res = CASResult(rng.Intn(2) == 0)
		}
		if err := c.End(id, ret, res); err != nil {
			panic(fmt.Sprintf("随机历史 End 失败: %v", err))
		}
	}
	return c
}

// snapshotOf 取出检查器当前的一致快照（测试用）。
func snapshotOf(c *Checker) []opRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	snap := make([]opRecord, len(c.ops))
	for i, rec := range c.ops {
		snap[i] = *rec
	}
	return snap
}

// TestRandomAgainstBruteForce 8 个操作以内与穷举参照随机对拍。
func TestRandomAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(20260930))
	const rounds = 200
	mismatch := 0
	for round := 0; round < rounds; round++ {
		c := randomHistory(rng)
		snap := snapshotOf(c)
		ok, witness := c.Check()
		ref := bruteForce(snap)
		if round < 5 || ok != ref {
			t.Logf("--- 第 %d 轮 ---", round)
			logHistory(t, c)
			logVerdict(t, c, ok, witness)
			t.Logf("穷举参照结论: %v", ref)
		}
		if ok != ref {
			mismatch++
			t.Errorf("第 %d 轮结论不一致: DFS=%v 穷举=%v", round, ok, ref)
			continue
		}
		if ok {
			verifyWitness(t, c, witness)
		}
	}
	if mismatch > 0 {
		t.Fatalf("共 %d 轮与穷举参照不一致", mismatch)
	}
	t.Logf("对拍完成: %d 轮全部一致", rounds)
}

// TestConcurrentAccess Begin/End/Check 可被并发调用且结果确定。
func TestConcurrentAccess(t *testing.T) {
	c := NewChecker()
	var wg sync.WaitGroup
	// 多个客户端并发上报各自串行的操作。
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			client := fmt.Sprintf("cli-%d", g)
			for i := 0; i < 4; i++ {
				invoke := g*4 + i
				id, err := c.Begin(client, NewCAS(0, g+1), invoke)
				if err != nil {
					continue
				}
				_ = c.End(id, invoke, CASResult(false))
			}
		}(g)
	}
	// 并发执行检查。
	results := make(chan []int, 8)
	verdicts := make(chan bool, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, witness := c.Check()
			verdicts <- ok
			results <- witness
		}()
	}
	wg.Wait()
	close(results)
	close(verdicts)
	// 最终快照确定后，反复检查结论与见证序必须一致。
	ok, witness := c.Check()
	logHistory(t, c)
	logVerdict(t, c, ok, witness)
	for i := 0; i < 5; i++ {
		ok2, witness2 := c.Check()
		if ok2 != ok || fmt.Sprint(witness2) != fmt.Sprint(witness) {
			t.Fatalf("并发后重复检查不一致: (%v,%v) vs (%v,%v)", ok, witness, ok2, witness2)
		}
	}
}
