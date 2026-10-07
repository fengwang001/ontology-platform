package temporalauth

import "testing"

func TestCivilRoundTrip(t *testing.T) {
	dates := []Civil{
		{1900, 3, 1, 0, 0, 0},
		{1969, 12, 31, 23, 59, 59},
		{1970, 1, 1, 0, 0, 0},
		{2000, 2, 29, 12, 0, 0},
		{2024, 3, 31, 1, 30, 0},
		{2100, 3, 1, 0, 0, 0}, // 2100 非闰年
	}
	for _, c := range dates {
		got := secondsToCivil(civilToSeconds(c))
		if got != c {
			t.Fatalf("round trip %+v -> %+v", c, got)
		}
	}
}

// 构造一个固定夏令时规则：标准时 +0；春季 gap（本地 02:00->03:00），
// 秋季 overlap（本地 03:00->02:00）。跃迁点以 UTC 绝对时刻给出。
func testZone() *ZoneRules {
	return &ZoneRules{
		ID:         "X",
		BaseOffset: 0,
		Transitions: []Transition{
			{At: 1711846800, OffsetAfter: 3600}, // 2024-03-31 01:00Z
			{At: 1729990800, OffsetAfter: 0},    // 2024-10-27 01:00Z
		},
	}
}

func TestOffsetAtTransitionsHalfOpen(t *testing.T) {
	r := testZone()
	spring, autumn := Instant(1711846800), Instant(1729990800)
	if r.OffsetAt(spring-1) != 0 {
		t.Fatal("offset before spring must be 0")
	}
	if r.OffsetAt(spring) != 3600 {
		t.Fatal("offset at spring instant must be 3600")
	}
	if r.OffsetAt(autumn-1) != 3600 {
		t.Fatal("offset before autumn must be 3600")
	}
	if r.OffsetAt(autumn) != 0 {
		t.Fatal("offset at autumn instant must be 0")
	}
}

func TestCivilToInstantExhaustiveDSTDay(t *testing.T) {
	r := testZone()
	spring, autumn := Instant(1711846800), Instant(1729990800)

	// 跃迁点 01:00Z 时本地从 01:00 跳到 02:00，故 gap 为本地 [01:00,02:00)。
	// [00:00,01:00) 正常冬令时；[01:00,02:00) gap 全部归一化到跃迁点；
	// [02:00,24:00) 正常夏令时。
	for sec := 0; sec < 86400; sec++ {
		c := Civil{2024, 3, 31, sec / 3600, (sec % 3600) / 60, sec % 60}
		got := r.CivilToInstant(c)
		switch {
		case sec < 1*3600:
			if want := spring - Instant(3600) + Instant(sec); got != want {
				t.Fatalf("spring normal %v: got %d want %d", c, got, want)
			}
		case sec < 2*3600:
			if got != spring {
				t.Fatalf("spring gap %v: got %d want %d", c, got, spring)
			}
		default:
			if want := spring + Instant(sec-2*3600); got != want {
				t.Fatalf("spring summer %v: got %d want %d", c, got, want)
			}
		}
	}

	// 跃迁点 01:00Z 时本地从 02:00 回到 01:00，故 overlap 为本地 [01:00,02:00)。
	// [00:00,01:00) 夏令时；[01:00,02:00) overlap 取更早（夏令时）瞬间；
	// [02:00,24:00) 标准时。
	for sec := 0; sec < 86400; sec++ {
		c := Civil{2024, 10, 27, sec / 3600, (sec % 3600) / 60, sec % 60}
		got := r.CivilToInstant(c)
		switch {
		case sec < 2*3600: // 00:00..01:59:59，含 overlap 01:xx（取夏令时更早瞬间）
			if want := autumn - 7200 + Instant(sec); got != want {
				t.Fatalf("autumn dst/overlap %v: got %d want %d", c, got, want)
			}
		default: // 02:00 起为标准时
			if want := autumn + Instant(sec) - 3600; got != want {
				t.Fatalf("autumn std %v: got %d want %d", c, got, want)
			}
		}
	}
}

func TestInstantToCivilInverseOnNormalDays(t *testing.T) {
	r := testZone()
	// 对夏令时之外的一整段连续绝对时刻，双向换算必须恒等。
	for at := Instant(1704067200); at < 1704067200+3*86400; at += 7 {
		c := r.InstantToCivil(at)
		if got := r.CivilToInstant(c); got != at {
			t.Fatalf("inverse at %d: %+v -> %d", at, c, got)
		}
	}
}
