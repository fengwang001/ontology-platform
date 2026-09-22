package stats

// EquiSelectivity 估计等值连接 lt.lc = rt.rc 的选择率。
// 规则：任一列缺失统计 → 回退 DefaultSelectivity 并标注；两侧直方图均有效时
// 用桶重叠比例精化，否则回退 1/max(NDV)。直方图损坏返回 ErrStatCorrupt。
func (e *Estimator) EquiSelectivity(lt, lc, rt, rc string) (float64, []Note, error) {
	lTab, lcCol, ln, err := e.lookup(lt, lc)
	if err != nil {
		return 0, nil, err
	}
	rTab, rcCol, rn, err := e.lookup(rt, rc)
	if err != nil {
		return 0, nil, err
	}
	notes := append(ln, rn...)
	if lcCol == nil || rcCol == nil {
		return DefaultSelectivity, notes, nil
	}
	sel := ndvSelectivity(lcCol.NDV, rcCol.NDV)
	if lcCol.Hist != nil && rcCol.Hist != nil && len(lcCol.Hist.Buckets) > 0 && len(rcCol.Hist.Buckets) > 0 {
		if h := histOverlapSelectivity(lcCol, rcCol, lTab.Rows, rTab.Rows); h > sel {
			sel = h
		}
	}
	return sel, notes, nil
}

// ndvSelectivity 是经典的 1/max(NDV) 等值连接选择率。
func ndvSelectivity(a, b int64) float64 {
	m := a
	if b > m {
		m = b
	}
	if m <= 0 {
		return DefaultSelectivity
	}
	return 1.0 / float64(m)
}

// histOverlapSelectivity 按等宽桶的重叠比例估计等值连接选择率：
// sel = Σ_{i,j} (count_i/rowsA)*(count_j/rowsB) * |bucket_i ∩ bucket_j| / width_j。
func histOverlapSelectivity(a, b *Column, rowsA, rowsB int64) float64 {
	if rowsA <= 0 || rowsB <= 0 {
		return 0
	}
	var total float64
	for i := range a.Hist.Buckets {
		ba := &a.Hist.Buckets[i]
		for j := range b.Hist.Buckets {
			bb := &b.Hist.Buckets[j]
			lo := ba.Lo
			if bb.Lo > lo {
				lo = bb.Lo
			}
			hi := ba.Hi
			if bb.Hi < hi {
				hi = bb.Hi
			}
			if hi <= lo {
				continue
			}
			overlap := (hi - lo) / bb.width()
			total += float64(ba.Count) * float64(bb.Count) * overlap
		}
	}
	return total / (float64(rowsA) * float64(rowsB))
}
