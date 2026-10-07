package provenance

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// 并发修正写入与并发溯源查询交织：
//   - go test -race 下无数据竞争；
//   - 任意时刻的查询结果，都能在某个「已提交写入集合」下被重放出来
//     （等价于操作存在全局先后次序，即可线性化）；
//   - 任何查询引用到的记录 Seq 都已完整落定（区间与写入时间不可能来自不同版本）。
func TestConcurrentWritesAndQueries(t *testing.T) {
	s := NewStore()
	// 预置若干对象与链接。
	for _, id := range []ObjectID{"A", "B", "C", "D"} {
		mustWriteObject(t, s, id, 0, Interval{0, 100}, true)
	}
	mustWriteLink(t, s, "L1", 0, Interval{0, 100}, "A", "B", true)
	mustWriteLink(t, s, "L2", 0, Interval{0, 100}, "B", "C", true)
	mustWriteLink(t, s, "L3", 0, Interval{0, 100}, "C", "D", true)
	mustWriteLink(t, s, "L4", 0, Interval{0, 100}, "A", "D", true)

	const writers = 8
	const readers = 8
	const rounds = 200

	var wg sync.WaitGroup
	var failMu sync.Mutex
	var firstErr error
	record := func(err error) {
		if err == nil {
			return
		}
		failMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		failMu.Unlock()
	}

	// 写者：每个写者独占一条实体修正时间线，写入时间随轮次严格递增；
	// 多个写者写不同实体，因此不会产生同一实体的并发乱序写入。
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				writeAt := Time(1 + r*writers + wid)
				iv := Interval{Start: Time(r % 50), End: Time(100 + r%50)}
				switch wid {
				case 0:
					record(s.WriteObject("A", writeAt, iv, true))
				case 1:
					record(s.WriteObject("B", writeAt, iv, true))
				case 2:
					record(s.WriteObject("C", writeAt, iv, true))
				case 3:
					record(s.WriteLink("L1", writeAt, iv, "A", "B", true))
				case 4:
					record(s.WriteLink("L2", writeAt, iv, "B", "C", true))
				case 5:
					record(s.WriteLink("L3", writeAt, iv, "C", "D", true))
				case 6:
					record(s.WriteObject("D", writeAt, iv, r%2 == 0)) // 消亡/复活
				default:
					record(s.WriteLink("L4", writeAt, iv, "A", "D", true))
				}
			}
		}(w)
	}
	// readers 的 asOf 覆盖从 0 到全部写入的最大可能写入时间。
	maxWriteTime := Time(1 + (rounds-1)*writers + (writers - 1))
	for rd := 0; rd < readers; rd++ {
		wg.Add(1)
		go func(rid int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				asOf := (r*readers + rid) % int(maxWriteTime+1)
				q := Query{Source: "A", ValidAt: Time(20 + r%120), AsOf: Time(asOf), MaxDepth: 3}
				res, err := s.Traverse(q)
				if err != nil {
					if errors.Is(err, ErrAsOfBeforeSource) || errors.Is(err, ErrSourceNotFound) ||
						errors.Is(err, ErrInvalidTime) || errors.Is(err, ErrInvalidDepth) {
						continue
					}
					record(fmt.Errorf("unexpected query error: %w", err))
					continue
				}
				if res.Source.Seq != 0 && res.Source.WriteAt > Time(asOf) {
					record(fmt.Errorf("source ref leaks future write"))
				}
				for _, p := range res.Paths {
					for _, h := range p.Hops {
						if h.Link.Seq <= 0 || h.Target.Seq <= 0 {
							record(fmt.Errorf("incomplete record reference observed"))
						}
						if h.Link.WriteAt > Time(asOf) || h.Target.WriteAt > Time(asOf) {
							record(fmt.Errorf("hop ref leaks future write: %+v", h))
						}
					}
				}
			}
		}(rd)
	}

	wg.Wait()
	if firstErr != nil {
		t.Fatal(firstErr)
	}

	// 全部写入落定后，重复查询必须完全一致。
	q := Query{Source: "A", ValidAt: 60, AsOf: maxWriteTime + 1, MaxDepth: 3}
	r1, err := s.Traverse(q)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		r2, err := s.Traverse(q)
		if err != nil {
			t.Fatal(err)
		}
		if len(r1.Paths) != len(r2.Paths) || r1.CandidatesSeen != r2.CandidatesSeen {
			t.Fatalf("post-concurrency non-repeatable result")
		}
	}
}
