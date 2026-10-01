package zlibstore

import (
	"compress/zlib"
	"io"
)

func zlibNewReader(r io.Reader) (io.ReadCloser, error) {
	return zlib.NewReader(r)
}

func zlibReadAll(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}
