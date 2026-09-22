package ival

import "errors"

// ErrInvalidInterval 表示左端点大于右端点。
var ErrInvalidInterval = errors.New("invalid interval: L > R")
