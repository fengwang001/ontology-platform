package activity

const maxNow = 1_000_000_000_000_000

func validNow(now int64) bool { return now >= 0 && now <= maxNow }

func validK(k int) bool { return k >= 1 && k <= 20 }

// Clock 返回全局时钟。
func (e *Executor) Clock() int64 { return e.clock }

// probeOf 返回某活动最近一次写操作推演时考察的堆项数（非导出，测试用）。
func (e *Executor) probeOf(id []byte) (int, bool) {
	a, ok := e.items[string(id)]
	return a.probe, ok
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
