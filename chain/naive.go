package chain

// naive.go: 严格按题目文字写成的逐步朴素模拟器。
// 展开若干个超周期内的全部作业，按时间顺序逐个处理“释放读取/写出”事件，
// 把外部采样标记沿链传播，再对区间内每个整数 x 取值。不做任何代数化简。

import "sort"

// event 为一个作业事件。read=true 表示释放并读取；否则为写出。
type event struct {
	time int
	task int
	read bool
	job  int
}

// jobInfo 记录一个已展开作业。
type jobInfo struct {
	release int
	write   int
	tag     int // 该作业读到的采样标记（τ1 为自身释放时刻）
	hasTag  bool
}

// expandEvents 展开时间到 horizon 为止的全部作业，并按时间排序。
// 同刻排序：先写出、后释放读取，以实现“同一时刻写出对同一时刻释放可见”。
func expandEvents(periods, phases, writeDelays []int, horizon int) []event {
	var events []event
	for k := 0; k < len(periods); k++ {
		for j := 0; ; j++ {
			r := phases[k] + j*periods[k]
			if r > horizon {
				break
			}
			events = append(events,
				event{time: r, task: k, read: true, job: j},
				event{time: r + writeDelays[k], task: k, read: false, job: j},
			)
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].time != events[j].time {
			return events[i].time < events[j].time
		}
		ri, rj := 0, 0
		if events[i].read {
			ri = 1
		}
		if events[j].read {
			rj = 1
		}
		if ri != rj {
			return ri < rj // 写出(0) 在释放读取(1) 之前
		}
		return events[i].task < events[j].task
	})
	return events
}

// propagate 逐步处理事件，维护每个任务“最近写出的采样”以及每个作业读到的标记。
// jobs[k][j] 为 τk 第 j 个作业的信息。
func propagate(periods, phases, writeDelays []int, horizon int) [][]jobInfo {
	n := len(periods)
	jobs := make([][]jobInfo, n)
	counts := make([]int, n)
	for k := range periods {
		cnt := 0
		// 作业表必须覆盖到“释放 + 写延迟”不早于 horizon，否则末尾传播链会越界。
		for r := phases[k]; r+writeDelays[k] <= horizon+periods[k]; r += periods[k] {
			cnt++
		}
		counts[k] = cnt
		jobs[k] = make([]jobInfo, cnt)
		for j := range jobs[k] {
			r := phases[k] + j*periods[k]
			jobs[k][j] = jobInfo{release: r, write: r + writeDelays[k], tag: -1}
		}
	}

	events := expandEvents(periods, phases, writeDelays, horizon)
	latestTag := make([]int, n)
	haveLatest := make([]bool, n)

	for _, e := range events {
		if e.read {
			j := &jobs[e.task][e.job]
			if e.task == 0 {
				// τ1 释放即读取外部采样，标记取本次释放时刻
				j.tag = j.release
				j.hasTag = true
			} else if haveLatest[e.task] {
				j.tag = latestTag[e.task]
				j.hasTag = true
			}
		} else {
			j := &jobs[e.task][e.job]
			if j.hasTag {
				latestTag[e.task] = j.tag
				haveLatest[e.task] = true
			}
		}
	}
	return jobs
}

// tagToExit 用事件传播结果，得到“τ1 释放时刻为 tag 的采样”
// 在 τn 的写出时刻；通过沿作业链逐跳查找首个释放 >= 上游写出的作业得到。
func tagToExit(jobs [][]jobInfo, tag int) int {
	n := len(jobs)
	idx := sort.Search(len(jobs[0]), func(i int) bool { return jobs[0][i].release >= tag })
	writeTime := jobs[0][idx].write
	for k := 1; k < n; k++ {
		js := jobs[k]
		next := sort.Search(len(js), func(i int) bool { return js[i].release >= writeTime })
		if next >= len(js) {
			panic("naive simulator: jobs not expanded far enough downstream")
		}
		writeTime = js[next].write
	}
	return writeTime
}

// reactionInterval 对 [start,end) 内每个整数 x，用传播后的作业标记给出 Reaction(x)。
func reactionInterval(jobs [][]jobInfo, start, end int) []int {
	out := make([]int, end-start)
	js0 := jobs[0]
	for x := start; x < end; x++ {
		idx := sort.Search(len(js0), func(i int) bool { return js0[i].release >= x })
		exit := tagToExit(jobs, js0[idx].release)
		out[x-start] = exit - x
	}
	return out
}

// ageInterval 对 [start,end) 内每个整数 x，从 τn 反向逐作业回溯给出 Age(x)。
func ageInterval(jobs [][]jobInfo, periods, phases, writeDelays []int, start, end int) []int {
	n := len(jobs)
	out := make([]int, end-start)
	for x := start; x < end; x++ {
		jsn := jobs[n-1]
		idx := sort.Search(len(jsn), func(i int) bool { return jobs[n-1][i].write > x }) - 1
		if idx < 0 {
			panic("naive simulator: tau_n jobs not expanded far enough back")
		}
		nextRelease := jsn[idx].release
		for k := n - 2; k >= 0; k-- {
			js := jobs[k]
			j := sort.Search(len(js), func(i int) bool { return jobs[k][i].write > nextRelease }) - 1
			if j < 0 {
				panic("naive simulator: upstream jobs not expanded far enough back")
			}
			nextRelease = js[j].release
		}
		out[x-start] = x - nextRelease
	}
	return out
}

// naiveAnalysis 展开若干个超周期的全部作业，按时间顺序传播采样，再逐 x 汇总。
func naiveAnalysis(periods, phases, writeDelays []int) Analysis {
	h := hyperperiod(periods)
	phi := 0
	sumPeriods := 0
	for k, p := range periods {
		if phases[k] > phi {
			phi = phases[k]
		}
		sumPeriods += p
	}
	start := phi
	end := phi + h
	// 额外展开两个超周期与一个最大周期，保证最末 x 的传播链完整落在作业表内。
	horizon := end + 2*h
	for _, p := range periods {
		horizon += p
	}
	maxP := 0
	for _, p := range periods {
		if p > maxP {
			maxP = p
		}
	}
	horizon += maxP
	jobs := propagate(periods, phases, writeDelays, horizon)

	rx := reactionInterval(jobs, start, end)
	maxR, minR := rx[0], rx[0]
	for _, v := range rx[1:] {
		if v > maxR {
			maxR = v
		}
		if v < minR {
			minR = v
		}
	}
	w0 := phi + 2*sumPeriods
	ag := ageInterval(jobs, periods, phases, writeDelays, w0, w0+h)
	maxAge := ag[0]
	for _, v := range ag[1:] {
		if v > maxAge {
			maxAge = v
		}
	}
	return Analysis{MaxReaction: maxR, MinReaction: minR, MaxAge: maxAge}
}
