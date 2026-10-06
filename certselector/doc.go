// Package certselector selects a TLS certificate by SNI host name, the key
// types a client supports and the current time, over a hot-updatable set.
//
// Basic usage:
//
//	s := certselector.NewSelector()
//	err := s.Add(certselector.Certificate{
//		ID:        "ec-site",
//		Names:     []string{"site.example", "*.cdn.example"},
//		Key:       certselector.ECDSA,
//		NotBefore: 1700000000,
//		NotAfter:  1800000000,
//	})
//	_ = s.SetDefault("ec-site")
//
//	sel, err := s.Select("Host.CDN.Example.", map[certselector.KeyType]bool{
//		certselector.ECDSA: true,
//	}, time.Now().Unix())
//	// sel.Source is SourceExact / SourceWildcard / SourceDefault.
//
// Failures are sentinel errors wrapped by *FailureError; classify them with
// errors.Is(err, certselector.ErrExpired) etc. See DESIGN.md for the full
// specification, concurrency model and complexity arguments.
package certselector
