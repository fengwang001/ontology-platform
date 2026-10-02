package compaction

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func f(id uint64, level int, min, max string, size int64) File {
	return File{ID: id, Level: level, Min: []byte(min), Max: []byte(max), Size: size}
}

func mustNew(t *testing.T, L int, caps []int64, X int64) *Picker {
	t.Helper()
	p, err := NewPicker(L, caps, X)
	if err != nil {
		t.Fatalf("NewPicker: %v", err)
	}
	return p
}

func mustAdd(t *testing.T, p *Picker, files ...File) {
	t.Helper()
	for _, file := range files {
		if err := p.AddFile(file); err != nil {
			t.Fatalf("AddFile %+v: %v", file, err)
		}
	}
}

func ids(files []File) []uint64 {
	out := make([]uint64, len(files))
	for i, file := range files {
		out[i] = file.ID
	}
	return out
}

func mustPick(t *testing.T, p *Picker) (inputs, overlaps []File) {
	t.Helper()
	inputs, overlaps, err := p.Pick()
	if err != nil {
		t.Fatalf("Pick: %v", err)
	}
	return inputs, overlaps
}

func mustPtr(t *testing.T, p *Picker, level int, want string) {
	t.Helper()
	key, ok := p.Pointer(level)
	if !ok || string(key) != want {
		t.Fatalf("Pointer(%d) = %q, %v; want %q", level, key, ok, want)
	}
}

func TestConstructorErrors(t *testing.T) {
	if _, err := NewPicker(1, nil, 10); !errors.Is(err, ErrTooFewLevels) {
		t.Fatalf("L=1: %v", err)
	}
	if _, err := NewPicker(3, []int64{10}, 10); !errors.Is(err, ErrCapCountMismatch) {
		t.Fatalf("cap count: %v", err)
	}
	if _, err := NewPicker(2, []int64{0}, 10); !errors.Is(err, ErrCapNonPositive) {
		t.Fatalf("cap zero: %v", err)
	}
	if _, err := NewPicker(2, []int64{10}, 0); !errors.Is(err, ErrXNonPositive) {
		t.Fatalf("X zero: %v", err)
	}
}

func TestAddFileValidationOrder(t *testing.T) {
	p := mustNew(t, 2, []int64{100}, 1000)
	mustAdd(t, p, f(1, 1, "b", "c", 10))

	cases := []struct {
		name string
		file File
		want error
	}{
		{"level out of range beats bad range", f(2, 9, "z", "a", 0), ErrLevelOutOfRange},
		{"bad range beats non-positive size", f(3, 1, "z", "a", 0), ErrBadRange},
		{"non-positive size beats duplicate id", f(1, 1, "x", "y", 0), ErrNonPositiveSize},
		{"duplicate id beats overlap", f(1, 1, "b", "c", 10), ErrDuplicateID},
		{"overlap", f(4, 1, "c", "d", 10), ErrOverlap},
		{"touching endpoints overlap", f(5, 1, "a", "b", 10), ErrOverlap},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.AddFile(tc.file); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}

	// 被拒绝的 AddFile 不改变文件集合。
	got, err := p.Files(1)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids(got), []uint64{1}) {
		t.Fatalf("files changed after rejected adds: %v", ids(got))
	}
}

func TestScoreExactlyOneCompacts(t *testing.T) {
	p := mustNew(t, 2, []int64{100}, 1000)
	mustAdd(t, p, f(1, 1, "a", "b", 60), f(2, 1, "c", "d", 40))

	inputs, overlaps := mustPick(t, p)
	if !reflect.DeepEqual(ids(inputs), []uint64{1}) {
		t.Fatalf("inputs = %v", ids(inputs))
	}
	if len(overlaps) != 0 {
		t.Fatalf("overlaps = %v", ids(overlaps))
	}
	mustPtr(t, p, 1, "a")
}

func TestScoreJustBelowOneDoesNothing(t *testing.T) {
	p := mustNew(t, 2, []int64{100}, 1000)
	mustAdd(t, p, f(1, 1, "a", "b", 99))

	if _, _, err := p.Pick(); !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("got %v, want ErrNothingToCompact", err)
	}
	// 被拒绝的 Pick 不推进指针。
	if _, ok := p.Pointer(1); ok {
		t.Fatal("pointer advanced after rejected pick")
	}
}

func TestScoreTiePrefersLowerLevel(t *testing.T) {
	p := mustNew(t, 3, []int64{100, 100}, 1000)
	mustAdd(t, p,
		f(1, 1, "a", "b", 100),
		f(2, 2, "a", "b", 100),
		f(3, 3, "a", "b", 100),
	)

	inputs, _ := mustPick(t, p)
	if !reflect.DeepEqual(ids(inputs), []uint64{1}) {
		t.Fatalf("inputs = %v, want level-1 file", ids(inputs))
	}
	if _, ok := p.Pointer(1); !ok {
		t.Fatal("level 1 pointer not set")
	}
	if _, ok := p.Pointer(2); ok {
		t.Fatal("level 2 pointer should be unset")
	}
}

func TestStartFileRotationAndWrap(t *testing.T) {
	p := mustNew(t, 2, []int64{10}, 1000)
	mustAdd(t, p,
		f(1, 1, "a", "a", 4),
		f(2, 1, "c", "c", 4),
		f(3, 1, "e", "e", 4),
	)

	// 第 1 次：ptr 为「无」，取第一个文件。
	inputs, _ := mustPick(t, p)
	if !reflect.DeepEqual(ids(inputs), []uint64{1}) {
		t.Fatalf("pick 1 inputs = %v", ids(inputs))
	}
	mustPtr(t, p, 1, "a")

	// 第 2 次：ptr 恰等于文件 1 的最小键 "a"，须严格大于，取文件 2。
	inputs, _ = mustPick(t, p)
	if !reflect.DeepEqual(ids(inputs), []uint64{2}) {
		t.Fatalf("pick 2 inputs = %v", ids(inputs))
	}
	mustPtr(t, p, 1, "c")

	// 第 3 次：取文件 3。
	inputs, _ = mustPick(t, p)
	if !reflect.DeepEqual(ids(inputs), []uint64{3}) {
		t.Fatalf("pick 3 inputs = %v", ids(inputs))
	}
	mustPtr(t, p, 1, "e")

	// 第 4 次：没有最小键大于 "e" 的文件，回绕取文件 1。
	inputs, _ = mustPick(t, p)
	if !reflect.DeepEqual(ids(inputs), []uint64{1}) {
		t.Fatalf("pick 4 inputs = %v", ids(inputs))
	}
	mustPtr(t, p, 1, "a")
}

// 扩张成功：T 多于起点、字节和严格小于 X、下层重叠集不变。
func TestExpansionSuccess(t *testing.T) {
	p := mustNew(t, 2, []int64{20}, 1000)
	mustAdd(t, p,
		f(1, 1, "a", "c", 10),
		f(2, 1, "d", "f", 10),
		f(3, 2, "b", "e", 10),
	)

	inputs, overlaps := mustPick(t, p)
	if !reflect.DeepEqual(ids(inputs), []uint64{1, 2}) {
		t.Fatalf("inputs = %v, want [1 2]", ids(inputs))
	}
	if !reflect.DeepEqual(ids(overlaps), []uint64{3}) {
		t.Fatalf("overlaps = %v, want [3]", ids(overlaps))
	}
	// 指针推进到本层输入中最小键最大者（文件 2 的 "d"）。
	mustPtr(t, p, 1, "d")
}

// 扩张失败一：T 只含起点（R 内没有其他同层文件）。
func TestExpansionFailsWhenTIsOnlyStart(t *testing.T) {
	p := mustNew(t, 2, []int64{10}, 1000)
	mustAdd(t, p,
		f(1, 1, "a", "b", 10),
		f(2, 1, "y", "z", 1),
		f(3, 2, "a", "b", 10),
	)

	inputs, overlaps := mustPick(t, p)
	if !reflect.DeepEqual(ids(inputs), []uint64{1}) {
		t.Fatalf("inputs = %v, want [1]", ids(inputs))
	}
	if !reflect.DeepEqual(ids(overlaps), []uint64{3}) {
		t.Fatalf("overlaps = %v, want [3]", ids(overlaps))
	}
}

// 扩张失败二：T 与 O 的字节总和恰等于 X（要求严格小于）。
func TestExpansionFailsWhenBytesEqualX(t *testing.T) {
	p := mustNew(t, 2, []int64{20}, 30)
	mustAdd(t, p,
		f(1, 1, "a", "c", 10),
		f(2, 1, "d", "f", 10),
		f(3, 2, "b", "e", 10),
	)

	inputs, overlaps := mustPick(t, p)
	if !reflect.DeepEqual(ids(inputs), []uint64{1}) {
		t.Fatalf("inputs = %v, want [1] (sum 30 == X)", ids(inputs))
	}
	if !reflect.DeepEqual(ids(overlaps), []uint64{3}) {
		t.Fatalf("overlaps = %v, want [3]", ids(overlaps))
	}
}

// 扩张失败三：扩张后下层重叠集变大。
func TestExpansionFailsWhenLowerOverlapGrows(t *testing.T) {
	p := mustNew(t, 2, []int64{20}, 1000)
	mustAdd(t, p,
		f(1, 1, "a", "c", 10),
		f(2, 1, "d", "f", 10),
		f(3, 2, "b", "d", 10),
		f(4, 2, "e", "g", 10),
	)

	// 起点文件 1 [a,c]：O={3}，R=[a,d]，T={1,2}，T 并范围 [a,f]
	// 在下层还与文件 4 [e,g] 相交，O'={3,4} != O，故不扩张。
	inputs, overlaps := mustPick(t, p)
	if !reflect.DeepEqual(ids(inputs), []uint64{1}) {
		t.Fatalf("inputs = %v, want [1]", ids(inputs))
	}
	if !reflect.DeepEqual(ids(overlaps), []uint64{3}) {
		t.Fatalf("overlaps = %v, want [3]", ids(overlaps))
	}
}

func TestConcurrentCallsSerialize(t *testing.T) {
	p := mustNew(t, 3, []int64{50, 50}, 100)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := uint64(g*1000 + i + 1)
				// 每个 goroutine 使用互不重叠的键区间，避免合法 Add 被拒绝。
				key := []byte{byte(g), byte(i)}
				_ = p.AddFile(File{ID: id, Level: 1 + i%3, Min: key, Max: key, Size: 1})
				_, _, _ = p.Pick()
				_, _ = p.Files(1 + i%3)
				_, _ = p.Pointer(1 + i%2)
			}
		}(g)
	}
	wg.Wait()

	// 不变量：任何时刻同层文件互不相交（此处验证最终状态）。
	for level := 1; level <= 3; level++ {
		files, err := p.Files(level)
		if err != nil {
			t.Fatal(err)
		}
		for i := 1; i < len(files); i++ {
			if string(files[i-1].Max) >= string(files[i].Min) {
				t.Fatalf("level %d files overlap: %v", level, ids(files))
			}
		}
	}
}

// 相同调用序列重放得到完全相同的选择与指针。
func TestDeterministicReplay(t *testing.T) {
	run := func() ([][]uint64, []string) {
		p := mustNew(t, 3, []int64{30, 40}, 25)
		mustAdd(t, p,
			f(1, 1, "a", "c", 12),
			f(2, 1, "d", "f", 12),
			f(3, 1, "g", "i", 12),
			f(4, 2, "b", "e", 10),
			f(5, 2, "h", "j", 10),
			f(6, 3, "a", "z", 10),
		)
		var picks [][]uint64
		var ptrs []string
		for i := 0; i < 6; i++ {
			inputs, _, err := p.Pick()
			if err != nil {
				picks = append(picks, nil)
			} else {
				picks = append(picks, ids(inputs))
			}
			for level := 1; level <= 2; level++ {
				key, ok := p.Pointer(level)
				if ok {
					ptrs = append(ptrs, string(key))
				} else {
					ptrs = append(ptrs, "<nil>")
				}
			}
		}
		return picks, ptrs
	}
	p1, ptr1 := run()
	p2, ptr2 := run()
	if !reflect.DeepEqual(p1, p2) || !reflect.DeepEqual(ptr1, ptr2) {
		t.Fatalf("replay mismatch:\n%v %v\n%v %v", p1, ptr1, p2, ptr2)
	}
}
