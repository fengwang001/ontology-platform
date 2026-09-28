package ontology

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func TestJudgeLoggerOutput(t *testing.T) {
	var buf bytes.Buffer
	s := NewStore(10).SetLogger(NewTextJudgeLogger(&buf))

	_, err := s.Apply([]Event{
		{Seq: 1, Key: "k1", Op: OpInsert, After: Row{"v": "1"}},
		// 前像值不符 -> 冲突
		{Seq: 2, Key: "k1", Op: OpUpdate, Before: Row{"v": "9"}, After: Row{"v": "2"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	// 非法批 -> 拒绝日志
	_, _ = s.Apply([]Event{
		{Seq: 3, Key: "k1", Op: "bogus"},
	})

	log := buf.String()
	t.Logf("判定日志:\n%s", log)

	// 每条日志都包含输入（seq/key/op/前后像）、判定结果与依据
	for _, want := range []string{
		"seq=1", "key=\"k1\"", "op=insert",
		"=> applied", "依据",
		"seq=2", "op=update", "=> conflict:before_mismatch",
		"seq=3", "=> rejected:invalid_event",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("日志缺少 %q:\n%s", want, log)
		}
	}
	// 前像/当前行内容应出现在依据中（formatRow 渲染为 k="v"）
	if !strings.Contains(log, `v="9"`) || !strings.Contains(log, `v="1"`) {
		t.Fatalf("日志应打印前像与当前行内容:\n%s", log)
	}
}

func TestConcurrentReadsSeeBatchBoundaries(t *testing.T) {
	s := NewStore(1_000_000)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	// 并发读取者：每次快照要么看到偶数批边界（每批 10 行全部可见），
	// 要么看到之前的边界；绝不会看到半批。
	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := s.Snapshot()
				n := snap.Len()
				// 已应用的批每批新增 10 行；行数必须始终是 10 的整数倍。
				if n%10 != 0 {
					t.Errorf("读到半批状态: rows=%d", n)
					return
				}
				// 一致性交叉校验：Len 与 Keys/Get 自洽
				if len(snap.Keys()) != n {
					t.Errorf("Keys/Len 不一致: %d vs %d", len(snap.Keys()), n)
					return
				}
				_ = s.LastSeq()
			}
		}()
	}

	// 写入者：每批 10 个插入，序号连续。
	const batches = 200
	for b := 0; b < batches; b++ {
		evs := make([]Event, 0, 10)
		for i := 0; i < 10; i++ {
			seq := int64(b*10 + i + 1)
			evs = append(evs, Event{
				Seq: seq, Key: "k" + strconv.FormatInt(seq, 10), Op: OpInsert,
				After: Row{"v": "x"},
			})
		}
		if _, err := s.Apply(evs); err != nil {
			t.Fatalf("batch %d: %v", b, err)
		}
	}
	close(stop)
	wg.Wait()

	if got := s.Snapshot().Len(); got != batches*10 {
		t.Fatalf("最终行数 = %d, want %d", got, batches*10)
	}
	if s.LastSeq() != int64(batches*10) {
		t.Fatalf("LastSeq = %d, want %d", s.LastSeq(), batches*10)
	}
}
