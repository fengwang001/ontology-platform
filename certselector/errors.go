package certselector

import "errors"

var (
	ErrInvalidArgument       = errors.New("certselector: invalid argument")
	ErrCertificateConflict   = errors.New("certselector: certificate id conflict")
	ErrCertificateNotFound   = errors.New("certselector: certificate not found")
	ErrNoMatchingCertificate = errors.New("certselector: no matching certificate")
	ErrNoValidCertificate    = errors.New("certselector: no certificate is valid at the requested time")
	ErrUnsupportedKeyType    = errors.New("certselector: no matching certificate uses a supported key type")
)
