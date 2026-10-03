// Package sms implements deterministic SMS segmentation, periodic billing, and balances.
//
// All public operations are serialized by a single mutex and are safe for concurrent use.
// Quote and Split do not mutate state; Quote performs the same validation and pricing as
// an immediately following Send.
package sms
