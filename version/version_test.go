package version

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    Range
		wantErr bool
	}{
		{name: "single", in: "1", want: Range{1, 1}},
		{name: "single max", in: "1000", want: Range{1000, 1000}},
		{name: "single mid", in: "42", want: Range{42, 42}},
		{name: "closed", in: "1-1000", want: Range{1, 1000}},
		{name: "closed equal", in: "5-5", want: Range{5, 5}},
		{name: "open ended", in: "3-", want: Range{3, 1000}},
		{name: "open ended max", in: "1000-", want: Range{1000, 1000}},

		{name: "empty", in: "", wantErr: true},
		{name: "zero", in: "0", wantErr: true},
		{name: "leading zero", in: "01", wantErr: true},
		{name: "leading zero multi", in: "007", wantErr: true},
		{name: "double zero", in: "00", wantErr: true},
		{name: "above max", in: "1001", wantErr: true},
		{name: "huge", in: "99999999999999999999", wantErr: true},
		{name: "sign plus", in: "+1", wantErr: true},
		{name: "sign minus", in: "-5", wantErr: true},
		{name: "space", in: " 1", wantErr: true},
		{name: "trailing space", in: "1 ", wantErr: true},
		{name: "alpha", in: "abc", wantErr: true},
		{name: "decimal", in: "1.5", wantErr: true},
		{name: "dash only", in: "-", wantErr: true},
		{name: "lo zero", in: "0-1", wantErr: true},
		{name: "lo leading zero", in: "01-2", wantErr: true},
		{name: "hi leading zero", in: "1-02", wantErr: true},
		{name: "hi zero", in: "1-0", wantErr: true},
		{name: "hi above max", in: "1-1001", wantErr: true},
		{name: "lo gt hi", in: "2-1", wantErr: true},
		{name: "double dash", in: "1--2", wantErr: true},
		{name: "triple segment", in: "1-2-3", wantErr: true},
		{name: "open then dash", in: "1--", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr {
				if !errors.Is(err, ErrSyntax) {
					t.Fatalf("Parse(%q) err = %v, want ErrSyntax", tt.in, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected err = %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}
