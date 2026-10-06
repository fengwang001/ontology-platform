package lineage

import (
	"fmt"
	"testing"
	"time"
)

// buildChain 构造 depth 个提交的链, 每个提交在文件 f 追加一行。
func buildChain(t *testing.T, s *Service, prefix string, depth int, baseContent string, parent string) (head string, content string) {
	t.Helper()
	content = baseContent
	head = parent
	for i := 0; i < depth; i++ {
		content += fmt.Sprintf("%s-line%d\n", prefix, i)
		id := fmt.Sprintf("%s-%d", prefix, i)
		c := Commit{ID: id, Files: map[string]string{"f": content}}
		if head != "" {
			c.Parents = []string{head}
		}
		mustLoad(t, s, c)
		head = id
	}
	return head, content
}

// 重复查询的开销不随版本库总提交数或历史深度增长:
// 以内部计数器证明重复查询只做一次缓存命中, 不做任何对齐/归属计算。
func TestRepeatQueryConstantCost(t *testing.T) {
	s := NewService()
	head, _ := buildChain(t, s, "c", 300, "", "")
	v := mustVersion(t, s)

	first := mustBlame(t, s, head, "f", v)
	warm := s.Stats()
	t.Logf("输入: 深度 300 的链, 首次查询 (%s, f, v%d)", head, v)
	t.Logf("实际输出: 首次查询后统计=%+v", warm)

	// 继续载入 500 个提交并创建新名单版本。
	head2, _ := buildChain(t, s, "d", 500, "", head)
	_ = head2
	v2 := mustVersion(t, s, "c-0", "c-1")
	_ = v2

	start := time.Now()
	second := mustBlame(t, s, head, "f", v)
	elapsed := time.Since(start)
	after := s.Stats()
	t.Logf("输入: 追加 500 提交与新版本后, 重复查询 (%s, f, v%d)", head, v)
	t.Logf("实际输出: 统计=%+v, 耗时=%v", after, elapsed)
	t.Logf("判定依据: 重复查询命中缓存, 对齐与完整归属计算次数零增长, 与总提交数/历史深度无关")
	if after.AlignComputations != warm.AlignComputations || after.FullBlameComputations != warm.FullBlameComputations {
		t.Fatalf("重复查询触发了新计算: %+v -> %+v", warm, after)
	}
	if after.QueryCacheHits != warm.QueryCacheHits+1 {
		t.Fatalf("重复查询未命中缓存: %+v -> %+v", warm, after)
	}
	if fmt.Sprint(first) != fmt.Sprint(second) {
		t.Fatal("重复查询结果与首次不一致")
	}
	t.Logf("判定依据: 两次查询结果逐字节相同(%d 行)", len(first))
}

// 首次查询的开销不随与该路径无关的提交数增长:
// 无关提交(未触碰该路径)只产生 O(1) 跳链, 计数器严格相等。
func TestFirstQueryIndependentOfUnrelatedCommits(t *testing.T) {
	build := func(extra int) (Stats, string, time.Duration) {
		s := NewService()
		head, fContent := buildChain(t, s, "c", 40, "", "")
		// extra 个无关提交: 只改其它文件, f 内容原样携带。
		content := ""
		parent := head
		for i := 0; i < extra; i++ {
			content += fmt.Sprintf("noise%d\n", i)
			id := fmt.Sprintf("n-%d", i)
			mustLoad(t, s, Commit{
				ID:      id,
				Parents: []string{parent},
				Files:   map[string]string{"f": fContent, "g": content},
			})
			parent = id
		}
		v := mustVersion(t, s)
		start := time.Now()
		res := mustBlame(t, s, parent, "f", v)
		return s.Stats(), fmt.Sprint(res), time.Since(start)
	}
	st0, res0, d0 := build(0)
	st1, res1, d1 := build(2000)
	t.Logf("输入: 40 个相关提交 + 分别 0 / 2000 个无关提交(只改其它文件), 首次查询 (head, f)")
	t.Logf("实际输出: 无无关提交 统计=%+v 耗时=%v; 2000 个无关提交 统计=%+v 耗时=%v", st0, d0, st1, d1)
	t.Logf("判定依据: 对齐与完整归属计算次数不随无关提交数增长(严格相等), 结果逐字节相同")
	if st0.AlignComputations != st1.AlignComputations || st0.FullBlameComputations != st1.FullBlameComputations {
		t.Fatalf("首次查询开销随无关提交数增长: %+v vs %+v", st0, st1)
	}
	if res0 != res1 {
		t.Fatal("两种规模下查询结果不一致")
	}
}

// 结果稳定性: 查询之后无论载入多少新提交、创建多少新名单版本,
// 同一 (提交, 路径, 名单版本) 的结果逐字节相同。
func TestResultStability(t *testing.T) {
	s := NewService()
	head, _ := buildChain(t, s, "c", 50, "", "")
	v := mustVersion(t, s, "c-10")
	before := fmt.Sprint(mustBlame(t, s, head, "f", v))

	head2, _ := buildChain(t, s, "d", 500, "", head)
	_ = head2
	for i := 0; i < 20; i++ {
		mustVersion(t, s, fmt.Sprintf("c-%d", i))
	}
	after := fmt.Sprint(mustBlame(t, s, head, "f", v))
	t.Logf("输入: 查询 (c-49, f, v0) 后追加 500 提交与 20 个新名单版本, 再次查询")
	t.Logf("实际输出: 两次结果均为 %d 字节", len(before))
	t.Logf("判定依据: 结果逐字节相同")
	if before != after {
		t.Fatal("结果不稳定")
	}
}
