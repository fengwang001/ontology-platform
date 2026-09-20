package pctenc

import "errors"

// ErrBadEscape is returned when decoding encounters a malformed
// percent-escape: a trailing '%', a missing hex digit, or a non-hex
// digit where one is required.
var ErrBadEscape error = errors.New("pctenc: invalid percent-escape")
