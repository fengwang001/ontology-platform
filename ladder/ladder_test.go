package ladder_test

import (
	"errors"
	"testing"

	"ontology/ladder"
)

func template4() []ladder.Rung {
	return []ladder.Rung{
		{Name: "360", Height: 360, Bitrate: 800, Required: true},
		{Name: "720", Height: 720, Bitrate: 2500, Required: true},
		{Name: "1080", Height: 1080, Bitrate: 5000},
		{Name: "2160", Height: 2160, Bitrate: 16000},
	}
}

func TestNewTemplate(t *testing.T) {
	bad := [][]ladder.Rung{
		nil,
		make([]ladder.Rung, 17),
		{{Name: "", Height: 360, Bitrate: 800, Required: true}},
		{{Name: "a", Height: 0, Bitrate: 800, Required: true}},
		{{Name: "a", Height: 4321, Bitrate: 800, Required: true}},
		{{Name: "a", Height: 360, Bitrate: 0, Required: true}},
		{{Name: "a", Height: 360, Bitrate: 1e8 + 1, Required: true}},
		{
			{Name: "a", Height: 720, Bitrate: 800, Required: true},
			{Name: "b", Height: 360, Bitrate: 900, Required: true},
		},
		{
			{Name: "a", Height: 360, Bitrate: 900, Required: false},
			{Name: "b", Height: 720, Bitrate: 900, Required: false},
		},
		{
			{Name: "a", Height: 360, Bitrate: 900, Required: false},
			{Name: "b", Height: 720, Bitrate: 1000, Required: false},
		},
	}
	for i, rungs := range bad {
		if _, err := ladder.NewTemplate(rungs); !errors.Is(err, ladder.ErrInvalidTemplate) {
			t.Fatalf("case %d: want ErrInvalidTemplate, got %v", i, err)
		}
	}
}

func TestDerive(t *testing.T) {
	tests := []struct {
		name       string
		srcHeight  int
		srcBitrate int
		want       []ladder.Rung
		wantErr    error
	}{
		{
			name:       "example1_keep3_clamp",
			srcHeight:  1080,
			srcBitrate: 3000,
			want: []ladder.Rung{
				{Name: "360", Height: 360, Bitrate: 800, Required: true},
				{Name: "720", Height: 720, Bitrate: 2500, Required: true},
				{Name: "1080", Height: 1080, Bitrate: 3000},
			},
		},
		{
			name:       "example1_equal_bitrate_drops_optional",
			srcHeight:  1080,
			srcBitrate: 2500,
			want: []ladder.Rung{
				{Name: "360", Height: 360, Bitrate: 800, Required: true},
				{Name: "720", Height: 720, Bitrate: 2500, Required: true},
			},
		},
		{
			name:       "example1_required_dropped_step3_ok",
			srcHeight:  720,
			srcBitrate: 600,
			want: []ladder.Rung{
				{Name: "360", Height: 360, Bitrate: 600, Required: true},
			},
		},
		{
			name:       "example1_required_dropped_step1_source_insufficient",
			srcHeight:  480,
			srcBitrate: 5000,
			wantErr:    ladder.ErrSourceInsufficient,
		},
		{
			name:       "height_equal_kept",
			srcHeight:  720,
			srcBitrate: 2500,
			want: []ladder.Rung{
				{Name: "360", Height: 360, Bitrate: 800, Required: true},
				{Name: "720", Height: 720, Bitrate: 2500, Required: true},
			},
		},
		{
			name:       "all_above_source_optional_tail",
			srcHeight:  2160,
			srcBitrate: 20000,
			want:       template4(),
		},
		{
			name:       "invalid_source",
			srcHeight:  0,
			srcBitrate: 100,
			wantErr:    ladder.ErrInvalidTemplate,
		},
	}
	tmpl, err := ladder.NewTemplate(template4())
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tmpl.Derive(tt.srcHeight, tt.srcBitrate)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("want %v, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("want %+v, got %+v", tt.want, got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("rung %d: want %+v, got %+v", i, tt.want[i], got[i])
				}
			}
		})
	}
}

func TestTemplateImmutable(t *testing.T) {
	src := template4()
	tmpl, err := ladder.NewTemplate(src)
	if err != nil {
		t.Fatal(err)
	}
	src[0].Bitrate = 1
	got, err := tmpl.Derive(4320, 1e8)
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Bitrate != 800 {
		t.Fatalf("template must be copied, got bitrate %d", got[0].Bitrate)
	}
}
