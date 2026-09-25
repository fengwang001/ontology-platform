// Package par 把输入按任意字节偏移切段并行转码后拼接，依赖 stream、u8。
package par

import "ontology/stream"

// Transcode 把 data 切成 k 段并行转码并拼接，返回与单线程流式完全一致的结果。
func Transcode(data []byte, c stream.Config, k int) ([]byte, stream.Stats, error) {
	return nil, stream.Stats{}, nil
}
