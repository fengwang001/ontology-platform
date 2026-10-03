package canon

import (
	"errors"
	"testing"
)

func TestBuild(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		path        string
		query       []Pair
		headers     []Pair
		signedNames []string
		payloadHash string
		want        string
		wantErr     error
	}{
		{
			name:        "spec example",
			method:      "GET",
			path:        "/a b/ü",
			query:       []Pair{{"b", "x/y"}, {"a", "1 2"}, {"a", ""}},
			headers:     []Pair{{"Host", " h.example "}, {"X-Date", "100"}, {"X-Tag", "a  b"}, {"x-tag", "c"}},
			signedNames: []string{"x-tag", "host", "x-date", "host"},
			payloadHash: "UNSIGNED",
			want:        "GET\n/a%20b/%C3%BC\na=&a=1%202&b=x%2Fy\nhost:h.example\nx-date:100\nx-tag:a b,c\nhost;x-date;x-tag\nUNSIGNED",
		},
		{
			name:        "encoding percent plus tilde nonascii",
			method:      "POST",
			path:        "/%+~/€",
			query:       []Pair{{"k%", "v+v~"}, {"ü", "/"}},
			headers:     []Pair{{"host", "h"}, {"x-date", "1"}},
			signedNames: []string{"host", "x-date"},
			payloadHash: "P",
			want:        "POST\n/%25%2B~/%E2%82%AC\n%C3%BC=%2F&k%25=v%2Bv~\nhost:h\nx-date:1\nhost;x-date\nP",
		},
		{
			name:        "root path empty query",
			method:      "GET",
			path:        "/",
			query:       nil,
			headers:     []Pair{{"Host", "h"}, {"X-Date", "7"}},
			signedNames: []string{"host", "x-date"},
			payloadHash: "H",
			want:        "GET\n/\n\nhost:h\nx-date:7\nhost;x-date\nH",
		},
		{
			name:        "method empty",
			method:      "",
			path:        "/",
			headers:     []Pair{{"Host", "h"}, {"X-Date", "1"}},
			signedNames: []string{"host", "x-date"},
			wantErr:     ErrInvalidParam,
		},
		{
			name:        "method lowercase",
			method:      "get",
			path:        "/",
			headers:     []Pair{{"Host", "h"}, {"X-Date", "1"}},
			signedNames: []string{"host", "x-date"},
			wantErr:     ErrInvalidParam,
		},
		{
			name:        "path not absolute",
			method:      "GET",
			path:        "x",
			headers:     []Pair{{"Host", "h"}, {"X-Date", "1"}},
			signedNames: []string{"host", "x-date"},
			wantErr:     ErrInvalidParam,
		},
		{
			name:        "missing host in signed names",
			method:      "GET",
			path:        "/",
			headers:     []Pair{{"Host", "h"}, {"X-Date", "1"}},
			signedNames: []string{"x-date"},
			wantErr:     ErrMissingSignedHeader,
		},
		{
			name:        "signed header absent from request",
			method:      "GET",
			path:        "/",
			headers:     []Pair{{"Host", "h"}, {"X-Date", "1"}},
			signedNames: []string{"host", "x-date", "x-missing"},
			wantErr:     ErrMissingSignedHeader,
		},
		{
			name:        "invalid param beats missing header",
			method:      "get",
			path:        "/",
			headers:     []Pair{{"X-Date", "1"}},
			signedNames: []string{"x-date"},
			wantErr:     ErrInvalidParam,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Build(tc.method, tc.path, tc.query, tc.headers, tc.signedNames, tc.payloadHash)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err == nil && got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestHeaderVisitsBound(t *testing.T) {
	for _, H := range []int{20, 2000} {
		headers := make([]Pair, 0, H+2)
		headers = append(headers, Pair{"Host", "h"}, Pair{"X-Date", "1"})
		for i := 2; i < H; i++ {
			headers = append(headers, Pair{"X-N" + itoa(i), "v"})
		}
		names := make([]string, 0, H)
		for _, h := range headers {
			names = append(names, h.Name)
		}
		S := len(names)
		_, err := Build("GET", "/", nil, headers, names, "P")
		if err != nil {
			t.Fatalf("H=%d: %v", H, err)
		}
		visits := HeaderVisits()
		if visits > uint64(H+S) {
			t.Fatalf("H=%d S=%d visits=%d > %d", H, S, visits, H+S)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
