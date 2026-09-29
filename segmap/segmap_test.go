package segmap

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

type bufLogger struct{ buf bytes.Buffer }

var bufLoggerMu sync.Mutex

func (b *bufLogger) Printf(format string, args ...any) {
	bufLoggerMu.Lock()
	defer bufLoggerMu.Unlock()
	fmt.Fprintf(&b.buf, format+"\n", args...)
}

func newLoggedStore(t *testing.T, size int64) (*Store, *bufLogger) {
	t.Helper()
	s := New(size)
	l := &bufLogger{}
	s.SetLogger(l)
	t.Cleanup(func() {
		t.Helper()
		bufLoggerMu.Lock()
		logText := strings.TrimRight(l.buf.String(), "\n")
		bufLoggerMu.Unlock()
		t.Logf("操作日志（输入 -> 输出 / 判定依据）:\n%s", logText)
	})
	return s, l
}

func mustWrite(t *testing.T, s *Store, name string, off int64, data string) {
	t.Helper()
	if err := s.Write(name, off, []byte(data)); err != nil {
		t.Fatalf("Write(%q,%d,%q) unexpected error: %v", name, off, data, err)
	}
}

func mustRead(t *testing.T, s *Store, name string, off, length int64) string {
	t.Helper()
	got, err := s.Read(name, off, length)
	if err != nil {
		t.Fatalf("Read(%q,%d,%d) unexpected error: %v", name, off, length, err)
	}
	return string(got)
}

func createTwo(t *testing.T, s *Store, a, b string) {
	t.Helper()
	if err := s.CreateFile(a); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateFile(b); err != nil {
		t.Fatal(err)
	}
}

// 一次写入把旧区段劈成三段：先写 [0,30)，再覆盖中间 [10,20)。
func TestWriteSplitsExtentIntoThree(t *testing.T) {
	s, _ := newLoggedStore(t, 4096)
	if err := s.CreateFile("f"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, "f", 0, strings.Repeat("A", 30))
	mustWrite(t, s, "f", 10, strings.Repeat("B", 10))

	es, ok := s.Extents("f")
	if !ok {
		t.Fatal("file missing")
	}
	want := []ExtentView{{0, 10, 0}, {10, 20, 30}, {20, 30, 20}}
	if fmt.Sprint(es) != fmt.Sprint(want) {
		t.Fatalf("extents = %v, want %v（判定：旧区段 [0,30)@0 被新写 [10,20) 劈成三段）", es, want)
	}
	got := mustRead(t, s, "f", 0, 30)
	wantData := strings.Repeat("A", 10) + strings.Repeat("B", 10) + strings.Repeat("A", 10)
	if got != wantData {
		t.Fatalf("data = %q, want %q", got, wantData)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// 不同文件并发读写/克隆；同文件修改串行，读者只见修改前或修改后的完整内容。
func TestConcurrentFilesAndSnapshotReads(t *testing.T) {
	s, _ := newLoggedStore(t, 1<<20)
	createTwo(t, s, "a", "b")
	const block = 16
	for i := 0; i < 8; i++ {
		mustWrite(t, s, "a", int64(i*block), strings.Repeat("A", block))
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})

	for r := 0; r < 4; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			old := strings.Repeat("A", block)
			neu := strings.Repeat("B", block)
			for {
				select {
				case <-stop:
					return
				default:
				}
				for i := int64(0); i < 8; i++ {
					got, err := s.Read("a", i*block, block)
					if err != nil {
						t.Errorf("read: %v", err)
						return
					}
					switch g := string(got); g {
					case old, neu:
					default:
						t.Errorf("torn read at block %d: %q（读者看到了写一半的内容）", i, g)
						return
					}
				}
			}
		}()
	}

	writerDone := make(chan struct{})
	go func() {
		for round := 0; round < 50; round++ {
			for i := 0; i < 8; i++ {
				ch := byte('A')
				if round%2 == 1 {
					ch = 'B'
				}
				if err := s.Write("a", int64(i*block), bytes.Repeat([]byte{ch}, block)); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}
		close(writerDone)
	}()

	for round := 0; round < 50; round++ {
		payload := fmt.Sprintf("b-%03d", round)
		payload += strings.Repeat(".", block-len(payload))
		if err := s.Write("b", int64(round%4)*block, []byte(payload)); err != nil {
			t.Fatalf("independent file write: %v", err)
		}
		if err := s.Clone("b", 0, "a", 200, block); err != nil {
			t.Fatalf("concurrent clone: %v", err)
		}
	}

	<-writerDone
	close(stop)
	wg.Wait()
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// 同一操作序列重复执行，映射与物理布局完全相同。
func TestDeterministicLayout(t *testing.T) {
	run := func() [][3]int64 {
		s := New(256)
		_ = s.CreateFile("x")
		_ = s.Write("x", 0, []byte("aaaa"))
		_ = s.Write("x", 10, []byte("bb"))
		_ = s.Truncate("x", 11)
		_ = s.CreateFile("y")
		_ = s.Write("y", 0, []byte("cccccccc"))
		_ = s.Write("x", 3, []byte("z"))
		es, _ := s.Extents("x")
		out := make([][3]int64, len(es))
		for i, e := range es {
			out[i] = [3]int64{e.LogicalStart, e.LogicalEnd, e.PhysicalStart}
		}
		return out
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); fmt.Sprint(got) != fmt.Sprint(first) {
			t.Fatalf("layout differs: %v vs %v", got, first)
		}
	}
}

// 空洞读零且不占物理空间；源空洞克隆到目标后仍是空洞。
func TestHolesReadZeroAndClonePreservesHoles(t *testing.T) {
	s, _ := newLoggedStore(t, 4096)
	createTwo(t, s, "src", "dst")
	mustWrite(t, s, "src", 0, "abc")
	mustWrite(t, s, "src", 8, "xyz")
	if used := s.UsedPhysicalBytes(); used != 6 {
		t.Fatalf("used = %d, want 6（中间 5 字节是空洞，不占物理空间）", used)
	}
	wantData := "abc\x00\x00\x00\x00\x00xyz"
	if got := mustRead(t, s, "src", 0, 11); got != wantData {
		t.Fatalf("hole data = %q, want %q", got, wantData)
	}
	if err := s.Clone("src", 0, "dst", 100, 11); err != nil {
		t.Fatal(err)
	}
	es, _ := s.Extents("dst")
	if fmt.Sprint(es) != "[{100 103 0} {108 111 3}]" {
		t.Fatalf("dst extents = %v（判定：源空洞 [3,8) 在目标仍为空洞，不产生映射）", es)
	}
	if got := mustRead(t, s, "dst", 100, 11); got != wantData {
		t.Fatalf("cloned hole data = %q, want %q", got, wantData)
	}
	if used := s.UsedPhysicalBytes(); used != 6 {
		t.Fatalf("used = %d, want 6（克隆只共享）", used)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// 各类拒绝原因可区分，且被拒绝操作不改变任何映射/计数/已分配空间。
func TestRejectionsAreAtomicAndDistinguishable(t *testing.T) {
	s, _ := newLoggedStore(t, 64)
	createTwo(t, s, "f", "g")
	mustWrite(t, s, "f", 0, "1234")

	cases := []struct {
		name string
		call func() error
		want error
	}{
		{"empty range", func() error { return s.Write("f", 0, nil) }, ErrInvalidRange},
		{"negative offset", func() error {
			_, err := s.Read("f", -1, 4)
			return err
		}, ErrInvalidRange},
		{"write beyond max", func() error { return s.Write("f", MaxFileLength, []byte("x")) }, ErrFileTooLarge},
		{"clone beyond max", func() error { return s.Clone("f", 0, "f", MaxFileLength, 1) }, ErrFileTooLarge},
		{"same-file clone overlap", func() error { return s.Clone("f", 0, "f", 2, 4) }, ErrSameFileOverlap},
		{"space exhausted", func() error { return s.Write("g", 0, make([]byte, 65)) }, ErrSpaceExhausted},
		{"truncate beyond max", func() error { return s.Truncate("f", MaxFileLength+1) }, ErrFileTooLarge},
	}
	extentsBefore, _ := s.Extents("f")
	usedBefore := s.UsedPhysicalBytes()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.call(); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
	extentsAfter, _ := s.Extents("f")
	if fmt.Sprint(extentsAfter) != fmt.Sprint(extentsBefore) || s.UsedPhysicalBytes() != usedBefore {
		t.Fatal("被拒绝的操作改变了映射或已分配空间")
	}
	if err := s.Clone("f", 0, "f", 4, 4); err != nil {
		t.Fatalf("abutting clone should succeed, got %v", err)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// 截断落在区段中间：只保留前缀，尾段计数归零后回收。
func TestTruncateInMiddleOfExtent(t *testing.T) {
	s, _ := newLoggedStore(t, 4096)
	if err := s.CreateFile("f"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, "f", 0, "abcdefghij")
	before := s.UsedPhysicalBytes()
	if err := s.Truncate("f", 4); err != nil {
		t.Fatal(err)
	}
	if s.Length("f") != 4 {
		t.Fatalf("length = %d, want 4", s.Length("f"))
	}
	es, _ := s.Extents("f")
	if fmt.Sprint(es) != "[{0 4 0}]" {
		t.Fatalf("extents = %v, want [{0 4 0}]（判定：[4,10) 被释放）", es)
	}
	if got := mustRead(t, s, "f", 0, 4); got != "abcd" {
		t.Fatalf("data = %q, want abcd", got)
	}
	if s.UsedPhysicalBytes() != before-6 {
		t.Fatalf("used after truncate = %d, want %d（回收 6 字节）", s.UsedPhysicalBytes(), before-6)
	}
	if c := s.Refcount(3); c != 1 {
		t.Fatalf("refcount at 3 = %d, want 1", c)
	}
	if c := s.Refcount(4); c != 0 {
		t.Fatalf("refcount at 4 = %d, want 0（已回收）", c)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// 回收字节数：计数归零的字节回到空闲池，first-fit 重新取最低地址。
func TestReclaimedBytesReusedLowestAddress(t *testing.T) {
	s, _ := newLoggedStore(t, 64)
	if err := s.CreateFile("a"); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, "a", 0, "0123456789")
	if err := s.Truncate("a", 0); err != nil {
		t.Fatal(err)
	}
	if used := s.UsedPhysicalBytes(); used != 0 {
		t.Fatalf("used = %d, want 0（全部回收）", used)
	}
	mustWrite(t, s, "a", 0, "zzz")
	es, _ := s.Extents("a")
	if len(es) != 1 || es[0].PhysicalStart != 0 {
		t.Fatalf("reused extent = %v, want physical start 0（判定：回收后 first-fit 取最低地址）", es)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// 相接合并：逻辑与物理都相接才合并；仅逻辑相接（物理不连续）保持独立。
func TestAdjacentExtentsMerged(t *testing.T) {
	s, _ := newLoggedStore(t, 4096)
	createTwo(t, s, "m", "h")
	mustWrite(t, s, "m", 0, "AAAAA")
	mustWrite(t, s, "m", 5, "BBBBB")
	es, _ := s.Extents("m")
	if fmt.Sprint(es) != "[{0 10 0}]" {
		t.Fatalf("merged extents = %v, want single [{0 10 0}]", es)
	}

	// h 占物理 [10,12)；克隆其后 1 字节（物理 11）到 m@10：
	// 逻辑与 [0,10) 相接，但物理起点 11 != 10，故不得合并。
	mustWrite(t, s, "h", 0, "HI")
	if err := s.Clone("h", 1, "m", 10, 1); err != nil {
		t.Fatal(err)
	}
	es, _ = s.Extents("m")
	if fmt.Sprint(es) != "[{0 10 0} {10 11 11}]" {
		t.Fatalf("non-phys-adjacent extents = %v, want two unmerged extents", es)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}

// 克隆后覆盖源文件一半：克隆内容保持不变（共享只读，写时复制）。
func TestCloneThenOverwriteSourceHalf(t *testing.T) {
	s, _ := newLoggedStore(t, 4096)
	createTwo(t, s, "src", "dst")
	mustWrite(t, s, "src", 0, strings.Repeat("S", 20))
	if err := s.Clone("src", 0, "dst", 0, 20); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{0, 5, 19} {
		if c := s.Refcount(at); c != 2 {
			t.Fatalf("refcount at %d = %d, want 2（克隆只增计数）", at, c)
		}
	}
	if used := s.UsedPhysicalBytes(); used != 20 {
		t.Fatalf("used = %d, want 20（克隆不复制物理字节）", used)
	}

	mustWrite(t, s, "src", 0, strings.Repeat("X", 10))
	if got := mustRead(t, s, "dst", 0, 20); got != strings.Repeat("S", 20) {
		t.Fatalf("dst changed after source overwrite: %q, want 20*S", got)
	}
	wantSrc := strings.Repeat("X", 10) + strings.Repeat("S", 10)
	if got := mustRead(t, s, "src", 0, 20); got != wantSrc {
		t.Fatalf("src = %q, want %q", got, wantSrc)
	}
	if c := s.Refcount(0); c != 1 {
		t.Fatalf("refcount at 0 = %d, want 1（源已解绑，仅 dst 引用）", c)
	}
	if c := s.Refcount(15); c != 2 {
		t.Fatalf("refcount at 15 = %d, want 2（后半段仍共享）", c)
	}
	if err := s.CheckInvariants(); err != nil {
		t.Fatal(err)
	}
}
