package keyring

import (
	"errors"
	"strconv"
	"testing"
)

func TestNewRoleAndVerify(t *testing.T) {
	tests := []struct {
		name      string
		keyIDs    []string
		threshold int
		sigs      []string
		want      bool
		wantErr   error
	}{
		{name: "single key", keyIDs: []string{"A"}, threshold: 1, sigs: []string{"A"}, want: true},
		{name: "duplicates count once", keyIDs: []string{"A", "B"}, threshold: 2, sigs: []string{"A", "A", "A"}, want: false},
		{name: "unknown keys ignored", keyIDs: []string{"A", "B"}, threshold: 2, sigs: []string{"X", "A", "B", "Y"}, want: true},
		{name: "below threshold", keyIDs: []string{"A", "B", "C"}, threshold: 2, sigs: []string{"A", "X"}, want: false},
		{name: "empty key ids", threshold: 1, wantErr: ErrInvalidArgument},
		{name: "duplicate role keys", keyIDs: []string{"A", "A"}, threshold: 1, wantErr: ErrInvalidArgument},
		{name: "zero threshold", keyIDs: []string{"A"}, threshold: 0, wantErr: ErrInvalidArgument},
		{name: "threshold above keys", keyIDs: []string{"A"}, threshold: 2, wantErr: ErrInvalidArgument},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, err := NewRole(tt.keyIDs, tt.threshold)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("NewRole() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				return
			}

			if got := role.Verify(tt.sigs, nil); got != tt.want {
				t.Fatalf("Verify() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVerifyLookupsBoundedBySignatureCount(t *testing.T) {
	small := makeRole(t, 4)
	large := makeRole(t, 4000)

	signatures := []string{"A", "A", "unknown", "B"}
	smallLookups := 0
	largeLookups := 0

	if !small.Verify(signatures, &smallLookups) {
		t.Fatal("small role verification failed")
	}
	if !large.Verify(signatures, &largeLookups) {
		t.Fatal("large role verification failed")
	}
	if smallLookups != 3 || largeLookups != 3 {
		t.Fatalf("lookups = %d and %d, want 3 each", smallLookups, largeLookups)
	}
	if largeLookups > len(signatures) {
		t.Fatalf("lookups %d exceed signature count %d", largeLookups, len(signatures))
	}
}

func makeRole(t *testing.T, count int) Role {
	t.Helper()

	keys := []string{"A", "B", "C", "D"}
	for i := 4; i < count; i++ {
		keys = append(keys, "K"+strconv.Itoa(i))
	}

	role, err := NewRole(keys, 2)
	if err != nil {
		t.Fatal(err)
	}
	return role
}
