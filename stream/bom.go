package stream

import "ontology/scalar"

// bomStep 处理流首 BOM。返回的 consumed 是顶层可计入的已裁决字节数：
// 匹配中途返回 0（字节被缓存，不算已消费）；完整匹配返回 BOM 长度；
// 失配时回退调用方读指针并返回 (0,false)，由主循环关闭 BOM 识别后自然重放。
func (t *Transcoder) bomStep(p []byte, ip *int) (int, bool) {
	b := p[*ip]
	want := t.bomWant()
	pos := len(t.bomGot)
	t.stats.Checks++
	if b != want[pos] {
		*ip -= len(t.bomGot) // 回到 BOM 前缀第一个字节
		t.bomNeed, t.bomGot = 0, t.bomGot[:0]
		return 0, false
	}
	t.bomGot = append(t.bomGot, b)
	*ip++
	if pos+1 < len(want) {
		return 0, true
	}
	// 完整 BOM。
	t.bomNeed = 0
	t.abs += int64(len(want))
	t.stats.Consumed += int64(len(want))
	t.stats.BOMBytes += int64(len(want))
	if t.cfg.KeepBOM {
		t.stats.Scalars++
		if !t.emit(scalar.BOM) {
			t.terminal = ErrLimit
		}
	}
	return len(want), true
}

func (t *Transcoder) bomWant() []byte {
	if t.in == k16in {
		if t.cfg.Order == 0 {
			return []byte{0xFF, 0xFE}
		}
		return []byte{0xFE, 0xFF}
	}
	return []byte{0xEF, 0xBB, 0xBF}
}
