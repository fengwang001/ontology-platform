package cert_test

import (
	"errors"
	"testing"

	"ontology/cert"
)

func TestPureFunctions(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  any
		want any
	}{
		{"lapse max(now,nb): nb later", cert.LapseAt(950, 900, 100), int64(1050)},
		{"lapse max(now,nb): now later", cert.LapseAt(100, 500, 100), int64(600)},
		{"retire capped by na", cert.RetireAt(1000, 950, 40), int64(990)},
		{"retire beyond na uses na", cert.RetireAt(1000, 970, 40), int64(1000)},
		{"renew boundary equal", cert.WithinRenew(1000, 900, 100), true},
		{"renew before window", cert.WithinRenew(1000, 899, 100), false},
		{"renew expired allowed", cert.WithinRenew(1000, 1001, 100), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("got %v want %v", tc.got, tc.want)
			}
		})
	}
}

func TestValidationTable(t *testing.T) {
	t.Parallel()
	type cfg struct{ g, ttl, w int64 }
	cfgCases := []struct {
		c    cfg
		want bool
	}{
		{cfg{1, 1, 1}, true},
		{cfg{1e9, 1e9, 1e9}, true},
		{cfg{0, 1, 1}, false},
		{cfg{1, 1e9 + 1, 1}, false},
	}
	for _, tc := range cfgCases {
		if got := cert.ValidConfig(tc.c.g, tc.c.ttl, tc.c.w); got != tc.want {
			t.Errorf("ValidConfig(%+v)=%v want %v", tc.c, got, tc.want)
		}
	}

	certCases := []struct {
		name string
		c    cert.Cert
		want bool
	}{
		{"ok", cert.Cert{"s1", "d", 0, 1000}, true},
		{"empty serial", cert.Cert{"", "d", 0, 1}, false},
		{"empty dev", cert.Cert{"s", "", 0, 1}, false},
		{"nb==na", cert.Cert{"s", "d", 5, 5}, false},
		{"nb negative", cert.Cert{"s", "d", -1, 5}, false},
		{"na over max", cert.Cert{"s", "d", 0, cert.MaxTime + 1}, false},
	}
	for _, tc := range certCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Valid(); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}

	if cert.ValidTime(-1) || cert.ValidTime(cert.MaxTime+1) || !cert.ValidTime(cert.MaxTime) {
		t.Fatal("ValidTime boundaries wrong")
	}
	if !cert.Revoked.Terminal() || cert.Active.Terminal() {
		t.Fatal("Terminal classification wrong")
	}
}

func TestSentinelsDistinct(t *testing.T) {
	t.Parallel()
	errs := []error{cert.ErrInvalid, cert.ErrClockBack, cert.ErrDupSerial, cert.ErrPendingExists,
		cert.ErrTooEarly, cert.ErrUnknown, cert.ErrMismatch, cert.ErrFinal, cert.ErrRevoked,
		cert.ErrRetired, cert.ErrLapsed, cert.ErrNotYet, cert.ErrExpired, cert.ErrNoSession}
	for i := range errs {
		for j := i + 1; j < len(errs); j++ {
			if errors.Is(errs[i], errs[j]) {
				t.Fatalf("sentinels %d/%d collide", i, j)
			}
		}
	}
}
