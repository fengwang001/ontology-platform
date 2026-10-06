package federation

import (
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// testLog 让每个用例的输入、实际输出与判定依据同时打印到测试日志，
// 并镜像写入临时文件，测试结束时输出文件路径便于事后核查。
type testLog struct {
	w io.Writer
	f *os.File
}

func newTestLog(t *testing.T, name string) *testLog {
	path := filepath.Join(t.TempDir(), name+".log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	t.Logf("日志文件: %s", path)
	return &testLog{w: io.MultiWriter(os.Stdout, f), f: f}
}

func (l *testLog) printf(format string, args ...any) {
	fmt.Fprintf(l.w, format+"\n", args...)
}

func (l *testLog) close() { l.f.Close() }

// naiveInput 是朴素模型使用的简化配置（直接以普通整数给出）。
type naiveInput struct {
	name      string
	weight    int64
	min       int64
	max       int64 // -1 表示不设置最大副本数
	capacity  int64
	available bool
	current   int64
	dead      bool
}

// naiveAllocate 是独立于生产代码的逐副本朴素模型，仅适用于小规模 total。
//
// 它忠实、逐步地模拟规格描述的过程：
//  1. 每个可用集群先拿 min；min > 有效上限 => 配置冲突；min 之和 > total => 拒绝。
//  2. 每一轮在“未饱和且权重>0”的集群间做一次 Hamilton 分摊：
//     floor(pool*w/W) 为确定份额（受 headroom 截断），取整余量按小数部分
//     降序补一，小数并列时 current 多者优先，再并列名称升序。
//  3. floor 或补一过程中触顶的集群饱和退出；它们被截掉的份额与让出的余量
//     回到池中，进入下一轮；全部饱和仍有剩余 => 容量不足（报告缺口）。
//
// 与生产实现相比，这里不用 big.Int、不用整批公式，而是以最小粒度的有符号
// 整数运算逐轮、逐余量推进，代码路径完全独立，便于交叉印证。
func naiveAllocate(in []naiveInput, total int64) (map[string]int64, *AllocError) {
	targets := map[string]int64{}
	for _, c := range in {
		targets[c.name] = 0
	}

	var avail []naiveInput
	for _, c := range in {
		if c.available && !c.dead {
			avail = append(avail, c)
		}
	}

	effCap := func(c naiveInput) int64 {
		if c.max >= 0 && c.max < c.capacity {
			return c.max
		}
		return c.capacity
	}

	for _, c := range avail {
		if c.min > effCap(c) {
			return nil, &AllocError{Code: ErrConfigConflict,
				Msg: fmt.Sprintf("cluster %s min %d > cap %d", c.name, c.min, effCap(c))}
		}
	}

	sumMin := int64(0)
	for _, c := range avail {
		targets[c.name] = c.min
		sumMin += c.min
	}
	if sumMin > total {
		return nil, &AllocError{Code: ErrMinExceedsTotal,
			Msg: fmt.Sprintf("sumMin %d > total %d", sumMin, total)}
	}

	type nstate struct {
		c naiveInput
		t int64
	}
	var active []*nstate
	for _, c := range avail {
		if c.weight > 0 {
			active = append(active, &nstate{c: c, t: targets[c.name]})
		}
	}

	pool := total - sumMin
	for pool > 0 {
		live := make([]*nstate, 0, len(active))
		var wsum int64
		for _, s := range active {
			if s.t < effCap(s.c) {
				live = append(live, s)
				wsum += s.c.weight
			}
		}
		if len(live) == 0 {
			return nil, &AllocError{Code: ErrInsufficientCapacity,
				Msg: fmt.Sprintf("shortfall %d", pool)}
		}

		type nrow struct {
			s     *nstate
			num   int64 // pool*w mod W：小数分子（同分母 W，直接比较分子）
			floor int64
			sat   bool
		}
		rows := make([]*nrow, 0, len(live))
		var floorGiven int64
		for _, s := range live {
			raw := pool * s.c.weight
			f := raw / wsum
			num := raw % wsum
			head := effCap(s.c) - s.t
			r := &nrow{s: s, num: num, floor: f}
			if f >= head {
				r.sat = true
				s.t = effCap(s.c)
				floorGiven += head
			} else {
				s.t += f
				floorGiven += f
			}
			rows = append(rows, r)
		}
		left := pool - floorGiven // 取整余量：0 <= left < len(live)

		sort.SliceStable(rows, func(i, j int) bool {
			if rows[i].num != rows[j].num {
				return rows[i].num > rows[j].num
			}
			if rows[i].s.c.current != rows[j].s.c.current {
				return rows[i].s.c.current > rows[j].s.c.current
			}
			return rows[i].s.c.name < rows[j].s.c.name
		})

		// 逐余量补一；饱和者让出的余量在同一轮继续按序寻找下一个未饱和者，
		// 若所有候选都饱和仍未发完，则把剩余带入下一轮（由外层循环处理）。
		for left > 0 {
			moved := false
			for _, r := range rows {
				if left == 0 {
					break
				}
				if r.sat {
					continue
				}
				if r.s.t >= effCap(r.s.c) {
					r.sat = true
					continue
				}
				r.s.t++
				left--
				moved = true
				if r.s.t >= effCap(r.s.c) {
					r.sat = true
				}
			}
			if !moved {
				break // 本轮全部饱和，剩余 pool 在下一轮被判定为容量不足
			}
		}

		var given int64
		next := make([]*nstate, 0, len(live))
		for _, r := range rows {
			given += r.s.t - targets[r.s.c.name]
			targets[r.s.c.name] = r.s.t
			if !r.sat {
				next = append(next, r.s)
			}
		}
		pool -= given
		active = next
	}

	for _, s := range active {
		targets[s.c.name] = s.t
	}
	return targets, nil
}

func bigI(v int64) *big.Int { return big.NewInt(v) }

func bigMax(v int64) *big.Int {
	if v < 0 {
		return nil
	}
	return big.NewInt(v)
}
