package scheduler

import (
	"bytes"
	"strings"
	"testing"
)

func TestLogsContainInputsDepthRoundAndBasis(t *testing.T) {
	var logs bytes.Buffer
	s := New(&logs)
	err := s.Add([]Transaction{
		tx(1, []string{"audited-read"}, map[string]string{"a": "1"}),
		tx(2, []string{"audited-read"}, map[string]string{"a": "2", "b": "2"}),
		tx(3, nil, map[string]string{"c": "3"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Schedule(2); err != nil {
		t.Fatal(err)
	}

	out := logs.String()
	for _, want := range []string{
		`msg="accepted transaction"`,
		"seq=1", "seq=2", "seq=3",
		`reads=[audited-read]`,
		"writes=[a]",
		"depth=1", "depth=2",
		`depth_basis=[1]`,
		`msg="scheduled transaction"`,
		"round=1", "round=2",
		"max_parallel=2",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q in:\n%s", want, out)
		}
	}
}
