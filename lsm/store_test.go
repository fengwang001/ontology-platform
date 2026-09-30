package lsm

import (
	"errors"
	"fmt"
	"testing"
)

func testOptions() Options {
	return Options{MemtableCapacity: 2, Fanout: 2, MaxLevels: 3}
}

func openTemp(t *testing.T, opts Options) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), opts)
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	return s
}

func mustGet(t *testing.T, s *Store, key string) (string, bool) {
	t.Helper()
	v, found, err := s.Get([]byte(key))
	if err != nil {
		t.Fatalf("Get(%q) 出错: %v", key, err)
	}
	return string(v), found
}

func putKV(t *testing.T, s *Store, key, value string) {
	t.Helper()
	if err := s.Put([]byte(key), []byte(value)); err != nil {
		t.Fatalf("Put(%q) 失败: %v", key, err)
	}
	t.Logf("写入 Put(%q, %q)，当前各层段数=%v", key, value, s.levelSizes())
}

func delKey(t *testing.T, s *Store, key string) {
	t.Helper()
	if err := s.Delete([]byte(key)); err != nil {
		t.Fatalf("Delete(%q) 失败: %v", key, err)
	}
	t.Logf("写入 Delete(%q)（墓碑），当前各层段数=%v", key, s.levelSizes())
}

func checkKey(t *testing.T, s *Store, key, want string) {
	t.Helper()
	got, found := mustGet(t, s, key)
	if !found || got != want {
		t.Fatalf("Get(%q) = (%q, %v)，判定依据: 最新写入为 %q，应命中", key, got, found, want)
	}
	t.Logf("读取 Get(%q) = %q，判定依据: 最新写入为 %q，一致", key, got, want)
}

func checkAbsent(t *testing.T, s *Store, key, reason string) {
	t.Helper()
	got, found := mustGet(t, s, key)
	if found {
		t.Fatalf("Get(%q) = (%q, true)，判定依据: %s，应未命中", key, got, reason)
	}
	t.Logf("读取 Get(%q) 未命中，判定依据: %s，一致", key, reason)
}

// TestLevelCascade 验证缓存冻结进入第 0 层，并随段数达到扇出阈值逐级级联到更深层。
func TestLevelCascade(t *testing.T) {
	s := openTemp(t, testOptions())
	defer s.Close()

	// 容量 2、扇出 2、3 层：每 2 条写冻结一次；
	// 第 8 条写后应级联出第 2 层段：L0 两次合并进 L1，L1 再合并进 L2。
	for i := 1; i <= 8; i++ {
		putKV(t, s, fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}
	sizes := s.levelSizes()
	t.Logf("级联后各层段数=%v，判定依据: 容量2/扇出2 下 8 条写应全部合并进第 2 层", sizes)
	if sizes[0] != 0 || sizes[1] != 0 || sizes[2] != 1 {
		t.Fatalf("层分布 %v 不符合预期 [0 0 1]", sizes)
	}
	for i := 1; i <= 8; i++ {
		checkKey(t, s, fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
	}
	if err := s.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	t.Log("自检通过: 所有段可解码，层级不变量成立")
}

// TestTombstoneRetention 验证墓碑在非最大层合并时保留、在最大层合并时丢弃。
func TestTombstoneRetention(t *testing.T) {
	opts := Options{MemtableCapacity: 1, Fanout: 2, MaxLevels: 2}
	s := openTemp(t, opts)
	defer s.Close()

	putKV(t, s, "k", "v1")
	delKey(t, s, "k")
	// 两次写使 L0 达到扇出，合并进 L1（非最大层自合并，L1 是最大层但合并源是 L0）。
	if lvl, id, ok := s.findTombstone("k"); !ok {
		t.Fatal("判定依据: 合并进最大层时更旧段可能仍有该键，墓碑必须保留，但未找到")
	} else {
		t.Logf("墓碑保留在层 %d 段 %d，判定依据: 合并未发生在最大层自身，不得丢弃", lvl, id)
	}
	checkAbsent(t, s, "k", "最新记录是墓碑")

	// 再写两条无关键，触发最大层自合并，此时全系统更早写入都在输入段内，墓碑可丢弃。
	putKV(t, s, "a", "1")
	putKV(t, s, "b", "2")
	if _, _, ok := s.findTombstone("k"); ok {
		t.Fatal("判定依据: 最大层自合并已覆盖全部更早写入，墓碑应被丢弃")
	} else {
		t.Log("墓碑已在最大层合并时丢弃，判定依据: 不存在更早写入，符合保留规则")
	}
	checkAbsent(t, s, "k", "墓碑已丢弃且该键无其它记录")
	checkKey(t, s, "a", "1")
	checkKey(t, s, "b", "2")
}

// TestInvalidArguments 验证非法参数与空键被整体拒绝，且失败不改变已有状态。
func TestInvalidArguments(t *testing.T) {
	if _, err := Open(t.TempDir(), Options{MemtableCapacity: 0, Fanout: 2, MaxLevels: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("容量为 0 应返回 ErrInvalidArgument，得到 %v", err)
	}
	if _, err := Open(t.TempDir(), Options{MemtableCapacity: 1, Fanout: 1, MaxLevels: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("扇出为 1 应返回 ErrInvalidArgument，得到 %v", err)
	}
	if _, err := Open("", Options{MemtableCapacity: 1, Fanout: 2, MaxLevels: 1}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("空目录应返回 ErrInvalidArgument，得到 %v", err)
	}
	t.Log("非法配置均被 ErrInvalidArgument 拒绝，原因可区分")

	s := openTemp(t, testOptions())
	defer s.Close()
	putKV(t, s, "x", "1")
	before := s.levelSizes()

	if err := s.Put(nil, []byte("v")); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("空键 Put 应返回 ErrEmptyKey，得到 %v", err)
	}
	if err := s.Delete(nil); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("空键 Delete 应返回 ErrEmptyKey，得到 %v", err)
	}
	if _, _, err := s.Get(nil); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("空键 Get 应返回 ErrEmptyKey，得到 %v", err)
	}
	if err := s.Put([]byte("big"), make([]byte, maxValueSize+1)); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("超大值应返回 ErrInvalidArgument，得到 %v", err)
	}
	after := s.levelSizes()
	t.Logf("失败前层分布=%v，失败后层分布=%v，判定依据: 失败不得改变缓存、段与层分布", before, after)
	if fmt.Sprint(before) != fmt.Sprint(after) {
		t.Fatalf("失败后层分布改变: %v -> %v", before, after)
	}
	checkKey(t, s, "x", "1")
}
