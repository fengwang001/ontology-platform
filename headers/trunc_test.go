package headers_test

import (
	"errors"
	"testing"

	"ontology/headers"
)

// 遍历所有截断点：每个前缀都必须返回可判定错误，不 panic、不返回半截集合。
func TestAllTruncationPoints(t *testing.T) {
	samples := []string{
		"X-One: 1\r\nX-Two: longer value\r\n folded\r\nX-Three: 3\r\n\r\n",
		"A: b\r\n\r\n",
		"X: \r\n\r\n",
	}
	for _, sample := range samples {
		full, err := headers.Parse([]byte(sample), nil)
		if err != nil {
			t.Fatalf("完整输入应解析成功: %v", err)
		}
		if full == nil {
			t.Fatal("完整输入返回空集合")
		}
		for i := 0; i < len(sample); i++ {
			s, err := headers.Parse([]byte(sample[:i]), nil)
			if err == nil {
				t.Errorf("截断点 %d/%d 未报错", i, len(sample))
				continue
			}
			if s != nil {
				t.Errorf("截断点 %d 返回了半截集合", i)
			}
			var pe *headers.ParseError
			if !errors.As(err, &pe) {
				t.Errorf("截断点 %d 的错误不可判定为 ParseError: %v", i, err)
			}
		}
	}
}

// 抽查特定截断点的阶段判定。
func TestTruncationStages(t *testing.T) {
	sample := "X-One: 1\r\nX-Two: 2\r\n\r\n"
	cases := []struct {
		name  string
		cut   int
		stage headers.Stage
	}{
		{"空输入", 0, headers.StageEnd},
		{"名字读到一半", 3, headers.StageName},
		{"值读到一半", 8, headers.StageValue},
		{"CR 后截断", len("X-One: 1\r"), headers.StageValue},
		{"第二条名字读到一半", len("X-One: 1\r\n") + 2, headers.StageName},
		{"缺终止空行", len(sample) - 2, headers.StageEnd},
	}
	for _, c := range cases {
		_, err := headers.Parse([]byte(sample[:c.cut]), nil)
		var pe *headers.ParseError
		if !errors.As(err, &pe) {
			t.Fatalf("%s: 不是 ParseError: %v", c.name, err)
		}
		if pe.Stage != c.stage {
			t.Errorf("%s: stage = %v, want %v", c.name, pe.Stage, c.stage)
		}
	}
}

// 续行被截断应判定为折行阶段。
func TestTruncatedContinuation(t *testing.T) {
	_, err := headers.Parse([]byte("X-A: 1\r\n fold"), nil)
	var pe *headers.ParseError
	if !errors.As(err, &pe) || pe.Stage != headers.StageFold {
		t.Errorf("续行截断应为 StageFold, got %v", err)
	}
}
