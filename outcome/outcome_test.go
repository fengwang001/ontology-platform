package outcome

import "testing"

func TestKindString(t *testing.T) {
	cases := []struct {
		kind Kind
		want string
	}{
		{Success, "success"},
		{Failure, "failure"},
		{Rejected, "rejected"},
		{Kind(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.kind.String(); got != c.want {
			t.Errorf("Kind(%d).String() = %q, want %q", int(c.kind), got, c.want)
		}
	}
}
