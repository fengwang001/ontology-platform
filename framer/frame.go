package framer

type BodyMode uint8

const (
	BodyModeNone BodyMode = iota
	BodyModeFixed
	BodyModeChunked
)

type EventKind uint8

const (
	EventHeaders EventKind = iota + 1
	EventBody
	EventEnd
	EventReject
)

type ErrorKind string

const (
	ErrorHeaderTooLarge              ErrorKind = "header_too_large"
	ErrorNullByte                    ErrorKind = "null_byte"
	ErrorBareLineFeed                ErrorKind = "bare_line_feed"
	ErrorSyntax                      ErrorKind = "syntax"
	ErrorLengthAndTransferEncoding   ErrorKind = "length_and_transfer_encoding"
	ErrorInvalidContentLength        ErrorKind = "invalid_content_length"
	ErrorBodyTooLarge                ErrorKind = "body_too_large"
	ErrorUnsupportedTransferEncoding ErrorKind = "unsupported_transfer_encoding"
	ErrorChunkFormat                 ErrorKind = "chunk_format"
	ErrorTrailer                     ErrorKind = "trailer"
)

type Event struct {
	Kind    EventKind
	Mode    BodyMode
	Method  string
	Target  string
	BodyLen uint64
	Error   ErrorKind
}

type Limits struct {
	MaxHeaderBytes uint64
	MaxBodyBytes   uint64
}
