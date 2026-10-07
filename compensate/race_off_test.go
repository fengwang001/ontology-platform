//go:build !race

package compensate

// raceEnabled 在非竞态构建下恒为 false。
var raceEnabled = false
