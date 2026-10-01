package ontology

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"testing"
)

func hexKey(b []byte) string {
	var out []byte
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			out = append(out, c)
		} else {
			out = append(out, []byte(fmt.Sprintf("\\x%02X", c))...)
		}
	}
	return string(out)
}

func TestMakeSepFixed(t *testing.T) {
	cases := []struct {
		name   string
		last   []byte
		next   []byte
		want   []byte
		reason string
	}{
		{
			name:   "shorten abcdefg/abzzz -> abd",
			last:   []byte("abcdefg"),
			next:   []byte("abzzz"),
			want:   []byte("abd"),
			reason: "d=2, b='c'(0x63), b+1='d'(0x64) < next[2]='z'(0x7A)，故取前缀+单字节",
		},
		{
			name:   "b+1 equals next[d], no shorten",
			last:   []byte("abczzz"),
			next:   []byte("abdqqq"),
			want:   []byte("abczzz"),
			reason: "d=2, b='c', b+1='d' 不严格小于 next[2]='d'，退回 sep=last",
		},
		{
			name:   "last is prefix of next",
			last:   []byte("abc"),
			next:   []byte("abcdef"),
			want:   []byte("abc"),
			reason: "d=len(last)=3（last 是 next 前缀），sep=last",
		},
		{
			name:   "b is 0xFF, cannot increment",
			last:   []byte{'a', 0xFF, 0x00},
			next:   []byte{'b', 0x00, 0x00},
			want:   []byte{'a', 0xFF, 0x00},
			reason: "d=1, last[1]=0xFF 不满足 b<0xFF，退回 sep=last",
		},
		{
			name:   "non adjacent high bytes still shorten",
			last:   []byte{0x10, 0x20},
			next:   []byte{0x10, 0x40},
			want:   []byte{0x10, 0x21},
			reason: "d=1, b=0x20, b+1=0x21 < 0x40，缩短成功",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := makeSep(tc.last, tc.next)
			t.Logf("输入 last=%q next=%q | 输出 sep=%q | 判定依据: %s",
				hexKey(tc.last), hexKey(tc.next), hexKey(got), tc.reason)
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("want %q, got %q", hexKey(tc.want), hexKey(got))
			}
		})
	}
}

func TestNextPrefixOfLastRejected(t *testing.T) {
	// next 是 last 的前缀时 last > next，AddBlock 必须拒绝。
	var b Builder
	_, err := b.AddBlock([]byte("abcdef"), []byte("abc"))
	t.Logf("输入 last=abcdef next=abc | 输出 err=%v | 判定依据: next 是 last 前缀，last>next", err)
	if !errors.Is(err, ErrLastNotBeforeNext) {
		t.Fatalf("want ErrLastNotBeforeNext, got %v", err)
	}
	if seps := b.Seps(); len(seps) != 0 {
		t.Fatalf("被拒绝操作不得登记，seps=%v", seps)
	}
}

func TestFinishRules(t *testing.T) {
	t.Run("all 0xFF", func(t *testing.T) {
		last := []byte{0xFF, 0xFF}
		got := makeFinalSep(last)
		t.Logf("输入 last=\\xFF\\xFF | 输出 sep=%v | 判定依据: 全为 0xFF，sep=last", got)
		if !bytes.Equal(got, last) {
			t.Fatalf("want %v, got %v", last, got)
		}
	})
	t.Run("prefix 0xFF then increment", func(t *testing.T) {
		last := []byte{0xFF, 0x41, 0x99}
		got := makeFinalSep(last)
		want := []byte{0xFF, 0x42}
		t.Logf("输入 last=\\xFFA\\x99 | 输出 sep=%v | 判定依据: 首个非 0xFF 字节 0x41 加 1 并截断", got)
		if !bytes.Equal(got, want) {
			t.Fatalf("want %v, got %v", want, got)
		}
	})
}

func TestErrorPrecedenceAddBlock(t *testing.T) {
	// 空 last 与空 next：先报空键。
	var b1 Builder
	_, err := b1.AddBlock(nil, nil)
	if !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("empty both: want ErrEmptyKey, got %v", err)
	}

	// 非空但 last >= next：last-not-before-next 优先于乱序。
	var b2 Builder
	if _, err := b2.AddBlock([]byte("a"), []byte("m")); err != nil {
		t.Fatal(err)
	}
	_, err = b2.AddBlock([]byte("z"), []byte("a"))
	t.Logf("输入 last=z next=a（上一块 next=m）| 输出 err=%v | 判定依据: last>=next 先于乱序检查", err)
	if !errors.Is(err, ErrLastNotBeforeNext) {
		t.Fatalf("want ErrLastNotBeforeNext, got %v", err)
	}

	// last < 上一块 next：乱序。
	_, err = b2.AddBlock([]byte("b"), []byte("c"))
	if !errors.Is(err, ErrOutOfOrderBlock) {
		t.Fatalf("want ErrOutOfOrderBlock, got %v", err)
	}

	// Finish 之后再 AddBlock：即使参数本身非法，顺序仍是 空键 -> last>=next -> 乱序 -> 已Finish。
	if _, err := b2.Finish([]byte("z")); err != nil {
		t.Fatal(err)
	}
	if _, err := b2.AddBlock(nil, []byte("q")); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("after finish, empty key first: got %v", err)
	}
	if _, err := b2.AddBlock([]byte("z"), []byte("a")); !errors.Is(err, ErrLastNotBeforeNext) {
		t.Fatalf("after finish, ordering second: got %v", err)
	}
	if _, err := b2.AddBlock([]byte("a"), []byte("c")); !errors.Is(err, ErrOutOfOrderBlock) {
		t.Fatalf("after finish, ordering third: got %v", err)
	}
	if _, err := b2.AddBlock([]byte("p"), []byte("q")); !errors.Is(err, ErrAlreadyFinished) {
		t.Fatalf("after finish, valid args -> ErrAlreadyFinished: got %v", err)
	}
	if len(b2.Seps()) != 2 {
		t.Fatalf("被拒绝操作不得改变已登记内容, got %d seps", len(b2.Seps()))
	}
}

func TestErrorPrecedenceFinish(t *testing.T) {
	var b Builder
	// 空键优先。
	if _, err := b.Finish(nil); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	// 乱序优先于已 Finish。
	if _, err := b.AddBlock([]byte("m"), []byte("z")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish([]byte("a")); !errors.Is(err, ErrOutOfOrderBlock) {
		t.Fatalf("want ErrOutOfOrderBlock, got %v", err)
	}
	// 正常 Finish 后再 Finish：第二次给空键仍先报空键。
	if _, err := b.Finish([]byte("z")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Finish(nil); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("want ErrEmptyKey, got %v", err)
	}
	if _, err := b.Finish([]byte("a")); !errors.Is(err, ErrOutOfOrderBlock) {
		t.Fatalf("want ErrOutOfOrderBlock, got %v", err)
	}
	if _, err := b.Finish([]byte("z")); !errors.Is(err, ErrAlreadyFinished) {
		t.Fatalf("want ErrAlreadyFinished, got %v", err)
	}
}

func TestSeekBeforeFinish(t *testing.T) {
	var b Builder
	_, err := b.Seek([]byte("a"))
	if !errors.Is(err, ErrNotFinished) {
		t.Fatalf("want ErrNotFinished, got %v", err)
	}
}

func TestSeekBlockRanges(t *testing.T) {
	// 块 0: [a, abcdefg]，与 next=abzzz 之间分隔键 abd
	// 块 1: [abzzz, abzzz]，Finish 规则首个非 0xFF 字节 'a'->'b'，分隔键 b
	var b Builder
	sep0, err := b.AddBlock([]byte("abcdefg"), []byte("abzzz"))
	if err != nil {
		t.Fatal(err)
	}
	sep1, err := b.Finish([]byte("abzzz"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sep1, []byte("b")) {
		t.Fatalf("Finish sep = %q, want b", sep1)
	}
	t.Logf("seps = [%q, %q]", sep0, sep1)

	cases := []struct {
		key  string
		want int
	}{
		{"", 0}, // 空 key < 全部 sep，定位第一块
		{"a", 0},
		{"abc", 0},
		{"abcdefg", 0}, // 恰好落在本块最大键
		{"abd", 0},     // sep0 本身 -> 第一个 sep>=key 为块 0
		{"abda", 1},    // 刚过 sep0，但仍小于 sep1="b" -> 块 1
		{"abzzz", 1},
		{"ac", 1}, // 'a'<'b'，仍小于 sep1 -> 块 1
		{"aca", 1},
		{"zzz", SeekOutOfBound},
	}
	for _, tc := range cases {
		got, err := b.Seek([]byte(tc.key))
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("Seek(%q) = %d（期望 %d）| 判定依据: 第一个 sep>=key 的块下标", tc.key, got, tc.want)
		if got != tc.want {
			t.Fatalf("Seek(%q) = %d, want %d", tc.key, got, tc.want)
		}
	}

	seps := b.Seps()
	if !bytes.Equal(seps[0], []byte("abd")) || !bytes.Equal(seps[1], []byte("b")) {
		t.Fatalf("Seps = %q, %q", seps[0], seps[1])
	}

	// Seps 返回副本：外部修改不影响内部状态。
	seps[0][0] = 0x00
	if got := b.Seps()[0]; !bytes.Equal(got, []byte("abd")) {
		t.Fatalf("Seps must return copies, got %q", got)
	}
}

// TestInvariantAllKeysInBlocks 对随机生成的块序列，枚举/抽验所有落在块内的键，
// Seek 必须返回其真实所在块。
func TestInvariantAllKeysInBlocks(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	seen := map[string]bool{}
	for iter := 0; iter < 60; iter++ {
		blocks := genOrderedBlocks(rng, 2+rng.Intn(6))
		key := blocksKey(blocks)
		if seen[key] {
			continue
		}
		seen[key] = true

		b := buildFromBlocks(t, blocks)
		for i, blk := range blocks {
			keys := sampleKeysInRange(rng, blk.lo, blk.hi, 30)
			for _, k := range keys {
				got, err := b.Seek(k)
				if err != nil {
					t.Fatal(err)
				}
				if got != i {
					t.Fatalf("iter %d key=%q 在块 %d[%q,%q]，Seek=%d",
						iter, hexKey(k), i, hexKey(blk.lo), hexKey(blk.hi), got)
				}
			}
		}
		// 全 0xFF 键必然大于任何不含全 0xFF 的分隔键，应越界。
		if oob, _ := b.Seek([]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF}); oob != SeekOutOfBound {
			t.Fatalf("iter %d: 超出最后块应越界, got %d", iter, oob)
		}
		// 不变量：last <= sep；非最后块 sep < next。
		for i, sep := range b.Seps() {
			if bytes.Compare(blocks[i].hi, sep) > 0 {
				t.Fatalf("last > sep: %q > %q", blocks[i].hi, sep)
			}
			if i < len(blocks)-1 && bytes.Compare(sep, blocks[i+1].lo) >= 0 {
				t.Fatalf("sep >= next: %q >= %q", sep, blocks[i+1].lo)
			}
		}
	}
	t.Logf("共对拍 %d 组有序随机块序列的块内键与不变量", len(seen))
}
