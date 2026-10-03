package chain

// timing.go: 纯计算（周期、超周期、首释放、末写出、反应时间、数据年龄）。

// gcd 返回两个正整数的最大公约数。
func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// lcm 返回两个正整数的最小公倍数。
func lcm(a, b int) int {
	return a / gcd(a, b) * b
}

// hyperperiod 返回链上周期的最小公倍数 H。
func hyperperiod(periods []int) int {
	h := 1
	for _, p := range periods {
		h = lcm(h, p)
	}
	return h
}

// firstReleaseAtOrAfter 返回释放时刻不小于 t 的第一个作业的释放时刻 r。
// 作业 j 的释放时刻为 phase + j*period（j >= 0）。
func firstReleaseAtOrAfter(t, period, phase int) int {
	if t <= phase {
		return phase
	}
	j := (t - phase + period - 1) / period
	return phase + j*period
}

// lastWriteAtOrBefore 返回写出时刻 r+w 不大于 x 的最后一个作业的写出时刻；
// 不存在这样的作业时返回 -1（及“无效”的释放时刻）。
func lastWriteAtOrBefore(x, period, phase, writeDelay int) (write, release int) {
	if x < phase+writeDelay {
		return -1, -1
	}
	j := (x - phase - writeDelay) / period
	release = phase + j*period
	return release + writeDelay, release
}

// reactionAt 按规范逐级传播：τ1 取释放 >= x 的作业，其后每级取释放 >= 上一级写出时刻的作业。
// 同一时刻写出对同一时刻释放可见，因此使用 >=。
func reactionAt(x int, periods, phases, writeDelays []int) int {
	r := firstReleaseAtOrAfter(x, periods[0], phases[0])
	t := r + writeDelays[0]
	for k := 1; k < len(periods); k++ {
		r = firstReleaseAtOrAfter(t, periods[k], phases[k])
		t = r + writeDelays[k]
	}
	return t - x
}

// ageAt 从 τn 反向回溯：取写出 <= x 的最后一个作业，再依次取写出 <= 下一级释放时刻的最后作业。
func ageAt(x int, periods, phases, writeDelays []int) int {
	n := len(periods)
	_, nextRelease := lastWriteAtOrBefore(x, periods[n-1], phases[n-1], writeDelays[n-1])
	for k := n - 2; k >= 0; k-- {
		_, nextRelease = lastWriteAtOrBefore(nextRelease, periods[k], phases[k], writeDelays[k])
	}
	return x - nextRelease
}

// analyzeChain 在 [phi, phi+H) 的每个整数 x 上计算 MaxReaction / MinReaction，
// 并在 [W, W+H) 上计算 MaxAge，W = phi + 2*sum(T)。
func analyzeChain(periods, phases, writeDelays []int) Analysis {
	h := hyperperiod(periods)
	phi := 0
	sumPeriods := 0
	for k, p := range periods {
		if phases[k] > phi {
			phi = phases[k]
		}
		sumPeriods += p
	}
	maxR := -1
	minR := 0
	for x := phi; x < phi+h; x++ {
		r := reactionAt(x, periods, phases, writeDelays)
		if r > maxR {
			maxR = r
		}
		if minR == 0 || r < minR {
			minR = r
		}
	}
	maxAge := 0
	w0 := phi + 2*sumPeriods
	for x := w0; x < w0+h; x++ {
		g := ageAt(x, periods, phases, writeDelays)
		if g > maxAge {
			maxAge = g
		}
	}
	return Analysis{MaxReaction: maxR, MinReaction: minR, MaxAge: maxAge}
}
