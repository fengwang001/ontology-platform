package cascade

import "testing"

var layout = Layout{Levels: 4, Size: 64}

func TestLevel(t *testing.T) {
	cases := []struct {
		remaining int64
		want      int
	}{
		{1, 0}, {63, 0}, {64, 1}, {4095, 1},
		{4096, 2}, {262143, 2}, {262144, 3}, {16777215, 3},
	}
	for _, c := range cases {
		if got := layout.Level(c.remaining); got != c.want {
			t.Fatalf("Level(%d) = %d, want %d", c.remaining, got, c.want)
		}
	}
}

func TestSlotAndCursor(t *testing.T) {
	cases := []struct {
		deadline int64
		level    int
		want     int
	}{
		{0, 0, 0}, {5, 0, 5}, {64, 0, 0}, {65, 0, 1},
		{64, 1, 1}, {128, 1, 2}, {4096, 2, 1}, {16777215, 3, 63},
	}
	for _, c := range cases {
		if got := layout.Slot(c.deadline, c.level); got != c.want {
			t.Fatalf("Slot(%d, %d) = %d, want %d", c.deadline, c.level, got, c.want)
		}
		if got := layout.Cursor(c.deadline, c.level); got != c.want {
			t.Fatalf("Cursor(%d, %d) = %d, want %d", c.deadline, c.level, got, c.want)
		}
	}
}

func TestRanges(t *testing.T) {
	cases := []struct {
		level int
		gran  int64
	}{
		{0, 1}, {1, 64}, {2, 4096}, {3, 262144},
	}
	for _, c := range cases {
		if got := layout.Granularity(c.level); got != c.gran {
			t.Fatalf("Granularity(%d) = %d, want %d", c.level, got, c.gran)
		}
	}
	if got := layout.MaxDelay(); got != 16777215 {
		t.Fatalf("MaxDelay = %d, want 16777215", got)
	}
}
