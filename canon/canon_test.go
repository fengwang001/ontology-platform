package canon

import "testing"

func TestBuild(t *testing.T) {
	cases := []struct {
		name        string
		method      string
		path        string
		query       [][2]string
		headers     []Header
		signedNames []string
		payloadHash string
		want        string
		wantErr     error
	}{
		{
			name:        "题目示例",
			method:      "GET",
			path:        "/a b/ü",
			query:       [][2]string{{"b", "x/y"}, {"a", "1 2"}, {"a", ""}},
			headers:     []Header{{"Host", " h.example "}, {"X-Date", "100"}, {"X-Tag", "a  b"}, {"x-tag", "c"}},
			signedNames: []string{"x-tag", "host", "x-date", "host"},
			payloadHash: "UNSIGNED",
			want:        "GET\n/a%20b/%C3%BC\na=&a=1%202&b=x%2Fy\nhost:h.example\nx-date:100\nx-tag:a b,c\nhost;x-date;x-tag\nUNSIGNED",
		},
		{
			name:        "百分号与加号与波浪号",
			method:      "POST",
			path:        "/%+~",
			query:       [][2]string{{"k%", "v+~"}},
			headers:     []Header{{"Host", "h"}, {"X-Date", "1"}},
			signedNames: []string{"host", "x-date"},
			payloadHash: "P",
			want:        "POST\n/%25%2B~\nk%25=v%2B~\nhost:h\nx-date:1\nhost;x-date\nP",
		},
		{
			name:        "路径斜杠保留 query斜杠编码",
			method:      "GET",
			path:        "/a/b",
			query:       [][2]string{{"dir", "/a/b"}},
			headers:     []Header{{"host", "h"}, {"x-date", "1"}},
			signedNames: []string{"host", "x-date"},
			payloadHash: "P",
			want:        "GET\n/a/b\ndir=%2Fa%2Fb\nhost:h\nx-date:1\nhost;x-date\nP",
		},
		{
			name:        "同名头折叠合并与制表符",
			method:      "GET",
			path:        "/",
			query:       nil,
			headers:     []Header{{"X-T", "\t a\t\tb \t"}, {"x-t", "c"}, {"HOST", "h"}, {"X-DATE", "2"}},
			signedNames: []string{"x-t", "host", "x-date"},
			payloadHash: "P",
			want:        "GET\n/\n\nhost:h\nx-date:2\nx-t:a b,c\nhost;x-date;x-t\nP",
		},
		{
			name:        "method小写非法",
			method:      "get",
			path:        "/",
			headers:     []Header{{"host", "h"}, {"x-date", "1"}},
			signedNames: []string{"host", "x-date"},
			payloadHash: "P",
			wantErr:     ErrInvalidArg,
		},
		{
			name:        "method含非ASCII非法",
			method:      "GEÄ",
			path:        "/",
			headers:     []Header{{"host", "h"}, {"x-date", "1"}},
			signedNames: []string{"host", "x-date"},
			payloadHash: "P",
			wantErr:     ErrInvalidArg,
		},
		{
			name:        "path不以斜杠开头非法",
			method:      "GET",
			path:        "a/",
			headers:     []Header{{"host", "h"}, {"x-date", "1"}},
			signedNames: []string{"host", "x-date"},
			payloadHash: "P",
			wantErr:     ErrInvalidArg,
		},
		{
			name:        "缺host",
			method:      "GET",
			path:        "/",
			headers:     []Header{{"x-date", "1"}},
			signedNames: []string{"x-date"},
			payloadHash: "P",
			wantErr:     ErrMissingHeader,
		},
		{
			name:        "签名头在请求中缺失",
			method:      "GET",
			path:        "/",
			headers:     []Header{{"host", "h"}, {"x-date", "1"}},
			signedNames: []string{"host", "x-date", "x-missing"},
			payloadHash: "P",
			wantErr:     ErrMissingHeader,
		},
		{
			name:        "参数非法优先于缺签名头",
			method:      "get",
			path:        "/",
			headers:     []Header{{"x-date", "1"}},
			signedNames: []string{"x-date"},
			payloadHash: "P",
			wantErr:     ErrInvalidArg,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Build(tc.method, tc.path, tc.query, tc.headers, tc.signedNames, tc.payloadHash)
			if tc.wantErr != nil {
				if err != tc.wantErr {
					t.Fatalf("err = %v, want %v; output=%q", err, tc.wantErr, got)
				}
				t.Logf("输入 method=%q path=%q => 拒绝 %v（依据：%v）", tc.method, tc.path, err, err)
				return
			}
			if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if got != tc.want {
				t.Fatalf("canonical 不符:\n got=%q\nwant=%q", got, tc.want)
			}
			t.Logf("输入 method=%q path=%q query=%v => 规范串 %q", tc.method, tc.path, tc.query, got)
		})
	}
}

func TestHeaderVisitsBound(t *testing.T) {
	for _, h := range []int{20, 2000} {
		headers := make([]Header, 0, h)
		for i := 0; i < h; i++ {
			headers = append(headers, Header{"X-Rand-" + itoa(i), "v"})
		}
		headers = append(headers, Header{"Host", "hh"}, Header{"X-Date", "9"})
		signed := []string{"host", "x-date"}
		if _, err := Build("GET", "/", nil, headers, signed, "P"); err != nil {
			t.Fatalf("h=%d build: %v", h, err)
		}
		s := len(signed)
		t.Logf("H=%d S=%d headerVisits=%d 上界 H+S=%d", h, s, headerVisits, h+s)
		if headerVisits > h+s {
			t.Fatalf("H=%d: headerVisits=%d > H+S=%d", h, headerVisits, h+s)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
