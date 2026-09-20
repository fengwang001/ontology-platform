package pctenc

import "testing"

func TestRoundtripAllBytes(t *testing.T) {
	var all [256]byte
	for i := range all {
		all[i] = byte(i)
	}
	samples := []string{
		"",
		string(all[:]),
		unreserved,
		":@&=+$,/#?% +",
		"中文字符αβγ",
		"\x00\x01\x02\xff\xfe",
		"mixed 中 :@& /? %20",
	}

	for _, m := range []Mode{Path, Query, Fragment} {
		for _, s := range samples {
			enc := Encode(s, m)
			dec, err := Decode(enc, m)
			if err != nil {
				t.Fatalf("mode %v: Decode(Encode(%q)) error: %v", m, s, err)
			}
			if dec != s {
				t.Errorf("mode %v: roundtrip mismatch\n input: %q\n got:   %q", m, s, dec)
			}
		}
	}
}

func TestBytewiseCoverage(t *testing.T) {
	// Every single byte value must survive a round trip on its own.
	for b := 0; b < 256; b++ {
		s := string([]byte{byte(b)})
		for _, m := range []Mode{Path, Query, Fragment} {
			dec, err := Decode(Encode(s, m), m)
			if err != nil || dec != s {
				t.Fatalf("byte %#02x mode %v: got %q, %v", b, m, dec, err)
			}
		}
	}
}

func TestRepeatable(t *testing.T) {
	inputs := []string{"plain", "a b&c=中", ":/?#[]@", "%E4%B8%AD"}
	for _, m := range []Mode{Path, Query, Fragment} {
		for _, s := range inputs {
			e1, e2 := Encode(s, m), Encode(s, m)
			d1, err1 := Decode(s, m)
			d2, err2 := Decode(s, m)
			if e1 != e2 || d1 != d2 || err1 != err2 {
				t.Errorf("mode %v input %q: repeated calls differ", m, s)
			}
		}
	}
}
