package partscan

import "errors"

// ErrNoPreamble 表示流的最开头不是 "--<boundary>\r\n"。
var ErrNoPreamble = errors.New("partscan: missing boundary preamble")

// ErrIncomplete 表示流结束时仍未见到结束分隔符。
var ErrIncomplete = errors.New("partscan: stream ended before closing boundary")

// ErrAfterClose 表示结束分隔符之后又喂入了非空数据。
var ErrAfterClose = errors.New("partscan: data fed after closing boundary")
