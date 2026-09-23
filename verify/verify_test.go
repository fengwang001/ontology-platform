package verify

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"ontology/acc"
	"ontology/hashpart"
	"ontology/row"
)

func TestInMemory(t *testing.T) {
	cases := []struct {
		name string
		rows []row.Row
		want map[string]acc.State
	}{
		{"空", nil, map[string]acc.State{}},
		{"基本", []row.Row{{Key: "a", Val: 1}, {Key: "a", Val: 2}, {Key: "b", Val: 5}},
			map[string]acc.State{"a": {Count: 2, Sum: 3, Min: 1, Max: 2}, "b": {Count: 1, Sum: 5, Min: 5, Max: 5}}},
		{"跳过NaN", []row.Row{{Key: "a", Val: math.NaN()}}, map[string]acc.State{}},
		{"负零归一化", []row.Row{{Key: "a", Val: math.Copysign(0, -1)}},
			map[string]acc.State{"a": {Count: 1, Sum: 0, Min: 0, Max: 0}}},
	}
	for _, c := range cases {
		got := InMemory(c.rows)
		if len(got) != len(c.want) {
			t.Fatalf("%s: 组数 %d != %d", c.name, len(got), len(c.want))
		}
		for k, w := range c.want {
			g := got[k]
			if g.Count != w.Count || math.Float64bits(g.Sum) != math.Float64bits(w.Sum) ||
				math.Float64bits(g.Min) != math.Float64bits(w.Min) || math.Float64bits(g.Max) != math.Float64bits(w.Max) {
				t.Errorf("%s: 键%q 得到 %+v 期望 %+v", c.name, k, g, w)
			}
		}
	}
}

func TestCompareGroups(t *testing.T) {
	ref := map[string]acc.State{"a": {Count: 2, Sum: 3, Min: 1, Max: 2}}
	cases := []struct {
		name    string
		got     []acc.Group
		wantErr bool
	}{
		{"一致", []acc.Group{{Key: "a", State: acc.State{Count: 2, Sum: 3, Min: 1, Max: 2}}}, false},
		{"组数不同", nil, true},
		{"键不同", []acc.Group{{Key: "b", State: acc.State{Count: 2, Sum: 3, Min: 1, Max: 2}}}, true},
		{"Sum位模式不同", []acc.Group{{Key: "a", State: acc.State{Count: 2, Sum: 3.5, Min: 1, Max: 2}}}, true},
		{"Count不同", []acc.Group{{Key: "a", State: acc.State{Count: 3, Sum: 3, Min: 1, Max: 2}}}, true},
	}
	for _, c := range cases {
		err := CompareGroups(c.got, ref)
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v", c.name, err)
		}
	}
}

func TestIdentical(t *testing.T) {
	a := []acc.Group{{Key: "a", State: acc.State{Count: 1, Sum: 1, Min: 1, Max: 1}}}
	cases := []struct {
		name string
		b    []acc.Group
		want bool
	}{
		{"相同", []acc.Group{{Key: "a", State: acc.State{Count: 1, Sum: 1, Min: 1, Max: 1}}}, true},
		{"顺序不同即不同", []acc.Group{{Key: "b", State: acc.State{}}, {Key: "a", State: acc.State{}}}, false},
		{"位模式不同", []acc.Group{{Key: "a", State: acc.State{Count: 1, Sum: 2, Min: 1, Max: 1}}}, false},
	}
	for _, c := range cases {
		if got := Identical(a, c.b); got != c.want {
			t.Errorf("%s: got=%v", c.name, got)
		}
	}
}

func TestCheckFile(t *testing.T) {
	dir := t.TempDir()
	sp, err := hashpart.NewSpiller(dir, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.WriteSegment(0, [][]byte{[]byte("x"), []byte("y")}); err != nil {
		t.Fatal(err)
	}
	path := sp.Segments(0)[0]
	if err := CheckFile(path); err != nil {
		t.Fatalf("完好文件误报: %v", err)
	}
	data, _ := os.ReadFile(path)
	cases := []struct {
		name string
		data []byte
		want error
	}{
		{"头部截断", data[:10], hashpart.ErrHeader},
		{"长度前缀截断", data[:hashpart.HeaderLen+2], hashpart.ErrLengthPrefix},
		{"行体截断", data[:hashpart.HeaderLen+4], hashpart.ErrBody},
		{"CRC截断", data[:len(data)-2], hashpart.ErrCRC},
	}
	for _, c := range cases {
		p := filepath.Join(dir, "part-0000-seg-000000")
		if err := os.WriteFile(p, c.data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := CheckFile(p); !errors.Is(err, c.want) {
			t.Errorf("%s: 期望 %v, 得到 %v", c.name, c.want, err)
		}
	}
}
