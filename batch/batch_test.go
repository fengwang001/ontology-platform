package batch

import (
	"bytes"
	"errors"
	"testing"
)

func TestValidate(t *testing.T) {
	errDup := errors.New("dup")
	cases := []struct {
		name    string
		b       Batch
		wantErr error
		dupKey  string
		dupPos  [2]int
	}{
		{"empty id", Batch{ID: ""}, ErrEmptyID, "", [2]int{}},
		{"empty records", Batch{ID: "b"}, nil, "", [2]int{}},
		{"empty key legal", Batch{ID: "b", Records: []Record{{Key: ""}}}, nil, "", [2]int{}},
		{"single", Batch{ID: "b", Records: []Record{{Key: "a"}}}, nil, "", [2]int{}},
		{"dup key", Batch{ID: "b", Records: []Record{{Key: "x"}, {Key: "y"}, {Key: "x"}}},
			errDup, "x", [2]int{0, 2}},
		{"dup empty key", Batch{ID: "b", Records: []Record{{Key: ""}, {Key: ""}}},
			errDup, "", [2]int{0, 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.b.Validate()
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected err %v", err)
				}
				return
			}
			if tc.wantErr == errDup {
				var d *DupKeyError
				if !errors.As(err, &d) {
					t.Fatalf("want DupKeyError, got %T %v", err, err)
				}
				if d.Key != tc.dupKey || d.First != tc.dupPos[0] || d.Second != tc.dupPos[1] {
					t.Fatalf("got key=%q pos=%d,%d want %q %d,%d",
						d.Key, d.First, d.Second, tc.dupKey, tc.dupPos[0], tc.dupPos[1])
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v got %v", tc.wantErr, err)
			}
		})
	}
}

func TestEncodeDecode(t *testing.T) {
	cases := []Batch{
		{ID: "b0"},
		{ID: "b1", Records: []Record{{Key: "a"}, {Key: ""}, {Key: "中文"}, {Key: "x y"}}},
	}
	for i, orig := range cases {
		var buf bytes.Buffer
		if err := orig.Encode(&buf); err != nil {
			t.Fatalf("case %d encode: %v", i, err)
		}
		got, err := Decode(&buf)
		if err != nil {
			t.Fatalf("case %d decode: %v", i, err)
		}
		if got.ID != orig.ID || got.Len() != orig.Len() {
			t.Fatalf("case %d header mismatch", i)
		}
		for j := range orig.Records {
			if got.KeyAt(j) != orig.KeyAt(j) {
				t.Fatalf("case %d rec %d: %q != %q", i, j, got.KeyAt(j), orig.KeyAt(j))
			}
		}
	}
}
