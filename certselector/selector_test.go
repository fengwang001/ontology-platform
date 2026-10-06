package certselector

import (
	"errors"
	"testing"
)

func keyTypes(types ...KeyType) map[KeyType]bool {
	result := make(map[KeyType]bool, len(types))
	for _, keyType := range types {
		result[keyType] = true
	}
	return result
}

func cert(id string, names []string, keyType KeyType, notBefore int64, notAfter int64) Certificate {
	return Certificate{
		ID:        id,
		Names:     names,
		KeyType:   keyType,
		NotBefore: notBefore,
		NotAfter:  notAfter,
	}
}

func addCerts(t *testing.T, selector *Selector, certificates ...Certificate) {
	t.Helper()
	for _, certificate := range certificates {
		if err := selector.Add(certificate); err != nil {
			t.Fatalf("Add(%s): %v", certificate.ID, err)
		}
	}
}

func assertSelection(t *testing.T, selection Selection, err error, wantID string, wantSource MatchSource) {
	t.Helper()
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if selection.Certificate.ID != wantID || selection.Source != wantSource {
		t.Fatalf("Select() = (%s, %v), want (%s, %v)", selection.Certificate.ID, selection.Source, wantID, wantSource)
	}
}

func assertError(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want %v", err, target)
	}
}

func TestValidityBoundaries(t *testing.T) {
	selector := New()
	addCerts(t, selector, cert("edge", []string{"example.com"}, KeyTypeRSA, 10, 20))

	_, err := selector.Select(SelectInput{Name: "example.com", KeyTypes: keyTypes(KeyTypeRSA), Now: 9})
	assertError(t, err, ErrNoValidCertificate)

	selection, err := selector.Select(SelectInput{Name: "example.com", KeyTypes: keyTypes(KeyTypeRSA), Now: 10})
	assertSelection(t, selection, err, "edge", MatchExact)
	selection, err = selector.Select(SelectInput{Name: "example.com", KeyTypes: keyTypes(KeyTypeRSA), Now: 19})
	assertSelection(t, selection, err, "edge", MatchExact)

	_, err = selector.Select(SelectInput{Name: "example.com", KeyTypes: keyTypes(KeyTypeRSA), Now: 20})
	assertError(t, err, ErrNoValidCertificate)
}

func TestWildcardLabelBoundary(t *testing.T) {
	selector := New()
	addCerts(t, selector, cert("good", []string{"*.example.com"}, KeyTypeRSA, 0, 100))

	selection, err := selector.Select(SelectInput{Name: "api.example.com", KeyTypes: keyTypes(KeyTypeRSA), Now: 0})
	assertSelection(t, selection, err, "good", MatchWildcard)
	_, err = selector.Select(SelectInput{Name: "example.com", KeyTypes: keyTypes(KeyTypeRSA), Now: 0})
	assertError(t, err, ErrNoMatchingCertificate)
	_, err = selector.Select(SelectInput{Name: "v1.api.example.com", KeyTypes: keyTypes(KeyTypeRSA), Now: 0})
	assertError(t, err, ErrNoMatchingCertificate)

	for _, name := range []string{"*a.example.com", "a.*.example.com", "*.com", "*."} {
		assertError(t, selector.Add(cert("bad-"+name, []string{name}, KeyTypeRSA, 0, 100)), ErrInvalidArgument)
	}
}

func TestExactMatchSuppressesAvailableWildcard(t *testing.T) {
	selector := New()
	addCerts(t, selector,
		cert("exact-expired", []string{"api.example.com"}, KeyTypeEC, 0, 10),
		cert("wild-available", []string{"*.example.com"}, KeyTypeEC, 0, 100),
	)

	_, err := selector.Select(SelectInput{Name: "api.example.com", KeyTypes: keyTypes(KeyTypeEC), Now: 50})
	assertError(t, err, ErrNoValidCertificate)
}

func TestDefaultFallbackBoundary(t *testing.T) {
	selector := New()
	addCerts(t, selector,
		cert("named", []string{"api.example.com"}, KeyTypeEC, 0, 100),
		cert("default", []string{"default.invalid"}, KeyTypeRSA, 0, 100),
	)
	if err := selector.SetDefault("default"); err != nil {
		t.Fatalf("SetDefault(): %v", err)
	}

	selection, err := selector.Select(SelectInput{Name: "other.example.com", KeyTypes: keyTypes(KeyTypeRSA), Now: 0})
	assertSelection(t, selection, err, "default", MatchDefault)
	_, err = selector.Select(SelectInput{Name: "api.example.com", KeyTypes: keyTypes(KeyTypeRSA), Now: 0})
	assertError(t, err, ErrUnsupportedKeyType)

	selection, err = selector.Select(SelectInput{Name: "", KeyTypes: keyTypes(KeyTypeRSA), Now: 0})
	assertSelection(t, selection, err, "default", MatchDefault)
	selector.RemoveDefault()
	_, err = selector.Select(SelectInput{Name: "", KeyTypes: keyTypes(KeyTypeRSA), Now: 0})
	assertError(t, err, ErrNoMatchingCertificate)
}

func TestRemovingDefaultCertificateClearsDefault(t *testing.T) {
	selector := New()
	addCerts(t, selector, cert("default", []string{"default.invalid"}, KeyTypeRSA, 0, 100))
	if err := selector.SetDefault("default"); err != nil {
		t.Fatalf("SetDefault(): %v", err)
	}
	if err := selector.Remove("default"); err != nil {
		t.Fatalf("Remove(): %v", err)
	}
	_, err := selector.Select(SelectInput{Name: "", KeyTypes: keyTypes(KeyTypeRSA), Now: 0})
	assertError(t, err, ErrNoMatchingCertificate)
	assertError(t, selector.SetDefault("default"), ErrCertificateNotFound)
}

func TestPreferenceOrder(t *testing.T) {
	tests := []struct {
		name   string
		certs  []Certificate
		wantID string
	}{
		{
			name: "ec before rsa",
			certs: []Certificate{
				cert("rsa-later", []string{"example.com"}, KeyTypeRSA, 0, 100),
				cert("ec-earlier", []string{"example.com"}, KeyTypeEC, 0, 50),
			},
			wantID: "ec-earlier",
		},
		{
			name: "later expiry before id",
			certs: []Certificate{
				cert("zzz", []string{"example.com"}, KeyTypeRSA, 0, 90),
				cert("aaa", []string{"example.com"}, KeyTypeRSA, 0, 80),
			},
			wantID: "zzz",
		},
		{
			name: "smaller id on tie",
			certs: []Certificate{
				cert("zzz", []string{"example.com"}, KeyTypeRSA, 0, 80),
				cert("aaa", []string{"example.com"}, KeyTypeRSA, 0, 80),
			},
			wantID: "aaa",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			selector := New()
			addCerts(t, selector, tt.certs...)
			selection, err := selector.Select(SelectInput{Name: "example.com", KeyTypes: keyTypes(KeyTypeEC, KeyTypeRSA), Now: 0})
			assertSelection(t, selection, err, tt.wantID, MatchExact)
		})
	}
}

func TestFailurePriorityAndRejectedOperations(t *testing.T) {
	selector := New()
	addCerts(t, selector, cert("active-rsa", []string{"example.com"}, KeyTypeRSA, 0, 100))

	_, err := selector.Select(SelectInput{Name: "example.com", KeyTypes: nil, Now: -1})
	assertError(t, err, ErrInvalidArgument)
	_, err = selector.Select(SelectInput{Name: "bad..name", KeyTypes: keyTypes(KeyTypeRSA), Now: 0})
	assertError(t, err, ErrInvalidArgument)

	_, err = selector.Select(SelectInput{Name: "example.com", KeyTypes: keyTypes(KeyTypeEC), Now: 0})
	assertError(t, err, ErrUnsupportedKeyType)

	assertError(t, selector.Add(cert("active-rsa", []string{"other.example.com"}, KeyTypeRSA, 0, 100)), ErrCertificateConflict)
	assertError(t, selector.Add(cert("", []string{"other.example.com"}, KeyTypeRSA, 0, 100)), ErrInvalidArgument)
	assertError(t, selector.Add(cert("empty", nil, KeyTypeRSA, 0, 100)), ErrInvalidArgument)
	assertError(t, selector.Remove("missing"), ErrCertificateNotFound)
	assertError(t, selector.SetDefault("missing"), ErrCertificateNotFound)

	selection, err := selector.Select(SelectInput{Name: "example.com", KeyTypes: keyTypes(KeyTypeRSA), Now: 0})
	assertSelection(t, selection, err, "active-rsa", MatchExact)
}
