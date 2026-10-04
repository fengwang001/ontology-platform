package otdoc

import "testing"

func TestRefXformSmoke(t *testing.T) {
	cases := []struct {
		s, c []Comp
		want []Comp
	}{
		{[]Comp{r(2), d(2), r(1)}, []Comp{r(3), i("X"), r(2)}, []Comp{r(2), i("X"), r(1)}},
		{[]Comp{r(1), i("P"), r(1)}, []Comp{r(1), i("Q"), r(1)}, []Comp{r(2), i("Q"), r(1)}},
		{[]Comp{r(1), d(2), r(1)}, []Comp{r(1), d(2), r(1)}, []Comp{r(2)}},
	}
	for idx, tc := range cases {
		s := mustOp(t, tc.s)
		c := mustOp(t, tc.c)
		want := mustOp(t, tc.want)
		d := refBuild(c, baseLength(c))
		got := refXform(s, d).toOp()
		if !opsEqual(got, want) {
			t.Fatalf("case %d got %s want %s", idx, opString(got), opString(want))
		}
		if !opsEqual(transform(s, c), want) {
			t.Fatalf("case %d fast mismatch", idx)
		}
	}
}
