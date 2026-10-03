package budget

import "math/big"

// sizeLimit 是允许的最大检查范围 Dmax+H。
const sizeLimit int64 = 1_000_000

// Task 是组件内的一个 EDF 任务。
type Task struct {
	ID string
	C  int64
	T  int64
	D  int64
}

// Component 是一个周期资源容器的快照视图。
type Component struct {
	Name  string
	Pi    int64
	Theta int64
	Tasks []Task
}

// Fraction 是既约的非负有理数，分母恒为正；零表示 0/1。
type Fraction struct {
	Num int64
	Den int64
}

func (f Fraction) String() string {
	return itoa(f.Num) + "/" + itoa(f.Den)
}

func itoa(x int64) string {
	if x == 0 {
		return "0"
	}
	neg := x < 0
	var b [24]byte
	i := len(b)
	for u := x; u != 0; u /= 10 {
		i--
		d := byte('0')
		if neg {
			d += byte(-(u % 10))
		} else {
			d += byte(u % 10)
		}
		b[i] = d
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// sbf 返回周期 Pi、预算 Theta 的最坏供给界函数在 t 处的值。
//
// 令 a = Pi-Theta，q = floor(max(0,t-a)/Pi)，则
//
//	sbf(t) = q*Theta + max(0, t - 2a - q*Pi)
//
// Theta = Pi（全供给）时 sbf(t) = t。
func sbf(pi, theta, t int64) int64 {
	if t <= 0 {
		return 0
	}
	if theta == pi {
		return t
	}
	a := pi - theta
	m := t - a
	if m < 0 {
		m = 0
	}
	q := m / pi
	rem := t - 2*a - q*pi
	if rem < 0 {
		rem = 0
	}
	return q*theta + rem
}

// dbfOne 返回单个任务在 t 处的 EDF 需求界：
// max(0, floor((t-D)/T)+1) * C，t < D 时贡献 0。
func dbfOne(task Task, t int64) int64 {
	if t < task.D {
		return 0
	}
	return ((t-task.D)/task.T + 1) * task.C
}

// dbf 返回任务集在 t 处的总需求界。
func dbf(tasks []Task, t int64) int64 {
	var sum int64
	for _, task := range tasks {
		sum += dbfOne(task, t)
	}
	return sum
}

// lcmChecked 返回 vals 的最小公倍数；一旦中间结果超过 limit
// 即返回 (0, false)，防止溢出并支持“超过阈值即可停止”。
func lcmChecked(vals []int64, limit int64) (int64, bool) {
	gcd := func(a, b int64) int64 {
		for b != 0 {
			a, b = b, a%b
		}
		return a
	}
	l := int64(1)
	for _, v := range vals {
		g := gcd(l, v)
		// l/g * v，先做除法再做乘法；并检查是否超过 limit。
		step := l / g
		if v > limit/step {
			return 0, false
		}
		l = step * v
		if l > limit {
			return 0, false
		}
	}
	return l, true
}

// checkHorizon 计算可行性检查范围 Dmax 与 Dmax+H。
// H = lcm(Pi, 各任务周期)；H（从而 Dmax+H）超过 sizeLimit 时返回 ok=false。
func checkHorizon(pi int64, tasks []Task) (dmax, horizon int64, ok bool) {
	vals := make([]int64, 0, len(tasks)+1)
	vals = append(vals, pi)
	for _, task := range tasks {
		if task.D > dmax {
			dmax = task.D
		}
		vals = append(vals, task.T)
	}
	h, fine := lcmChecked(vals, sizeLimit)
	if !fine {
		return dmax, 0, false
	}
	// Dmax+H 可能超过 sizeLimit；用 big.Int 兜底以防加法溢出概念问题。
	sum := new(big.Int).Add(big.NewInt(dmax), big.NewInt(h))
	if sum.Cmp(big.NewInt(sizeLimit)) > 0 {
		return dmax, 0, false
	}
	return dmax, dmax + h, true
}

// jumpPoints 返回 dbf 在 (0, horizon] 内的全部严格递增跳变点，
// 即各任务的 D + mT（m >= 0）。不逐个整数 t 扫描，采用多路归并。
// 返回点数同时供测试断言：不超过 sum(ceil(horizon/T_i))。
func jumpPoints(tasks []Task, horizon int64) []int64 {
	type cursor struct {
		t  int64
		id int
	}
	next := func(id int, t int64) (int64, bool) {
		task := tasks[id]
		nt := t + task.T
		if nt > horizon || nt < t {
			return 0, false
		}
		return nt, true
	}
	points := make([]int64, 0)
	cursors := make([]cursor, 0, len(tasks))
	for i, task := range tasks {
		if task.D <= horizon {
			cursors = append(cursors, cursor{t: task.D, id: i})
		}
	}
	for len(cursors) > 0 {
		best := 0
		for i := 1; i < len(cursors); i++ {
			if cursors[i].t < cursors[best].t {
				best = i
			}
		}
		cur := cursors[best]
		if len(points) == 0 || points[len(points)-1] != cur.t {
			points = append(points, cur.t)
		}
		nt, has := next(cur.id, cur.t)
		if has {
			cursors[best].t = nt
		} else {
			cursors = append(cursors[:best], cursors[best+1:]...)
		}
	}
	return points
}

// jumpPointBound 返回跳变点数的上界 sum(ceil(horizon/T_i))。
func jumpPointBound(tasks []Task, horizon int64) int {
	n := 0
	for _, task := range tasks {
		if horizon >= task.D {
			n += int((horizon-task.D)/task.T + 1)
		}
	}
	return n
}
