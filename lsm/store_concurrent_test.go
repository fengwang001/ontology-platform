package lsm

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// segmentPath 返回目录中唯一的段文件路径。
func segmentPath(t *testing.T, dir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.kvs"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("期望目录中只有 1 个段文件，得到 %v (err=%v)", matches, err)
	}
	return matches[0]
}

// TestSegmentTruncationRecovery 验证段尾部的不完整记录被安全截断，已有数据不受影响。
func TestSegmentTruncationRecovery(t *testing.T) {
	dir := t.TempDir()
	opts := Options{MemtableCapacity: 2, Fanout: 2, MaxLevels: 2}
	s, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	putKV(t, s, "k1", "v1")
	putKV(t, s, "k2", "v2") // 触发冻结，生成一个段
	if err := s.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	// 模拟崩溃遗留：在段文件尾部追加半条记录。
	path := segmentPath(t, dir)
	partial := encodeRecord(Record{Key: []byte("ghost"), Value: []byte("x"), Seq: 99})
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(partial[:len(partial)/2]); err != nil {
		t.Fatal(err)
	}
	f.Close()
	t.Logf("向段文件 %s 尾部追加 %d 字节不完整记录", filepath.Base(path), len(partial)/2)

	s2, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("截断恢复后 Open 应成功，得到 %v", err)
	}
	defer s2.Close()
	checkKey(t, s2, "k1", "v1")
	checkKey(t, s2, "k2", "v2")
	checkAbsent(t, s2, "ghost", "不完整记录已被安全截断，不得出现")
	if err := s2.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	t.Log("判定依据: 尾部不完整记录被截断，已提交记录完整可读")
}

// TestCorruptSegmentRejected 验证加载损坏段整体失败且原因可区分，已有数据不被破坏。
func TestCorruptSegmentRejected(t *testing.T) {
	dir := t.TempDir()
	opts := Options{MemtableCapacity: 2, Fanout: 2, MaxLevels: 2}
	s, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	putKV(t, s, "k1", "v1")
	putKV(t, s, "k2", "v2")
	if err := s.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	path := segmentPath(t, dir)
	orig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), orig...)
	corrupt[recordHeaderSize] ^= 0xFF // 破坏首条记录的键内容，使 CRC 校验失败
	if err := os.WriteFile(path, corrupt, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("破坏段文件 %s 首条记录的负载字节", filepath.Base(path))

	if _, err := Open(dir, opts); !errors.Is(err, ErrCorruptSegment) {
		t.Fatalf("损坏段应整体拒绝并返回 ErrCorruptSegment，得到 %v", err)
	}
	t.Log("判定依据: CRC 校验失败 -> ErrCorruptSegment，打开整体失败")

	// 恢复文件后应能正常打开，证明失败未改变已有数据。
	if err := os.WriteFile(path, orig, 0o644); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir, opts)
	if err != nil {
		t.Fatalf("恢复后 Open 应成功，得到 %v", err)
	}
	defer s2.Close()
	checkKey(t, s2, "k1", "v1")
	checkKey(t, s2, "k2", "v2")
}

// TestConcurrentReadsDuringMerge 验证合并期间并发读只能看到合并前或合并后的完整状态。
func TestConcurrentReadsDuringMerge(t *testing.T) {
	s := openTemp(t, Options{MemtableCapacity: 4, Fanout: 2, MaxLevels: 3})
	defer s.Close()

	const keys = 16
	const rounds = 200
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errCh := make(chan string, 64)

	// 写者：对同一批键反复递增值，持续触发冻结与层合并。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for round := 1; round <= rounds; round++ {
			for k := 0; k < keys; k++ {
				key := fmt.Sprintf("k%d", k)
				val := fmt.Sprintf("gen-%06d", round)
				if err := s.Put([]byte(key), []byte(val)); err != nil {
					select {
					case errCh <- fmt.Sprintf("Put 失败: %v", err):
					default:
					}
					return
				}
			}
		}
		close(stop)
	}()

	// 读者：同一键读到的代际必须单调不减（否则说明读到了混合两代的中间态）。
	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			lastGen := make(map[string]int)
			for {
				select {
				case <-stop:
					return
				default:
				}
				key := fmt.Sprintf("k%d", rng.Intn(keys))
				v, found, err := s.Get([]byte(key))
				if err != nil {
					errCh <- fmt.Sprintf("Get 失败: %v", err)
					return
				}
				if !found {
					continue
				}
				var gen int
				fmt.Sscanf(string(v), "gen-%d", &gen)
				if gen < lastGen[key] {
					errCh <- fmt.Sprintf("键 %s 读到回退: 代际 %d < 已见 %d（混合中间态）", key, gen, lastGen[key])
					return
				}
				lastGen[key] = gen
			}
		}(int64(r) + 1)
	}

	// 自检者与读写并发运行。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			if err := s.Check(); err != nil {
				errCh <- fmt.Sprintf("并发自检失败: %v", err)
				return
			}
		}
	}()

	wg.Wait()
	close(errCh)
	for msg := range errCh {
		t.Fatal(msg)
	}
	for k := 0; k < keys; k++ {
		checkKey(t, s, fmt.Sprintf("k%d", k), fmt.Sprintf("gen-%06d", rounds))
	}
	t.Logf("判定依据: %d 轮写入期间并发读未观察到代际回退，读到的均为完整一代状态", rounds)
}

// TestReplayAgainstModel 按时间顺序重放随机写/删，并与内存模型逐键核对。
func TestReplayAgainstModel(t *testing.T) {
	s := openTemp(t, Options{MemtableCapacity: 3, Fanout: 2, MaxLevels: 3})
	defer s.Close()

	rng := rand.New(rand.NewSource(42))
	model := make(map[string]string)
	const ops = 300
	for i := 0; i < ops; i++ {
		key := fmt.Sprintf("k%d", rng.Intn(20))
		if rng.Intn(4) == 0 {
			delete(model, key)
			if err := s.Delete([]byte(key)); err != nil {
				t.Fatalf("Delete(%q) 失败: %v", key, err)
			}
			t.Logf("重放[%d] Delete(%q)，模型删除该键", i, key)
		} else {
			val := fmt.Sprintf("v%d", i)
			model[key] = val
			if err := s.Put([]byte(key), []byte(val)); err != nil {
				t.Fatalf("Put(%q) 失败: %v", key, err)
			}
			t.Logf("重放[%d] Put(%q, %q)，模型记录最新值", i, key, val)
		}
	}
	// 全键空间核对：存在的键比对最新值，不存在的键确认未命中。
	for k := 0; k < 20; k++ {
		key := fmt.Sprintf("k%d", k)
		want, ok := model[key]
		got, found := mustGet(t, s, key)
		if ok {
			if !found || got != want {
				t.Fatalf("重放核对失败: Get(%q)=(%q,%v)，模型最新值=%q", key, got, found, want)
			}
			t.Logf("核对 Get(%q)=%q，判定依据: 按时间顺序重放的最新写入为 %q，一致", key, got, want)
		} else {
			if found {
				t.Fatalf("重放核对失败: Get(%q) 命中 %q，但模型中该键已删除或从未写入", key, got)
			}
			t.Logf("核对 Get(%q) 未命中，判定依据: 模型中该键已删除或从未写入，一致", key)
		}
	}
	if err := s.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	t.Logf("重放 %d 个操作后存储与模型完全一致，自检通过", ops)
}
