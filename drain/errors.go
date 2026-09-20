package drain

import "errors"

// ErrShuttingDown 在停机开始后由 Enter 返回：此时不再接收新请求。
var ErrShuttingDown = errors.New("drain: shutting down, new requests are rejected")

// ErrDrainTimeout 在到达 deadline 后仍有在途请求时由 Shutdown 返回。
var ErrDrainTimeout = errors.New("drain: timed out waiting for in-flight requests")
