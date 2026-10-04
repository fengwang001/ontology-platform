package forward

import (
	"reflect"
	"testing"
)

func TestDecide(t *testing.T) {
	cases := []struct {
		name    string
		P       int
		group   uint32
		in      int
		members []int
		routers []int
		flood   bool
		want    []int
	}{
		{"link-local flood", 4, 0xE0000001, 1, nil, nil, false, []int{2, 3, 4}},
		{"known members union routers", 4, 0xE1000001, 3, []int{1, 2}, []int{2, 4}, false, []int{1, 2, 4}},
		{"unknown no flood routers only", 4, 0xE1000001, 1, nil, []int{2, 4}, false, []int{2, 4}},
		{"unknown flood", 4, 0xE1000001, 3, nil, nil, true, []int{1, 2, 4}},
		{"members excludes in", 4, 0xE1000001, 2, []int{1, 2}, nil, false, []int{1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Decide(tc.P, tc.group, tc.in, tc.members, tc.routers, tc.flood)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
