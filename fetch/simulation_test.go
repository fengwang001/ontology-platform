package fetch

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// naiveModel 是按规则逐条写成的朴素模拟，用作被测实现的对照基准。
type naiveModel struct {
	n, w, p int
	queues  [][]Record
	pos     []int
	rr      int
}

func newNaiveModel(n, w, p int) *naiveModel {
	return &naiveModel{
		n:      n,
		w:      w,
		p:      p,
		queues: make([][]Record, n),
		pos:    make([]int, n),
	}
}

func (m *naiveModel) append(partition, size int, payload string) int {
	off := len(m.queues[partition])
	m.queues[partition] = append(m.queues[partition], Record{
		Partition: partition,
		Offset:    off,
		Size:      size,
		Payload:   payload,
	})
	return off
}

// fetch 返回响应记录、下次轮转起点；无数据时 ok=false。
// log 记录每条消息的判定依据。
func (m *naiveModel) fetch(log func(string)) (records []Record, nextStart int, ok bool) {
	hasData := false
	for i := 0; i < m.n; i++ {
		if m.pos[i] < len(m.queues[i]) {
			hasData = true
		}
	}
	if !hasData {
		log("所有分区均无数据 -> ErrNoData")
		return nil, m.rr, false
	}
	total := 0
	lastTaken := -1
	for i := 0; i < m.n; i++ {
		part := (m.rr + i) % m.n
		partBytes := 0
		for m.pos[part] < len(m.queues[part]) {
			msg := m.queues[part][m.pos[part]]
			switch {
			case total == 0:
				log(fmt.Sprintf("分区%d 位点%d 大小%d: 响应总字节为0, 首条例外无视P/W取下",
					part, msg.Offset, msg.Size))
			case partBytes+msg.Size > m.p:
				log(fmt.Sprintf("分区%d 位点%d 大小%d: 分区已取%d + %d > P=%d, 不取且本分区结束",
					part, msg.Offset, msg.Size, partBytes, msg.Size, m.p))
				goto nextPartition
			case total+msg.Size > m.w:
				log(fmt.Sprintf("分区%d 位点%d 大小%d: 响应已取%d + %d > W=%d, 不取且本分区结束",
					part, msg.Offset, msg.Size, total, msg.Size, m.w))
				goto nextPartition
			default:
				log(fmt.Sprintf("分区%d 位点%d 大小%d: 分区%d+%d<=P=%d 且 总计%d+%d<=W=%d, 取走",
					part, msg.Offset, msg.Size, partBytes, msg.Size, m.p, total, msg.Size, m.w))
			}
			m.pos[part]++
			partBytes += msg.Size
			total += msg.Size
			records = append(records, msg)
			lastTaken = part
		}
		log(fmt.Sprintf("分区%d: 读到末尾结束", part))
	nextPartition:
	}
	m.rr = (lastTaken + 1) % m.n
	log(fmt.Sprintf("最后取到分区%d, 下次轮转起点=%d", lastTaken, m.rr))
	return records, m.rr, true
}

// TestAgainstNaiveSimulation 用随机操作序列把被测实现与朴素模拟逐步对照，
// 并在日志中打印输入、输出与判定依据。
func TestAgainstNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for trial := 0; trial < 20; trial++ {
		n := 1 + rng.Intn(4)
		w := 1 + rng.Intn(12)
		p := 1 + rng.Intn(12)
		a := mustNew(t, n, w, p)
		model := newNaiveModel(n, w, p)
		t.Logf("=== 试验 %d: N=%d W=%d P=%d ===", trial, n, w, p)

		for step := 0; step < 100; step++ {
			if rng.Intn(100) < 55 {
				part := rng.Intn(n)
				size := 1 + rng.Intn(15)
				payload := fmt.Sprintf("t%ds%d", trial, step)
				gotOff, err := a.Append(part, size, payload)
				if err != nil {
					t.Fatalf("Append 失败: %v", err)
				}
				wantOff := model.append(part, size, payload)
				if gotOff != wantOff {
					t.Fatalf("步%d Append 位点=%d, 模拟=%d", step, gotOff, wantOff)
				}
				t.Logf("步%d 输入: Append(分区%d, 大小%d, %q) -> 位点%d",
					step, part, size, payload, gotOff)
				continue
			}

			t.Logf("步%d 输入: Fetch (轮转起点=%d)", step, model.rr)
			wantRecords, wantNext, wantOK := model.fetch(func(s string) {
				t.Logf("  模拟判定: %s", s)
			})
			resp, err := a.Fetch()
			if !wantOK {
				if !errors.Is(err, ErrNoData) {
					t.Fatalf("步%d Fetch err=%v, want ErrNoData", step, err)
				}
				t.Logf("步%d 输出: ErrNoData", step)
			} else {
				if err != nil {
					t.Fatalf("步%d Fetch 意外失败: %v", step, err)
				}
				if !reflect.DeepEqual(resp.Records, wantRecords) {
					t.Fatalf("步%d Records=%v, 模拟=%v", step, resp.Records, wantRecords)
				}
				if resp.NextStart != wantNext {
					t.Fatalf("步%d NextStart=%d, 模拟=%d", step, resp.NextStart, wantNext)
				}
				sum := 0
				for _, r := range resp.Records {
					sum += r.Size
				}
				if sum != resp.TotalBytes {
					t.Fatalf("步%d TotalBytes=%d 与记录合计=%d 不符", step, resp.TotalBytes, sum)
				}
				t.Logf("步%d 输出: total=%d nextStart=%d records=%v",
					step, resp.TotalBytes, resp.NextStart, resp.Records)
			}

			snap := a.Snapshot()
			if !reflect.DeepEqual(snap.ConsumePositions, model.pos) {
				t.Fatalf("步%d 消费位置=%v, 模拟=%v", step, snap.ConsumePositions, model.pos)
			}
			if snap.RotationStart != model.rr {
				t.Fatalf("步%d 轮转起点=%d, 模拟=%d", step, snap.RotationStart, model.rr)
			}
		}
	}
}

// TestConcurrency 并发调用追加、拉取与查询，验证结果等价于某个串行顺序：
// 每条消息至多被取走一次，分区内位点升序且无空洞，响应满足预算规则。
func TestConcurrency(t *testing.T) {
	const (
		n          = 4
		w          = 50
		p          = 20
		producers  = 4
		consumers  = 3
		perProduce = 200
	)
	a := mustNew(t, n, w, p)

	var producerWg sync.WaitGroup
	for g := 0; g < producers; g++ {
		producerWg.Add(1)
		go func(g int) {
			defer producerWg.Done()
			for i := 0; i < perProduce; i++ {
				part := (g + i) % n
				size := 1 + (g*7+i)%30
				payload := fmt.Sprintf("g%d#%d", g, i)
				if _, err := a.Append(part, size, payload); err != nil {
					t.Errorf("Append 失败: %v", err)
					return
				}
			}
		}(g)
	}

	var mu sync.Mutex
	var responses []Response
	stop := make(chan struct{})
	var workerWg sync.WaitGroup
	for g := 0; g < consumers; g++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, err := a.Fetch()
				if errors.Is(err, ErrNoData) {
					continue
				}
				if err != nil {
					t.Errorf("Fetch 失败: %v", err)
					return
				}
				mu.Lock()
				responses = append(responses, *resp)
				mu.Unlock()
			}
		}()
	}
	workerWg.Add(1)
	go func() {
		defer workerWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = a.Snapshot()
			}
		}
	}()

	producerWg.Wait()
	close(stop)
	workerWg.Wait()

	// 排空剩余消息。
	for {
		resp, err := a.Fetch()
		if errors.Is(err, ErrNoData) {
			break
		}
		if err != nil {
			t.Fatalf("排空 Fetch 失败: %v", err)
		}
		responses = append(responses, *resp)
	}

	// 校验每条响应满足预算判定：除响应首条（首条例外）外，
	// 每条消息取走时分区累计 <= P 且响应累计 <= W。
	for i, resp := range responses {
		total := 0
		partBytes := map[int]int{}
		for j, r := range resp.Records {
			if j == 0 {
				total += r.Size
				partBytes[r.Partition] += r.Size
				continue
			}
			if partBytes[r.Partition]+r.Size > p {
				t.Fatalf("响应%d 记录%d 违反 P: 分区%d 已取%d + %d > %d",
					i, j, r.Partition, partBytes[r.Partition], r.Size, p)
			}
			if total+r.Size > w {
				t.Fatalf("响应%d 记录%d 违反 W: 已取%d + %d > %d", i, j, total, r.Size, w)
			}
			total += r.Size
			partBytes[r.Partition] += r.Size
		}
		if total != resp.TotalBytes {
			t.Fatalf("响应%d TotalBytes=%d 与记录合计=%d 不符", i, resp.TotalBytes, total)
		}
	}

	// 校验每条消息恰好被取走一次：分区内位点覆盖 0..count-1 无重复无空洞。
	seen := make([][]bool, n)
	snap := a.Snapshot()
	for part := 0; part < n; part++ {
		seen[part] = make([]bool, snap.ConsumePositions[part])
	}
	totalFetched := 0
	for _, resp := range responses {
		for _, r := range resp.Records {
			if r.Offset < 0 || r.Offset >= len(seen[r.Partition]) {
				t.Fatalf("位点越界: 分区%d 位点%d", r.Partition, r.Offset)
			}
			if seen[r.Partition][r.Offset] {
				t.Fatalf("消息被取走两次: 分区%d 位点%d", r.Partition, r.Offset)
			}
			seen[r.Partition][r.Offset] = true
			totalFetched++
		}
	}
	for part := 0; part < n; part++ {
		for off, got := range seen[part] {
			if !got {
				t.Fatalf("分区%d 位点%d 出现空洞", part, off)
			}
		}
	}
	t.Logf("并发校验通过: %d 次响应共取走 %d 条消息, 各分区消费位置=%v",
		len(responses), totalFetched, snap.ConsumePositions)
}
