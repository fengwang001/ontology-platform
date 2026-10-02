package cron

import "testing"

func TestParseBasics(t *testing.T) {
	s, err := Parse("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	if !s.dayWild || !s.weekWild {
		t.Fatal("bare * must set wildcard flags")
	}

	s, err = Parse("*/2 3-5 */2 1,2,6 0,6")
	if err != nil {
		t.Fatal(err)
	}
	if !s.minutes[0] || !s.minutes[58] || s.minutes[1] {
		t.Fatal("*/2 minute set wrong")
	}
	if s.hours[2] || !s.hours[3] || !s.hours[5] || s.hours[6] {
		t.Fatal("3-5 hour set wrong")
	}
	if !s.dayWild {
		t.Fatal("*/2 must count as wildcard day field")
	}
	if s.weekWild {
		t.Fatal("0,6 is not a wildcard week field")
	}
	if !s.days[0] || !s.days[2] || s.days[1] {
		t.Fatal("*/2 day set wrong (1,3,5...)")
	}
	if !s.months[0] || !s.months[5] || s.months[2] {
		t.Fatal("month list wrong")
	}
	if !s.weekdays[0] || !s.weekdays[6] || s.weekdays[1] {
		t.Fatal("weekday list wrong")
	}
}

func TestParseAStepFromValue(t *testing.T) {
	s, err := Parse("5/20 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	for _, mi := range []int{5, 25, 45} {
		if !s.minutes[mi] {
			t.Fatalf("minute %d should match", mi)
		}
	}
	for _, mi := range []int{0, 4, 6, 24, 26, 44, 46, 59} {
		if s.minutes[mi] {
			t.Fatalf("minute %d should not match", mi)
		}
	}

	// Field upper bounds: step equal to value count is legal.
	for _, good := range []string{"*/60 * * * *", "*/24 * * * *", "* * */31 * *", "* * * */12 *", "* * * * */7"} {
		if _, err := Parse(good); err != nil {
			t.Fatalf("%q should parse: %v", good, err)
		}
	}
}

func TestParseDuplicatesAndCommas(t *testing.T) {
	s, err := Parse("0,0,0 1,1,1 1,1,1 1,1,1 0,0")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, b := range s.minutes {
		if b {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("duplicate items must collapse, got %d matching minutes", count)
	}

	// 15,30,45 within 0-45 range.
	s, err = Parse("15-45/15 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	for _, mi := range []int{15, 30, 45} {
		if !s.minutes[mi] {
			t.Fatalf("minute %d should match", mi)
		}
	}
}

func TestParseErrorsInOrder(t *testing.T) {
	bad := []struct {
		spec string
		err  error
	}{
		{"* * * *", ErrSyntax},
		{"* * * * * *", ErrSyntax},
		{"*  * * * *", ErrSyntax}, // consecutive space -> empty field
		{" * * * * *", ErrSyntax},
		{"* * * * ", ErrSyntax}, // trailing space -> empty field
		{"60 * * * *", ErrValueOutOfRange},
		{"* 24 * * *", ErrValueOutOfRange},
		{"* * 32 * *", ErrValueOutOfRange},
		{"* * 0 * *", ErrValueOutOfRange},
		{"* * * 13 *", ErrValueOutOfRange},
		{"* * * * 7", ErrValueOutOfRange}, // 7 is illegal
		{"* * * * 8", ErrValueOutOfRange},
		{"5-3 * * * *", ErrReversedRange},
		{"* 9-3 * * *", ErrReversedRange},
		{"*/0 * * * *", ErrInvalidStep},
		{"*/61 * * * *", ErrInvalidStep},
		{"* */25 * * *", ErrInvalidStep},
		{"* * */32 * *", ErrInvalidStep},
		{"* * * */13 *", ErrInvalidStep},
		{"* * * * */8", ErrInvalidStep},
		{"a * * * *", ErrSyntax},
		{"* * * 1- *", ErrSyntax},
		{"* * * * 0-", ErrSyntax},
		{"1--2 * * * *", ErrSyntax},
		{"** * * * *", ErrSyntax},
		{"1/2/3 * * * *", ErrSyntax},
		{"*/* * * * *", ErrSyntax},
	}
	for _, c := range bad {
		_, err := Parse(c.spec)
		if err != c.err {
			t.Errorf("Parse(%q) = %v, want %v", c.spec, err, c.err)
		}
	}
}

func TestParseFieldOrderErrorPrecedence(t *testing.T) {
	// Minute field error reported before hour field error.
	_, err := Parse("60 24 * * *")
	if err != ErrValueOutOfRange {
		t.Fatalf("got %v", err)
	}
	// Item order: first item error wins.
	_, err = Parse("5-3,60 * * * *")
	if err != ErrReversedRange {
		t.Fatalf("got %v want reversed range", err)
	}
	// Within an item: syntax beats range beats reversed beats step.
	_, err = Parse("x-60 * * * *")
	if err != ErrSyntax {
		t.Fatalf("got %v", err)
	}
	_, err = Parse("0-60/0 * * * *")
	if err != ErrValueOutOfRange {
		t.Fatalf("got %v", err)
	}
	_, err = Parse("60-61/0 * * * *")
	if err != ErrValueOutOfRange {
		t.Fatalf("got %v", err)
	}
}
