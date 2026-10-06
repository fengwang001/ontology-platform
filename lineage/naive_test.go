package lineage

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// naiveModel 是独立的朴素对照模型: 不缓存、不跳链, 每次查询都沿提交图
// 逐行递归追溯, 并显式按名单语义处理忽略提交的穿透与标记。
type naiveModel struct {
	commits map[string]Commit
}

func newNaive() *naiveModel { return &naiveModel{commits: map[string]Commit{}} }

func (n *naiveModel) load(c Commit) { n.commits[c.ID] = c }

func (n *naiveModel) blame(id, path string, ignore map[string]bool) []Attribution {
	c := n.commits[id]
	lines := splitLines(c.Files[path])
	out := make([]Attribution, len(lines))
	for i := range lines {
		out[i] = n.trace(id, path, i, ignore)
	}
	return out
}

func (n *naiveModel) trace(id, path string, lineIdx int, ignore map[string]bool) Attribution {
	c := n.commits[id]
	lines := splitLines(c.Files[path])
	parentPath := path
	for _, r := range c.Renames {
		if r.New == path {
			parentPath = r.Old
			break
		}
	}
	for _, pid := range c.Parents {
		pc, ok := n.commits[pid].Files[parentPath]
		if !ok {
			continue
		}
		m := naiveAlign(splitLines(pc), lines)
		if m[lineIdx] >= 0 {
			return n.trace(pid, parentPath, m[lineIdx], ignore)
		}
	}
	return Attribution{CommitID: id, Path: path, Line: lineIdx + 1, IgnoredButAttributed: ignore[id]}
}

// naiveAlign 与 align 独立实现: 先求 LCS 长度, 再按 (prev, curr) 字典序
// 逐个贪心选取能构成最优配对的最早行对。
func naiveAlign(prev, curr []string) []int {
	m, n := len(prev), len(curr)
	res := make([]int, n)
	for j := range res {
		res[j] = -1
	}
	if m == 0 || n == 0 {
		return res
	}
	lcs := make([][]int, m+1)
	for i := range lcs {
		lcs[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if prev[i] == curr[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	i0, j0 := 0, 0
	for i0 < m && j0 < n && lcs[i0][j0] > 0 {
		found := false
		for i := i0; i < m && !found; i++ {
			for j := j0; j < n && !found; j++ {
				if prev[i] == curr[j] && 1+lcs[i+1][j+1] == lcs[i0][j0] {
					res[j] = i
					i0, j0 = i+1, j+1
					found = true
				}
			}
		}
		if !found {
			break
		}
	}
	return res
}

// TestAlignTieBreak 用随机小序列对拍 align 与独立实现的 naiveAlign,
// 验证「最多配对 + 字典序最小」的语义在两种实现下完全一致。
func TestAlignTieBreak(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	words := []string{"a", "b", "c"}
	for iter := 0; iter < 200000; iter++ {
		prev := make([]string, r.Intn(6))
		curr := make([]string, r.Intn(6))
		for i := range prev {
			prev[i] = words[r.Intn(len(words))]
		}
		for j := range curr {
			curr[j] = words[r.Intn(len(words))]
		}
		got := align(prev, curr)
		want := naiveAlign(prev, curr)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("prev=%v curr=%v align=%v naive=%v", prev, curr, got, want)
		}
		if iter == 0 {
			t.Logf("输入: prev=%v curr=%v", prev, curr)
			t.Logf("实际输出: align=%v naiveAlign=%v", got, want)
			t.Logf("判定依据: 两种独立实现对 200000 组随机序列结果逐一相同")
		}
	}
}

// randRepo 生成随机提交图: 随机父(0-2 个)、随机文件增删改、随机合法改名。
func randRepo(r *rand.Rand, n int) []Commit {
	paths := []string{"a", "b", "c", "d"}
	words := []string{"alpha", "beta", "gamma", "delta"}
	byID := map[string]Commit{}
	var commits []Commit
	uniq := 0
	randContent := func() string {
		nl := r.Intn(6)
		if nl == 0 {
			return ""
		}
		lines := make([]string, nl)
		for i := range lines {
			if r.Intn(4) == 0 {
				uniq++
				lines[i] = fmt.Sprintf("u%d", uniq)
			} else {
				lines[i] = words[r.Intn(len(words))]
			}
		}
		return strings.Join(lines, "\n") + "\n"
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("c%d", i)
		var parents []string
		if i > 0 {
			np := 1
			if i >= 2 && r.Intn(3) == 0 {
				np = 2
			}
			used := map[int]bool{}
			for len(parents) < np {
				k := r.Intn(i)
				if !used[k] {
					used[k] = true
					parents = append(parents, commits[k].ID)
				}
			}
		}
		files := map[string]string{}
		if len(parents) > 0 {
			for k, v := range byID[parents[0]].Files {
				files[k] = v
			}
			if len(parents) == 2 {
				for k, v := range byID[parents[1]].Files {
					if _, ok := files[k]; !ok && r.Intn(2) == 0 {
						files[k] = v
					}
				}
			}
		}
		for _, p := range paths {
			if _, ok := files[p]; ok && r.Intn(6) == 0 {
				delete(files, p)
			}
		}
		for p := range files {
			if r.Intn(2) == 0 {
				files[p] = randContent()
			}
		}
		for _, p := range paths {
			if _, ok := files[p]; !ok && r.Intn(4) == 0 {
				files[p] = randContent()
			}
		}
		var renames []Rename
		if len(parents) > 0 {
			first := byID[parents[0]]
			for _, p := range paths {
				if len(renames) >= 2 || r.Intn(3) != 0 {
					continue
				}
				oldContent, ok := first.Files[p]
				if !ok {
					continue
				}
				if _, stillThere := files[p]; !stillThere {
					continue
				}
				newPath := fmt.Sprintf("r%d_%s", i, p)
				delete(files, p)
				if r.Intn(2) == 0 {
					files[newPath] = randContent()
				} else {
					files[newPath] = oldContent
				}
				renames = append(renames, Rename{Old: p, New: newPath})
			}
		}
		c := Commit{ID: id, Parents: parents, Files: files, Renames: renames}
		commits = append(commits, c)
		byID[id] = c
	}
	return commits
}

func TestRandomizedAgainstNaive(t *testing.T) {
	t.Log("判定依据: 服务实现(缓存+跳链) 与独立朴素模型(全递归) 逐行一致")
	for _, seed := range []int64{1, 2, 3, 7, 42, 99} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			r := rand.New(rand.NewSource(seed))
			commits := randRepo(r, 60)
			s := NewService()
			n := newNaive()
			var versions []map[string]bool
			for _, c := range commits {
				if err := s.LoadCommit(c); err != nil {
					t.Fatalf("load %s: %v", c.ID, err)
				}
				n.load(c)
				if r.Intn(3) == 0 {
					var ids []string
					for _, pc := range commits[:len(n.commits)] {
						if r.Intn(4) == 0 {
							ids = append(ids, pc.ID)
						}
					}
					v, err := s.NewIgnoreVersion(ids)
					if err != nil {
						t.Fatalf("version: %v", err)
					}
					set := map[string]bool{}
					for _, x := range ids {
						set[x] = true
					}
					_ = v
					versions = append(versions, set)
				}
			}
			if len(versions) == 0 {
				v, err := s.NewIgnoreVersion(nil)
				if err != nil {
					t.Fatal(err)
				}
				_ = v
				versions = append(versions, map[string]bool{})
			}
			queries := 0
			for q := 0; q < 400; q++ {
				c := commits[r.Intn(len(commits))]
				var ps []string
				for p := range c.Files {
					ps = append(ps, p)
				}
				if len(ps) == 0 {
					continue
				}
				path := ps[r.Intn(len(ps))]
				vi := r.Intn(len(versions))
				got, err := s.Blame(c.ID, path, vi)
				if err != nil {
					t.Fatalf("blame(%s,%s,v%d): %v", c.ID, path, vi, err)
				}
				want := n.blame(c.ID, path, versions[vi])
				queries++
				if msg := check(got, want...); msg != "" {
					t.Logf("输入: 提交=%s 路径=%s 名单版本=v%d%v", c.ID, path, vi, versions[vi])
					t.Logf("实际输出:\n%s", attrsStr(got))
					t.Logf("朴素模型输出:\n%s", attrsStr(want))
					t.Fatalf("不一致: %s", msg)
				}
				if q < 3 {
					t.Logf("输入: 提交=%s 路径=%s 名单版本=v%d", c.ID, path, vi)
					t.Logf("实际输出:\n%s", attrsStr(got))
					t.Logf("判定依据: 与独立朴素模型逐行一致")
				}
			}
			t.Logf("seed=%d: %d 个提交, %d 个名单版本, %d 次随机查询全部与朴素模型逐行一致",
				seed, len(commits), len(versions), queries)
		})
	}
}
