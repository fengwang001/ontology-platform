package engine

import "ontology/model"

// Complete 完成 Task t 的一次活动；之后按节点编号升序反复扫描汇合直到无变化。
func (in *Instance) Complete(t int) error {
	in.mu.Lock()
	defer in.mu.Unlock()

	if t < 1 || t > in.n || in.kind[t] != model.Task || in.act[t] <= 0 {
		return ErrNoActive
	}
	in.act[t]--
	in.deliverEdge(t, in.eout[t][0], 1)
	in.settle()
	return nil
}

// settle 按编号升序反复检查所有汇合，直到一整轮没有任何汇合触发。
func (in *Instance) settle() {
	for {
		fired := false
		for v := 1; v <= in.n; v++ {
			switch in.kind[v] {
			case model.AndJoin:
				if in.andJoinReady(v) {
					for i := range in.arr[v] {
						in.arr[v][i]--
					}
					in.fires[v]++
					in.deliverEdge(v, in.eout[v][0], 1)
					fired = true
				}
			case model.OrJoin:
				if in.orJoinReady(v) {
					for i := range in.arr[v] {
						in.arr[v][i] = 0
					}
					in.fires[v]++
					in.deliverEdge(v, in.eout[v][0], 1)
					fired = true
				}
			}
		}
		if !fired {
			return
		}
	}
}

func (in *Instance) andJoinReady(v int) bool {
	for _, a := range in.arr[v] {
		if a < 1 {
			return false
		}
	}
	return true
}

// orJoinReady：本汇合已有令牌到达，且有效图上不存在"其它令牌所在节点"可达本汇合。
// "其它令牌"指 act>0 的 Task、或 arr 总和>0 的其他汇合。判定只遍历当前有令牌的节点，
// 考察节点数不超过当前有令牌的节点数，与图的节点总数无关。
func (in *Instance) orJoinReady(v int) bool {
	total := 0
	for _, a := range in.arr[v] {
		total += a
	}
	if total == 0 {
		return false
	}
	mask := in.canReach // 局部别名，下面按"有令牌节点"逐个探测
	bit := uint64(1) << uint(v)
	for p := 1; p <= in.n; p++ {
		if p == v {
			continue
		}
		has := false
		if in.kind[p] == model.Task && in.act[p] > 0 {
			has = true
		} else if (in.kind[p] == model.AndJoin || in.kind[p] == model.OrJoin) && in.arrSum(p) > 0 {
			has = true
		}
		if has {
			in.orProbe++
			if mask[p]&bit != 0 {
				return false
			}
		}
	}
	return true
}

func (in *Instance) arrSum(v int) int {
	s := 0
	for _, a := range in.arr[v] {
		s += a
	}
	return s
}

func (in *Instance) classify() Status {
	active := false
	for v := 1; v <= in.n; v++ {
		if in.kind[v] == model.Task && in.act[v] > 0 {
			active = true
		}
		if (in.kind[v] == model.AndJoin || in.kind[v] == model.OrJoin) && in.arrSum(v) > 0 {
			if !active {
				return Stuck
			}
		}
	}
	if active {
		return Running
	}
	return Completed
}
