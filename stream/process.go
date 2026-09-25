package stream

import "ontology/scalar"

type unit struct {
	r          rune
	size       int
	bad, trunc bool
}

func (t *Transcoder) feedByte(b byte) (unit, bool) {
	if t.in == k8in {
		x, adv := t.d8.Feed(b)
		return unit{x.R, x.Size, x.Bad, x.Trunc}, adv
	}
	x, adv := t.d16.Feed(b)
	return unit{x.R, x.Size, x.Bad, x.Trunc}, adv
}

// Write 喂入一段输入。n 是已完整裁决的字节数（顶层调用即消费数）；
// 缓存中未裁决字节不计入 n。
func (t *Transcoder) Write(p []byte) (int, error) {
	if t.terminal != nil {
		return 0, t.terminal
	}
	baseAbs := t.abs
	for i := 0; i < len(p); {
		if t.ceiling >= 0 && t.pendingCount() == 0 && t.abs >= t.ceiling {
			break
		}
		// 流首 BOM：bomStep 直接消费/重放，abs 与 n 同步推进。
		if t.bomNeed > 0 {
			_, matched := t.bomStep(p, &i)
			if t.terminal != nil {
				return t.consumedIn(baseAbs), t.terminal
			}
			if matched {
				continue
			}
		}
		t.stats.Checks++
		start := t.abs
		u, advance := t.feedByte(p[i])
		if !advance {
			continue
		}
		i++
		if u.size == 0 {
			continue
		}
		t.commitUnit(u, start)
		if t.terminal != nil {
			return t.consumedIn(baseAbs), t.terminal
		}
	}
	return t.consumedIn(baseAbs), t.terminal
}

// consumedIn 返回自 baseAbs 起真正被完整裁决的字节数。
// 半截 BOM（bomGot）与半个序列（pending）都不算已消费。
func (t *Transcoder) consumedIn(baseAbs int64) int {
	n := t.abs - baseAbs
	n -= int64(t.pendingCount())
	if t.bomNeed > 0 {
		n -= int64(len(t.bomGot))
	}
	return int(n)
}

// SetRange 供 par 使用：设定输入起点全局偏移与 owned 区间 [floor, ceiling)。
// ceiling<0 表示直到 Close（含截断裁决）。
func (t *Transcoder) SetRange(start, floor, ceiling int64) {
	t.abs, t.floor, t.ceiling = start, floor, ceiling
}

func (t *Transcoder) pendingCount() int {
	if t.in == k8in {
		return t.d8.Pending()
	}
	return t.d16.Pending()
}

func (t *Transcoder) commitUnit(u unit, start int64) {
	owned := start >= t.floor
	t.abs += int64(u.size)
	if !owned {
		return
	}
	t.stats.Consumed += int64(u.size)
	if u.bad {
		t.stats.BadUnits++
		t.stats.BadBytes += int64(u.size)
		if t.cfg.Strict {
			if u.trunc {
				t.terminal = &TruncatedError{Offset: start, Size: int64(u.size)}
			} else {
				t.terminal = &IllegalError{Offset: start, Size: int64(u.size)}
			}
			return
		}
		if !t.emit(scalar.Replacement) {
			t.terminal = ErrLimit
		}
		return
	}
	t.stats.Scalars++
	if !t.emit(u.r) {
		t.terminal = ErrLimit
	}
}

// Close 裁决残留：替换输出一个 U+FFFD，严格返回 TruncatedError。
func (t *Transcoder) Close() error {
	if t.terminal != nil {
		return t.terminal
	}
	t.closed = true
	if t.bomNeed > 0 && len(t.bomGot) > 0 {
		got := append([]byte(nil), t.bomGot...)
		t.bomNeed, t.bomGot = 0, t.bomGot[:0]
		if _, err := t.Write(got); err != nil {
			return err
		}
	}
	var u unit
	if t.in == k8in {
		x, _ := t.d8.Close()
		u = unit{x.R, x.Size, x.Bad, x.Trunc}
	} else {
		x, _ := t.d16.Close()
		u = unit{x.R, x.Size, x.Bad, x.Trunc}
	}
	if u.size > 0 {
		t.commitUnit(u, t.abs)
	}
	return t.terminal
}
