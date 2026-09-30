package extent

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"testing"
)

// fill 生成 length 个以 seed 开头的可区分字节。
func fill(seed byte, length int) []byte {
	b := make([]byte, length)
	for i := range b {
		b[i] = seed + byte(i)
	}
	return b
}

// checkInvariant 校验核心不变量：
// 每个物理字节的引用计数恒等于映射到它的逻辑字节数，
// 已用物理字节数恒等于计数非零的物理字节数，
// 且映射表示唯一（有序、不重叠、相邻可合并者已合并）。
func checkInvariant(t *testing.T, v *Volume) {
	t.Helper()
	expected := make([]uint32, v.alloc.capacity)
	v.mu.RLock()
	defer v.mu.RUnlock()
	for _, f := range v.files {
		f.mu.RLock()
		for i, e := range f.exts {
			if i > 0 {
				prev := f.exts[i-1]
				if prev.end() > e.start {
					t.Fatalf("判定依据: 区段必须互不重叠, 但 %v 与 %v 重叠", prev, e)
				}
				if prev.end() == e.start && prev.phys+prev.length == e.phys {
					t.Fatalf("判定依据: 逻辑与物理都相接的相邻区段必须合并, 但 %v 与 %v 未合并", prev, e)
				}
			}
			for j := int64(0); j < e.length; j++ {
				expected[e.phys+j]++
			}
		}
		f.mu.RUnlock()
	}
	v.alloc.mu.Lock()
	defer v.alloc.mu.Unlock()
	for i := range expected {
		if expected[i] != v.alloc.refs[i] {
			t.Fatalf("判定依据: 物理字节 %d 的引用计数应恒等于映射到它的逻辑字节数 %d, 实际 %d",
				i, expected[i], v.alloc.refs[i])
		}
	}
	var nonzero int64
	for _, r := range v.alloc.refs {
		if r > 0 {
			nonzero++
		}
	}
	if nonzero != v.alloc.used {
		t.Fatalf("判定依据: 已用物理字节应恒等于计数非零的字节数 %d, 实际 %d", nonzero, v.alloc.used)
	}
}

func mustWrite(t *testing.T, v *Volume, f *File, off int64, data []byte) {
	t.Helper()
	if err := v.Write(f, off, data); err != nil {
		t.Fatalf("写入 [%d,%d) 失败: %v", off, off+int64(len(data)), err)
	}
}

// 一次写入把旧区段劈成三段：左段保留、中段被覆盖、右段保留。
func TestWriteSplitsExtentIntoThree(t *testing.T) {
	v := NewVolume(4096)
	f, _ := v.CreateFile("a", 100)

	mustWrite(t, v, f, 10, fill('A', 50)) // [10,60)
	t.Logf("输入: Write(a, 10, 50字节'A') -> 输出: 区段 %v, 已用 %d", f.Extents(), v.Used())

	mustWrite(t, v, f, 30, fill('B', 10)) // 覆盖 [30,40)
	exts := f.Extents()
	t.Logf("输入: Write(a, 30, 10字节'B') -> 输出: 区段 %v, 已用 %d", exts, v.Used())

	if len(exts) != 3 {
		t.Fatalf("判定依据: 旧区段 [10,60) 被 [30,40) 覆盖后应劈成三段, 实际 %d 段: %v", len(exts), exts)
	}
	if exts[0] != (extent{10, 20, 0}) || exts[1] != (extent{30, 10, 50}) || exts[2] != (extent{40, 20, 30}) {
		t.Fatalf("判定依据: 三段应为 [10,30)->phys0, [30,40)->phys50, [40,60)->phys30, 实际 %v", exts)
	}
	// 内容: 左段与右段仍是旧数据, 中段是新数据。
	got, err := v.Read(f, 10, 50)
	if err != nil {
		t.Fatal(err)
	}
	want := append(append(fill('A', 20), fill('B', 10)...), fill('A', 50)[30:]...)
	if !bytes.Equal(got, want) {
		t.Fatalf("判定依据: 读出应为 旧左段+新中段+旧右段\n实际 %v\n期望 %v", got, want)
	}
	t.Logf("输出: 读出内容 = 旧左段+新中段+旧右段, 一致")
	if v.Used() != 50 {
		t.Fatalf("判定依据: 被覆盖的 10 字节已回收, 已用应为 50, 实际 %d", v.Used())
	}
	checkInvariant(t, v)
}

// 克隆后覆盖源文件一半，克隆出的内容不变。
func TestCloneThenOverwriteSourceHalf(t *testing.T) {
	v := NewVolume(4096)
	src, _ := v.CreateFile("src", 100)
	dst, _ := v.CreateFile("dst", 100)

	orig := fill('A', 60)
	mustWrite(t, v, src, 0, orig) // src [0,60)
	if err := v.Clone(src, 0, dst, 10, 60); err != nil {
		t.Fatalf("克隆失败: %v", err)
	}
	t.Logf("输入: Clone(src[0,60) -> dst[10,70)) -> 输出: src=%v dst=%v, 已用 %d",
		src.Extents(), dst.Extents(), v.Used())
	if v.Used() != 60 {
		t.Fatalf("判定依据: 克隆只共享物理空间不新增, 已用应为 60, 实际 %d", v.Used())
	}

	// 覆盖源文件前半 [0,30)。
	mustWrite(t, v, src, 0, fill('X', 30))
	t.Logf("输入: Write(src, 0, 30字节'X') -> 输出: src=%v dst=%v, 已用 %d",
		src.Extents(), dst.Extents(), v.Used())

	got, err := v.Read(dst, 10, 60)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, orig) {
		t.Fatalf("判定依据: 覆盖源不影响克隆副本\n实际 %v\n期望 %v", got, orig)
	}
	t.Logf("输出: dst 读出 = 克隆时源内容, 一致")

	// 源的后半仍是原数据。
	tail, _ := v.Read(src, 30, 30)
	if !bytes.Equal(tail, orig[30:]) {
		t.Fatalf("判定依据: 源未被覆盖的后半应保持原数据")
	}
	// 已用 = 旧 60(后半 30 被 src+dst 共享, 前半 30 仅 dst) + 新 30 = 90。
	if v.Used() != 90 {
		t.Fatalf("判定依据: 已用应为 60+30=90, 实际 %d", v.Used())
	}
	checkInvariant(t, v)
}

// 截断落在区段中间：区段被劈短，超出部分回收。
func TestTruncateMiddleOfExtent(t *testing.T) {
	v := NewVolume(4096)
	f, _ := v.CreateFile("a", 100)

	mustWrite(t, v, f, 0, fill('A', 80)) // [0,80)
	if err := v.Truncate(f, 30); err != nil {
		t.Fatalf("截断失败: %v", err)
	}
	exts := f.Extents()
	t.Logf("输入: Truncate(a, 30) (原区段 [0,80)) -> 输出: 区段 %v, 已用 %d", exts, v.Used())

	if len(exts) != 1 || exts[0] != (extent{0, 30, 0}) {
		t.Fatalf("判定依据: 截断落在区段中间应劈短为 [0,30), 实际 %v", exts)
	}
	if v.Used() != 30 {
		t.Fatalf("判定依据: 超出部分 50 字节应回收, 已用应为 30, 实际 %d", v.Used())
	}
	if f.Length() != 30 {
		t.Fatalf("判定依据: 截断后长度应为 30, 实际 %d", f.Length())
	}
	// 截断区间之外读出越界。
	if _, err := v.Read(f, 20, 20); !errors.Is(err, ErrOutOfBounds) {
		t.Fatalf("判定依据: 超出新长度的读应报 ErrOutOfBounds, 实际 %v", err)
	}
	checkInvariant(t, v)
}

// 回收字节数：覆盖写释放旧空间，已用字节精确可预期。
func TestReclaimByteCount(t *testing.T) {
	v := NewVolume(4096)
	f, _ := v.CreateFile("a", 100)

	mustWrite(t, v, f, 0, fill('A', 40))
	mustWrite(t, v, f, 40, fill('B', 40))
	t.Logf("输入: Write [0,40)+[40,80) -> 输出: 已用 %d (期望 80)", v.Used())
	if v.Used() != 80 {
		t.Fatalf("判定依据: 两次写 40 字节, 已用应为 80, 实际 %d", v.Used())
	}

	// 完全覆盖 [0,80)：旧 80 字节全部回收，新 80 字节占用。
	mustWrite(t, v, f, 0, fill('C', 80))
	t.Logf("输入: Write [0,80) 全覆盖 -> 输出: 已用 %d (期望 80)", v.Used())
	if v.Used() != 80 {
		t.Fatalf("判定依据: 全覆盖后旧空间应全部回收, 已用仍为 80, 实际 %d", v.Used())
	}

	// 截断到 0：全部回收。
	if err := v.Truncate(f, 0); err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: Truncate(a, 0) -> 输出: 已用 %d (期望 0)", v.Used())
	if v.Used() != 0 {
		t.Fatalf("判定依据: 截断到 0 应回收全部字节, 已用应为 0, 实际 %d", v.Used())
	}
	checkInvariant(t, v)
}

// 相接合并：逻辑与物理都相接的相邻区段合并为一个。
func TestAdjacentMerge(t *testing.T) {
	v := NewVolume(4096)
	f, _ := v.CreateFile("a", 100)

	// 第一次写占物理 [0,30)；第二次写逻辑相接，分配器取最低地址得物理 [30,60)，
	// 逻辑与物理都相接，应合并为一个区段。
	mustWrite(t, v, f, 0, fill('A', 30))
	mustWrite(t, v, f, 30, fill('B', 30))
	exts := f.Extents()
	t.Logf("输入: Write [0,30) 再 Write [30,60) -> 输出: 区段 %v", exts)
	if len(exts) != 1 || exts[0] != (extent{0, 60, 0}) {
		t.Fatalf("判定依据: 逻辑与物理都相接的相邻区段必须合并为 {[0,60) phys 0}, 实际 %v", exts)
	}

	// 克隆制造逻辑相接但物理不相接的区段，不得合并。
	g, _ := v.CreateFile("b", 100)
	if err := v.Clone(f, 0, g, 0, 30); err != nil { // 共享 phys [0,30)
		t.Fatal(err)
	}
	mustWrite(t, v, g, 30, fill('C', 10)) // 新分配 phys [60,70)
	exts = g.Extents()
	t.Logf("输入: Clone(f[0,30)->g[0,30)) 再 Write(g,30,10) -> 输出: 区段 %v", exts)
	if len(exts) != 2 {
		t.Fatalf("判定依据: 逻辑相接但物理不相接(phys 30 vs 60)不得合并, 应为 2 段, 实际 %v", exts)
	}
	checkInvariant(t, v)
}

// 空洞读零：未映射区间读出零字节且不占物理空间。
func TestHoleReadsZero(t *testing.T) {
	v := NewVolume(4096)
	f, _ := v.CreateFile("a", 100)

	mustWrite(t, v, f, 40, fill('D', 20)) // 只映射 [40,60)
	got, err := v.Read(f, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: Read(a, 0, 100) (仅 [40,60) 有映射) -> 输出: 前40零+%d字节数据+后40零", 20)

	want := make([]byte, 100)
	copy(want[40:], fill('D', 20))
	if !bytes.Equal(got, want) {
		t.Fatalf("判定依据: 未映射区间应读出零字节")
	}
	if v.Used() != 20 {
		t.Fatalf("判定依据: 空洞不占物理空间, 已用应为 20, 实际 %d", v.Used())
	}

	// 克隆含空洞的区间，目标中仍是空洞。
	g, _ := v.CreateFile("g", 100)
	if err := v.Clone(f, 0, g, 0, 100); err != nil {
		t.Fatal(err)
	}
	gexts := g.Extents()
	got, _ = v.Read(g, 0, 100)
	t.Logf("输入: Clone(f[0,100)->g[0,100)) -> 输出: g 区段 %v", gexts)
	if len(gexts) != 1 || gexts[0] != (extent{40, 20, 0}) {
		t.Fatalf("判定依据: 源中空洞在目标中仍是空洞, g 应只有 [40,60) 一段, 实际 %v", gexts)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("判定依据: 克隆后目标空洞仍读零")
	}
	if v.Used() != 20 {
		t.Fatalf("判定依据: 克隆空洞不增加物理占用, 已用应为 20, 实际 %d", v.Used())
	}
	checkInvariant(t, v)
}

// 各类非法操作整体拒绝，且不改变任何映射、计数或已分配空间。
func TestRejections(t *testing.T) {
	v := NewVolume(64)
	f, _ := v.CreateFile("a", 100)
	g, _ := v.CreateFile("g", 100)
	mustWrite(t, v, f, 0, fill('A', 40)) // 物理 [0,40)

	snapshot := func() string {
		return fmt.Sprintf("exts=%v used=%d", f.Extents(), v.Used())
	}
	before := snapshot()

	cases := []struct {
		name string
		op   func() error
		want error
		why  string
	}{
		{"空写", func() error { return v.Write(f, 0, nil) }, ErrEmptyRange, "区间为空"},
		{"颠倒克隆", func() error { return v.Clone(f, 10, g, 0, -5) }, ErrEmptyRange, "区间长度为负"},
		{"空读", func() error { _, err := v.Read(f, 0, 0); return err }, ErrEmptyRange, "区间为空"},
		{"写越界", func() error { return v.Write(f, 90, fill('B', 20)) }, ErrOutOfBounds, "90+20>100 超出长度上限"},
		{"读越界", func() error { _, err := v.Read(f, 95, 10); return err }, ErrOutOfBounds, "95+10>100 超出长度上限"},
		{"克隆源越界", func() error { return v.Clone(f, 90, g, 0, 20) }, ErrOutOfBounds, "源区间超出长度上限"},
		{"克隆目标越界", func() error { return v.Clone(f, 0, g, 90, 20) }, ErrOutOfBounds, "目标区间超出长度上限"},
		{"同文件重叠克隆", func() error { return v.Clone(f, 0, f, 20, 30) }, ErrOverlap, "[0,30) 与 [20,50) 重叠"},
		{"截断越上限", func() error { return v.Truncate(f, 200) }, ErrOutOfBounds, "200>100 超出长度上限"},
		{"物理空间不足", func() error { return v.Write(g, 0, fill('C', 30)) }, ErrNoSpace, "仅剩 24 字节, 无法容纳 30"},
	}
	for _, c := range cases {
		err := c.op()
		t.Logf("输入: %s -> 输出: err=%v; 判定依据: %s", c.name, err, c.why)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: 期望 %v, 实际 %v", c.name, c.want, err)
		}
		if after := snapshot(); after != before {
			t.Fatalf("%s: 被拒绝的操作不得改变映射或已分配空间\n之前 %s\n之后 %s", c.name, before, after)
		}
	}
	t.Logf("输出: 全部拒绝后状态不变: %s", snapshot())
	checkInvariant(t, v)
}

// 并发：不同文件的读写与克隆并发执行，同文件修改串行，
// 读者只能看到某次修改之前或之后的完整内容。
func TestConcurrentAccess(t *testing.T) {
	v := NewVolume(1 << 20)
	const files = 8
	fs := make([]*File, files)
	for i := range fs {
		fs[i], _ = v.CreateFile(fmt.Sprintf("f%d", i), 4096)
		mustWrite(t, v, fs[i], 0, bytes.Repeat([]byte{byte('a' + i)}, 1024))
	}

	var wg sync.WaitGroup
	var readers sync.WaitGroup
	stop := make(chan struct{})

	// 读者：校验读到的内容必然是某一版完整内容（前缀可区分）。
	for i := 0; i < files; i++ {
		readers.Add(1)
		go func(f *File, seed byte) {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				buf, err := v.Read(f, 0, 1024)
				if err != nil {
					t.Errorf("读失败: %v", err)
					return
				}
				// 每版写入用同一种字节填充，读到混合内容即为撕裂。
				first := buf[0]
				for _, b := range buf {
					if b != first {
						t.Errorf("判定依据: 读者只能看到某次修改之前或之后的完整内容, 读到撕裂数据 (seed=%d)", seed)
						return
					}
				}
			}
		}(fs[i], byte('a'+i))
	}

	// 写者：对不同文件并发整段覆盖写（每次 1024 字节同一字节值）。
	for i := 0; i < files; i++ {
		wg.Add(1)
		go func(f *File, seed byte) {
			defer wg.Done()
			for round := 0; round < 50; round++ {
				data := bytes.Repeat([]byte{seed + byte(round%26)}, 1024)
				if err := v.Write(f, 0, data); err != nil {
					t.Errorf("写失败: %v", err)
					return
				}
			}
		}(fs[i], byte('a'+i))
	}

	// 克隆者：在不同文件之间并发克隆。
	for i := 0; i < files; i++ {
		wg.Add(1)
		go func(src, dst *File) {
			defer wg.Done()
			for round := 0; round < 20; round++ {
				if err := v.Clone(src, 0, dst, 1024, 512); err != nil {
					t.Errorf("克隆失败: %v", err)
					return
				}
			}
		}(fs[i], fs[(i+1)%files])
	}

	wg.Wait()
	close(stop)
	// 等读者退出后再校验不变量。
	readers.Wait()
	checkInvariant(t, v)
	t.Logf("输出: 并发写/读/克隆完成后不变量成立, 已用 %d", v.Used())
}

// 确定性：同一操作序列得到完全相同的映射与物理布局。
func TestDeterministicLayout(t *testing.T) {
	run := func() ([]extent, []extent, []byte) {
		v := NewVolume(256)
		a, _ := v.CreateFile("a", 100)
		b, _ := v.CreateFile("b", 100)
		mustWrite(t, v, a, 0, fill('A', 40))
		mustWrite(t, v, a, 60, fill('B', 20))
		if err := v.Clone(a, 10, b, 0, 50); err != nil {
			t.Fatal(err)
		}
		mustWrite(t, v, a, 20, fill('C', 30))
		if err := v.Truncate(b, 30); err != nil {
			t.Fatal(err)
		}
		v.alloc.mu.Lock()
		data := append([]byte(nil), v.alloc.data...)
		v.alloc.mu.Unlock()
		return a.Extents(), b.Extents(), data
	}
	ea1, eb1, d1 := run()
	ea2, eb2, d2 := run()
	t.Logf("输入: 同一操作序列执行两次 -> 输出: a=%v b=%v", ea1, eb1)
	if fmt.Sprint(ea1) != fmt.Sprint(ea2) || fmt.Sprint(eb1) != fmt.Sprint(eb2) || !bytes.Equal(d1, d2) {
		t.Fatalf("判定依据: 同一操作序列应得到完全相同的映射与物理布局\n第一次 a=%v b=%v\n第二次 a=%v b=%v", ea1, eb1, ea2, eb2)
	}
}
