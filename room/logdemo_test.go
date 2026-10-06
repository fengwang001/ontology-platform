package room

import "testing"

// TestLoggedDifferentialSample 固定种子生成 3 组序列，-v 时完整打印
// “输入、输出（fast vs 朴素）、判定依据”，供人工检视与复现。
// 大规模 1600 组对照见 TestRandomDifferential。
func TestLoggedDifferentialSample(t *testing.T) {
	if !testing.Verbose() {
		t.Skip("仅在 -v 下打印样例日志")
	}
	cases := genCases(randSrc(424242), 3)
	for i, tc := range cases {
		d := newDual(t, tc.cfg)
		for _, o := range tc.ops[:min(40, len(tc.ops))] {
			d.run(o, "随机差分")
		}
		t.Logf("===== 样例组 %d 结束，phase 比对一致 =====", i)
		d.log()
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
