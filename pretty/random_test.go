package pretty

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
)

// 本文件把被测引擎与独立朴素模型在大量随机文档上做对照，并在日志中
// 打印每次输入、输出与逐组判定依据（go test -v 可见）。随机用例数可用
// 环境变量 PRETTY_RANDOM_CASES 调整。

// randomCases 返回随机对照用例数。
func randomCases() int {
	n := 1000
	if v := os.Getenv("PRETTY_RANDOM_CASES"); v != "" {
		fmt.Sscan(v, &n)
	}
	return n
}

// textPool 是随机文本池：含单双宽字符、首尾空格与空串。
var textPool = []string{
	"a", "bb", "ccc", "dddd", "hello", "x", "yz",
	"界", "文字", "界a", "αβ", "a b", "x ", " y", "  ", "",
	"foo_bar", "12345", "()", "longertext",
}

// genDoc 生成随机文档树。frags 为可引用的已登记片段名。
func genDoc(rng *rand.Rand, depth int, frags []string) Doc {
	if depth <= 0 {
		return genLeaf(rng, frags)
	}
	switch rng.Intn(14) {
	case 0, 1, 2, 3:
		return genLeaf(rng, frags)
	case 4:
		return Group(genDoc(rng, depth-1, frags))
	case 5:
		return Indent(rng.Intn(4), genDoc(rng, depth-1, frags))
	case 6:
		return Align(genDoc(rng, depth-1, frags))
	case 7, 8, 9:
		kids := make([]Doc, 0, 4)
		for i, n := 0, 1+rng.Intn(4); i < n; i++ {
			kids = append(kids, genDoc(rng, depth-1, frags))
		}
		return Seq(kids...)
	case 10:
		return CondText(textPool[rng.Intn(len(textPool))], textPool[rng.Intn(len(textPool))])
	case 11:
		if len(frags) > 0 {
			return Ref(frags[rng.Intn(len(frags))])
		}
		return genLeaf(rng, frags)
	default:
		return genLeaf(rng, frags)
	}
}

// genLeaf 生成随机叶子节点。
func genLeaf(rng *rand.Rand, frags []string) Doc {
	switch rng.Intn(12) {
	case 0, 1:
		return Space()
	case 2:
		return Blank()
	case 3:
		return HardLine()
	case 4:
		if len(frags) > 0 {
			return Ref(frags[rng.Intn(len(frags))])
		}
		return Text(textPool[rng.Intn(len(textPool))])
	default:
		return Text(textPool[rng.Intn(len(textPool))])
	}
}

// TestRandomDifferential 随机文档对照：被测引擎与朴素模型的输出必须
// 完全一致。
func TestRandomDifferential(t *testing.T) {
	cases := randomCases()
	for seed := int64(0); seed < int64(cases); seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := NewSession()
		var fragNames []string
		for i, n := 0, rng.Intn(4); i < n; i++ {
			name := fmt.Sprintf("f%d", i)
			if err := s.Register(name, genDoc(rng, 3, fragNames)); err != nil {
				t.Fatalf("种子=%d 登记片段: %v", seed, err)
			}
			fragNames = append(fragNames, name)
		}
		doc := genDoc(rng, 5, fragNames)
		width := 1 + rng.Intn(60)

		var logBuf strings.Builder
		want := naiveRender(doc, width, s.snapshot(), &logBuf)
		got, err := s.Render(doc, width)
		if err != nil {
			t.Fatalf("种子=%d 渲染出错: %v\n文档=%s", seed, err, sprintDoc(doc))
		}
		t.Logf("种子=%d 行宽=%d\n文档=%s\n判定依据:\n%s输出=%q\n超宽行=%v",
			seed, width, sprintDoc(doc), logBuf.String(), got.Text, got.Overlong)

		if got.Text != want.Text || !reflect.DeepEqual(got.Overlong, want.Overlong) {
			t.Fatalf("种子=%d 行宽=%d 不一致\n文档=%s\n被测: %q %+v\n朴素: %q %+v\n判定依据:\n%s",
				seed, width, sprintDoc(doc), got.Text, got.Overlong, want.Text, want.Overlong, logBuf.String())
		}
		checkInvariants(t, got, width)
	}
}

// checkInvariants 校验渲染结果的结构性不变量。
func checkInvariants(t *testing.T, res Result, width int) {
	t.Helper()
	lines := strings.Split(res.Text, "\n")
	var wantOver []OverlongLine
	for i, ln := range lines {
		if strings.HasSuffix(ln, " ") {
			t.Fatalf("第 %d 行有行尾空格: %q", i+1, ln)
		}
		if w := Width(ln); w > width {
			wantOver = append(wantOver, OverlongLine{Line: i + 1, Width: w})
		}
	}
	if !reflect.DeepEqual(res.Overlong, wantOver) {
		t.Fatalf("超宽行清单 = %+v，按文本重算 = %+v", res.Overlong, wantOver)
	}
}

// TestDeepChainDifferential 深层嵌套（组/缩进/对齐混合）与朴素模型
// 对照：验证级联断开优化不改变语义，且深度不会导致栈溢出。
func TestDeepChainDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for _, depth := range []int{100, 300, 400} {
		leaf := Group(Seq(Text("deep"), Space(), Text("界"), Blank(), Text("end")))
		d := leaf
		for i := 0; i < depth; i++ {
			switch rng.Intn(3) {
			case 0:
				d = Group(d)
			case 1:
				d = Indent(rng.Intn(2), Group(d))
			case 2:
				d = Align(Group(d))
			}
		}
		width := 1 + rng.Intn(20)
		var logBuf strings.Builder
		want := naiveRender(d, width, nil, &logBuf)
		got, err := Render(d, width)
		if err != nil {
			t.Fatalf("depth=%d: %v", depth, err)
		}
		t.Logf("深度=%d 行宽=%d 输出=%q", depth, width, got.Text)
		if got.Text != want.Text || !reflect.DeepEqual(got.Overlong, want.Overlong) {
			t.Fatalf("depth=%d width=%d 不一致\n被测: %q %+v\n朴素: %q %+v",
				depth, width, got.Text, got.Overlong, want.Text, want.Overlong)
		}
	}
}

// TestLargeDocument 大规模文档：渲染开销随规模近线性（此处验证大文档
// 能在测试时限内完成且结果自洽）。
func TestLargeDocument(t *testing.T) {
	const n = 200_000
	kids := make([]Doc, 0, n)
	for i := 0; i < n; i++ {
		kids = append(kids, Group(Seq(Text("ab"), Space(), Text("cd"))))
		if i%3 == 0 {
			kids = append(kids, Space())
		}
	}
	doc := Seq(kids...)
	res, err := Render(doc, 40)
	if err != nil {
		t.Fatal(err)
	}
	checkInvariants(t, res, 40)
	t.Logf("输出 %d 字节，%d 个超宽行", len(res.Text), len(res.Overlong))
}

// TestDeepBreakCascade 深层组链在放不下时全部断开，不栈溢出、结果正确。
func TestDeepBreakCascade(t *testing.T) {
	d := Doc(Text("工具"))
	for i := 0; i < 333; i++ {
		d = Group(Indent(1, Seq(Blank(), d)))
	}
	res, err := Render(d, 3)
	if err != nil {
		t.Fatal(err)
	}
	// 全部断开：每层一个可断空串换行且缩进 1 列，末行为 333 列缩进
	// 加宽 4 的 "工具"（超宽）。
	lines := strings.Split(res.Text, "\n")
	if len(lines) != 334 {
		t.Fatalf("行数 = %d，期望 334", len(lines))
	}
	if last := lines[len(lines)-1]; last != strings.Repeat(" ", 333)+"工具" {
		t.Fatalf("末行 = %q", last)
	}
	checkInvariants(t, res, 3)
}
