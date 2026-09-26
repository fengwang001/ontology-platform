package errmap

import (
	"errors"
	"testing"

	"ontology/aggregate"
	"ontology/errdef"
	"ontology/wrap"
)

func TestHTTPStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found", errdef.ErrNotFound, 404},
		{"already exists", errdef.ErrAlreadyExists, 409},
		{"conflict", errdef.ErrConflict, 409},
		{"validation", errdef.ErrValidation, 400},
		{"permission", errdef.ErrPermissionDenied, 403},
		{"internal", errdef.ErrInternal, 500},
		{"base", errdef.ErrBase, 500},
		{"plain", errors.New("x"), 500},
		{"nil", nil, 200},
		{"aggregate uses first child",
			aggregate.New(errdef.ErrValidation, errdef.ErrConflict), 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HTTPStatus(tc.err); got != tc.want {
				t.Fatalf("HTTPStatus = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRoundTripFidelity(t *testing.T) {
	codes := []errdef.Code{
		errdef.CodeNotFound, errdef.CodeAlreadyExists, errdef.CodeConflict,
		errdef.CodeValidation, errdef.CodePermissionDenied,
		errdef.CodeInternal, errdef.CodeUnknown,
	}
	for _, code := range codes {
		base := errdef.New(code, "msg-"+string(code),
			errdef.WithObject("Person"), errdef.WithProperty("age"),
			errdef.WithCause(errdef.New(errdef.CodeInternal, "root")))
		forms := map[string]error{
			"typed":   base,
			"wrapped": wrap.Wrap(base, wrap.WithObject("Batch")),
			"wrapx2":  wrap.Wrap(wrap.Wrap(base), wrap.WithProperty("p")),
			"nested":  aggregate.New(base, errdef.New(errdef.CodeConflict, "c")),
		}
		for formName, err := range forms {
			t.Run(string(code)+"/"+formName, func(t *testing.T) {
				data, mErr := Marshal(err)
				if mErr != nil {
					t.Fatal(mErr)
				}
				back, uErr := Unmarshal(data)
				if uErr != nil {
					t.Fatal(uErr)
				}
				if errors.Is(back, errdef.ErrBase) != errors.Is(err, errdef.ErrBase) {
					t.Fatal("base match differs after round-trip")
				}
				wantNotFound := errors.Is(err, errdef.ErrNotFound)
				if errors.Is(back, errdef.ErrNotFound) != wantNotFound {
					t.Fatal("sentinel match differs after round-trip")
				}
				if errdef.CauseDepth(back) != errdef.CauseDepth(err) {
					t.Fatalf("depth %d != %d",
						errdef.CauseDepth(back), errdef.CauseDepth(err))
				}
				if formName == "typed" || formName == "wrapped" || formName == "wrapx2" {
					var typed *errdef.Error
					if !errors.As(back, &typed) {
						t.Fatal("typed error missing after round-trip")
					}
					if typed.Object() != "Person" || typed.Property() != "age" {
						t.Fatalf("context lost: %q/%q", typed.Object(), typed.Property())
					}
				}
				if formName == "nested" && !errors.Is(back, errdef.ErrConflict) {
					t.Fatal("aggregate child type lost after round-trip")
				}
			})
		}
	}
}

func TestUnknownCodeDegradesWithoutPanic(t *testing.T) {
	unknowns := []string{"", "future_quota", "totally_new"}
	for _, raw := range unknowns {
		t.Run(raw, func(t *testing.T) {
			var original error
			if raw == "" {
				original = errdef.New(errdef.CodeUnknown, "plain unknown")
			} else {
				original = errdef.NewUnknown(raw, "new kind")
			}
			data, _ := Marshal(original)
			back, err := Unmarshal(data)
			if err != nil || back == nil {
				t.Fatalf("unknown code must not panic/error: %v", err)
			}
			if !errors.Is(back, errdef.ErrBase) {
				t.Fatal("degraded error must still match base")
			}
			if errors.Is(back, errdef.ErrNotFound) {
				t.Fatal("unknown code must not match a concrete sentinel")
			}
			if HTTPStatus(back) != 500 {
				t.Fatal("unknown code must map to 500")
			}
			if raw != "" {
				var typed *errdef.Error
				if !errors.As(back, &typed) || typed.RawCode() != raw {
					t.Fatalf("raw code %q not preserved", raw)
				}
				data2, _ := Marshal(back)
				back2, _ := Unmarshal(data2)
				var typed2 *errdef.Error
				if !errors.As(back2, &typed2) || typed2.RawCode() != raw {
					t.Fatal("raw code must survive a second round-trip")
				}
			}
		})
	}
}
