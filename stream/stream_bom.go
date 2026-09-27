package stream

import "ontology/scalar"

var (
	bom8  = []byte{0xEF, 0xBB, 0xBF}
	bomLE = []byte{0xFF, 0xFE}
)

// expectedBOM 返回当前输入编码流首应出现的 BOM 字节（BE 时为 FEFF）。
func (t *Transcoder) expectedBOM() []byte {
	if t.cfg.From == U8 {
		return bom8
	}
	if t.cfg.From == U16BE {
		return []byte{0xFE, 0xFF}
	}
	return bomLE
}

// tryBOM 在流首试探 BOM；返回是否应停止及错误。
func (t *Transcoder) tryBOM(b byte) (bool, error) {
	t.bom = append(t.bom, b)
	want := t.expectedBOM()
	if !prefixOf(t.bom, want) {
		// 不是 BOM：已收集字节作为普通数据重放
		head := append([]byte(nil), t.bom...)
		t.bom = t.bom[:0]
		t.bomOK = true
		if err := t.replay(head); err != nil {
			return true, err
		}
		return false, nil
	}
	if len(t.bom) == len(want) {
		t.st.BOMBytes += int64(len(want))
		t.cons += int64(len(want))
		t.st.Consumed = t.cons
		if t.cfg.EmitBOM {
			t.out = append(t.out, t.enc(scalar.BOM)...)
		}
		t.bom = t.bom[:0]
		t.bomOK = true
	}
	return false, nil
}

// replay 把 BOM 误探的字节从解码器起点重新喂入。
func (t *Transcoder) replay(head []byte) error {
	for _, b := range head {
		if len(t.pend) == 0 {
			t.pend0 = t.cons
		}
		k, r, used := t.step(b)
		if k == evNone {
			t.pend = append(t.pend, b)
			if len(t.pend) > t.st.MaxPending {
				t.st.MaxPending = len(t.pend)
			}
			continue
		}
		if used {
			t.pend = append(t.pend, b)
		}
		err := t.finish(k, r)
		if err != nil {
			return err
		}
	}
	return nil
}

func prefixOf(a, b []byte) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
