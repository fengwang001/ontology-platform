package mec

import (
	"testing"

	"ontology/circ"
)

// TestInsertChecksConstant 证明落在圆内的插入每步只做 1 次判定、O(1)。
func TestInsertChecksConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		mec := New()
		mec.Insert(circ.Point{X: 0, Y: 0})
		mec.Insert(circ.Point{X: 1000, Y: 0}) // 直径圆：圆心 (500,0)，r²=250000
		for i := 0; i < m; i++ {
			// 严格落在圆内的互异点：x∈[400,600]，y∈[0,49]
			p := circ.Point{X: 400 + i%201, Y: i / 201}
			mec.Insert(p)
			if mec.checks != 1 {
				t.Fatalf("m=%d 第 %d 次插入判定次数=%d，应为 1", m, i, mec.checks)
			}
		}
		c, ok := mec.Circle()
		if !ok || c.R2.Cmp(circ.FromDiameter(circ.Point{X: 0, Y: 0}, circ.Point{X: 1000, Y: 0}).R2) != 0 {
			t.Fatalf("m=%d 圆不应被内部点改变", m)
		}
	}
}
