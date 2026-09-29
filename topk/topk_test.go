package topk

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// format 打印当前有序序列，便于日志核对。
func format(entries []Entry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = fmt.Sprintf("#%d %s=%.1f", i+1, e.ID, e.Score)
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// naiveTop 是朴素全量排序参考实现：分数降序、并列按标识升序。
func naiveTop(scores map[string]float64, n int) []Entry {
	all := make([]Entry, 0, len(scores))
	for id, s := range scores {
		all = append(all, Entry{ID: id, Score: s})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].ID < all[j].ID
	})
	if n > len(all) {
		n = len(all)
	}
	return all[:n]
}

// logStep 打印操作、当前有序序列与判定依据。
func logStep(t *testing.T, op string, tk *TopK, basis string) {
	t.Helper()
	t.Logf("操作: %s | 当前有序序列: %s | 判定依据: %s", op, format(tk.TopK()), basis)
}

func mustNew(t *testing.T, k, capacity int) *TopK {
	t.Helper()
	tk, err := New(k, capacity)
	if err != nil {
		t.Fatalf("New(%d, %d) 意外失败: %v", k, capacity, err)
	}
	return tk
}

func mustUpsert(t *testing.T, tk *TopK, id string, score float64) {
	t.Helper()
	if err := tk.Upsert(id, score); err != nil {
		t.Fatalf("Upsert(%q, %v) 意外失败: %v", id, score, err)
	}
}

func TestTieBreakByID(t *testing.T) {
	tk := mustNew(t, 3, 5)
	// 三个同分元素，名次应由标识字典序决定。
	mustUpsert(t, tk, "charlie", 90)
	mustUpsert(t, tk, "alpha", 90)
	mustUpsert(t, tk, "bravo", 90)
	mustUpsert(t, tk, "delta", 80)
	logStep(t, "Upsert charlie/alpha/bravo=90, delta=80", tk,
		"同分按标识字典序升序: alpha < bravo < charlie")

	want := []Entry{{"alpha", 90}, {"bravo", 90}, {"charlie", 90}}
	if got := tk.TopK(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TopK() = %v, want %v", got, want)
	}
	// 新同分元素挤入，字典序最大者被挤出门槛。
	mustUpsert(t, tk, "aaron", 90)
	logStep(t, "Upsert aaron=90", tk, "aaron 字典序最小，charlie 被挤出前 3")
	want = []Entry{{"aaron", 90}, {"alpha", 90}, {"bravo", 90}}
	if got := tk.TopK(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TopK() = %v, want %v", got, want)
	}
}

func TestRemoveBackfill(t *testing.T) {
	tk := mustNew(t, 2, 4)
	mustUpsert(t, tk, "a", 100)
	mustUpsert(t, tk, "b", 90)
	mustUpsert(t, tk, "c", 80)
	mustUpsert(t, tk, "d", 70)
	logStep(t, "填满容量 a=100 b=90 c=80 d=70", tk, "门槛 K=2，c/d 在门槛外候补")

	if err := tk.Remove("a"); err != nil {
		t.Fatalf("Remove(a) 失败: %v", err)
	}
	logStep(t, "Remove a", tk, "门槛内元素被撤回，门槛外最高分 c=80 必须补位")
	want := []Entry{{"b", 90}, {"c", 80}}
	if got := tk.TopK(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TopK() = %v, want %v", got, want)
	}

	// 幂等：重复删除同一元素是空操作，状态不变。
	if err := tk.Remove("a"); err != nil {
		t.Fatalf("重复 Remove(a) 应为幂等空操作: %v", err)
	}
	if err := tk.Remove("ghost"); err != nil {
		t.Fatalf("Remove 不存在元素应为幂等空操作: %v", err)
	}
	logStep(t, "Remove a(已删) 与 ghost(不存在)", tk, "幂等空操作，序列不变")
	if got := tk.TopK(); !reflect.DeepEqual(got, want) {
		t.Fatalf("幂等删除后 TopK() = %v, want %v", got, want)
	}
	if tk.Count() != 3 {
		t.Fatalf("Count() = %d, want 3", tk.Count())
	}
}

func TestUpsertOverwrite(t *testing.T) {
	tk := mustNew(t, 3, 3)
	mustUpsert(t, tk, "a", 70)
	mustUpsert(t, tk, "b", 80)
	mustUpsert(t, tk, "c", 90)
	logStep(t, "a=70 b=80 c=90", tk, "初始名次 c>b>a")

	// 覆盖更新：a 从 70 提到 95，应跃居第二。
	mustUpsert(t, tk, "a", 95)
	logStep(t, "Upsert a=95 (覆盖)", tk, "覆盖式更新重排名次: c=90 被 a=95 超越")
	want := []Entry{{"a", 95}, {"c", 90}, {"b", 80}}
	if got := tk.TopK(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TopK() = %v, want %v", got, want)
	}
	if tk.Count() != 3 {
		t.Fatalf("覆盖更新不应增加计数, Count() = %d, want 3", tk.Count())
	}

	// 覆盖更新把元素挤出门槛：c 降到 60，跌出前 3 之外无候补，仍在容量内。
	mustUpsert(t, tk, "c", 60)
	logStep(t, "Upsert c=60 (覆盖降分)", tk, "c 降至榜尾，b 升至第二")
	want = []Entry{{"a", 95}, {"b", 80}, {"c", 60}}
	if got := tk.TopK(); !reflect.DeepEqual(got, want) {
		t.Fatalf("TopK() = %v, want %v", got, want)
	}
}

func TestCapacityAndParamValidation(t *testing.T) {
	// K 非正。
	if _, err := New(0, 5); !errors.Is(err, ErrNonPositiveK) {
		t.Fatalf("New(0,5) err = %v, want ErrNonPositiveK", err)
	}
	if _, err := New(-1, 5); !errors.Is(err, ErrNonPositiveK) {
		t.Fatalf("New(-1,5) err = %v, want ErrNonPositiveK", err)
	}
	// 容量小于 K。
	if _, err := New(3, 2); !errors.Is(err, ErrCapacityTooSmall) {
		t.Fatalf("New(3,2) err = %v, want ErrCapacityTooSmall", err)
	}
	// 三种原因可区分。
	if errors.Is(ErrNonPositiveK, ErrCapacityTooSmall) || errors.Is(ErrAtCapacity, ErrEmptyID) {
		t.Fatal("失败原因之间必须可区分")
	}

	tk := mustNew(t, 2, 2)
	mustUpsert(t, tk, "a", 90)
	mustUpsert(t, tk, "b", 80)
	before := tk.TopK()

	// 新增第 3 个元素，已达容量上限，整体拒绝。
	err := tk.Upsert("c", 100)
	if !errors.Is(err, ErrAtCapacity) {
		t.Fatalf("Upsert 超容量 err = %v, want ErrAtCapacity", err)
	}
	logStep(t, "Upsert c=100 (超容量被拒)", tk, "失败不得改变任何状态")
	if got := tk.TopK(); !reflect.DeepEqual(got, before) {
		t.Fatalf("失败后状态改变: %v, want %v", got, before)
	}
	if tk.Count() != 2 {
		t.Fatalf("失败后 Count() = %d, want 2", tk.Count())
	}

	// 满容量时覆盖更新已有元素仍合法。
	mustUpsert(t, tk, "a", 95)
	logStep(t, "Upsert a=95 (满容量覆盖)", tk, "覆盖已有元素不受容量上限限制")
	if tk.Count() != 2 {
		t.Fatalf("覆盖后 Count() = %d, want 2", tk.Count())
	}

	// 空标识：新增与删除均拒绝且状态不变。
	if err := tk.Upsert("", 1); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("Upsert 空标识 err = %v, want ErrEmptyID", err)
	}
	if err := tk.Remove(""); !errors.Is(err, ErrEmptyID) {
		t.Fatalf("Remove 空标识 err = %v, want ErrEmptyID", err)
	}
	// Top 数量越界。
	if _, err := tk.Top(0); !errors.Is(err, ErrNOutOfRange) {
		t.Fatalf("Top(0) err = %v, want ErrNOutOfRange", err)
	}
	if _, err := tk.Top(3); !errors.Is(err, ErrNOutOfRange) {
		t.Fatalf("Top(3) err = %v, want ErrNOutOfRange", err)
	}
}

func TestPrefixProperty(t *testing.T) {
	tk := mustNew(t, 5, 8)
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 7; i++ {
		mustUpsert(t, tk, fmt.Sprintf("id-%d", i), float64(rng.Intn(100)))
	}
	full := tk.TopK()
	logStep(t, "随机写入 7 个元素", tk, "任意更小 K 必须是完整序列前缀")
	for n := 1; n <= tk.K(); n++ {
		got, err := tk.Top(n)
		if err != nil {
			t.Fatalf("Top(%d) 失败: %v", n, err)
		}
		wantN := n
		if wantN > len(full) {
			wantN = len(full)
		}
		if !reflect.DeepEqual(got, full[:wantN]) {
			t.Fatalf("Top(%d) = %v, 不是完整序列前缀 %v", n, got, full[:wantN])
		}
	}
	// 不足 K 个时返回全部：删除到只剩 2 个（含门槛外候补）。
	keep := map[string]bool{full[0].ID: true, full[1].ID: true}
	for i := 0; i < 7; i++ {
		id := fmt.Sprintf("id-%d", i)
		if keep[id] {
			continue
		}
		if err := tk.Remove(id); err != nil {
			t.Fatalf("Remove(%q) 失败: %v", id, err)
		}
	}
	logStep(t, "删除到只剩 2 个", tk, "不足 K 个时 TopK 返回全部")
	if got := tk.TopK(); len(got) != 2 {
		t.Fatalf("TopK() 长度 = %d, want 2", len(got))
	}
}

func TestConcurrentReads(t *testing.T) {
	tk := mustNew(t, 10, 20)
	for i := 0; i < 20; i++ {
		mustUpsert(t, tk, fmt.Sprintf("id-%02d", i), float64((i*37)%100))
	}
	want := tk.TopK()
	wantCount := tk.Count()
	logStep(t, "写入 20 个元素后并发读", tk, "并发读取结果必须逐元素相同")

	const readers = 8
	const rounds = 200
	var wg sync.WaitGroup
	errs := make(chan error, readers)
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(r int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if got := tk.TopK(); !reflect.DeepEqual(got, want) {
					errs <- fmt.Errorf("reader %d: TopK() = %v, want %v", r, got, want)
					return
				}
				n := 1 + (i % tk.K())
				got, err := tk.Top(n)
				if err != nil {
					errs <- fmt.Errorf("reader %d: Top(%d) err = %v", r, n, err)
					return
				}
				if !reflect.DeepEqual(got, want[:n]) {
					errs <- fmt.Errorf("reader %d: Top(%d) 非前缀", r, n)
					return
				}
				if c := tk.Count(); c != wantCount {
					errs <- fmt.Errorf("reader %d: Count() = %d, want %d", r, c, wantCount)
					return
				}
			}
		}(r)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// TestNaiveCrossCheck 用朴素全量排序对随机操作序列做交叉核对。
func TestNaiveCrossCheck(t *testing.T) {
	const k, capacity = 4, 10
	tk := mustNew(t, k, capacity)
	model := map[string]float64{}
	rng := rand.New(rand.NewSource(7))

	for step := 0; step < 500; step++ {
		id := fmt.Sprintf("e%d", rng.Intn(14)) // 故意让 id 数超过容量，触发满容量拒绝
		switch rng.Intn(3) {
		case 0, 1: // upsert
			score := float64(rng.Intn(5)) * 10 // 少量分值，制造大量并列
			err := tk.Upsert(id, score)
			_, exists := model[id]
			switch {
			case !exists && len(model) >= capacity:
				if !errors.Is(err, ErrAtCapacity) {
					t.Fatalf("step %d: 满容量新增应拒绝, err = %v", step, err)
				}
			default:
				if err != nil {
					t.Fatalf("step %d: Upsert(%q,%v) 失败: %v", step, id, score, err)
				}
				model[id] = score
			}
		case 2: // remove
			if err := tk.Remove(id); err != nil {
				t.Fatalf("step %d: Remove(%q) 失败: %v", step, id, err)
			}
			delete(model, id)
		}

		want := naiveTop(model, k)
		got := tk.TopK()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("step %d: TopK() = %v, 朴素全量排序参考 = %v", step, got, want)
		}
		if tk.Count() != len(model) {
			t.Fatalf("step %d: Count() = %d, want %d", step, tk.Count(), len(model))
		}
	}
	logStep(t, "500 步随机操作后与朴素全量排序逐步核对", tk,
		"每步 TopK 与 naive sort(分数降序, 标识升序) 完全一致")
}
