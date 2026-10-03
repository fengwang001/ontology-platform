package key

import "testing"

func TestIdentity(t *testing.T) {
	cases := []struct {
		name string
		id   Identity
		hdr  map[string]string
		want bool
	}{
		{"empty vary matches anything", Identity{}, map[string]string{"x": "y"}, true},
		{"exact match", Build("", []string{"accept-language"}, map[string]string{"accept-language": "en"}), map[string]string{"accept-language": "en"}, true},
		{"missing header equals empty", Build("", []string{"accept-language"}, map[string]string{}), map[string]string{}, true},
		{"missing vs present empty string", Build("", []string{"accept-language"}, map[string]string{}), map[string]string{"accept-language": ""}, true},
		{"different value", Build("", []string{"accept-language"}, map[string]string{"accept-language": "en"}), map[string]string{"accept-language": "fr"}, false},
		{"all headers must match", Build("", []string{"a", "b"}, map[string]string{"a": "1", "b": "2"}), map[string]string{"a": "1", "b": "3"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.id.Matches(tc.hdr); got != tc.want {
				t.Fatalf("Matches=%v want %v; id=%+v hdr=%v", got, tc.want, tc.id, tc.hdr)
			}
		})
	}

	a := Build("u", []string{"b", "a"}, map[string]string{"a": "1", "b": "2"})
	b := Build("u", []string{"a", "b", "a"}, map[string]string{"a": "1", "b": "2"})
	if !a.Equal(b) {
		t.Fatalf("sorted+dedup identities should be equal: %+v vs %+v", a, b)
	}
	c := Build("", []string{"a", "b"}, map[string]string{"a": "1", "b": "2"})
	if a.Equal(c) {
		t.Fatalf("different owners must not be equal: %+v vs %+v", a, c)
	}
	if got := len(a.Names); got != 2 || a.Names[0] != "a" {
		t.Fatalf("names not sorted/deduped: %+v", a.Names)
	}
}
