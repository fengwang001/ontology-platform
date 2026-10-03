package cred

// ResetProbesForTest 将按 keyID 查表计数清零（仅供测试使用）。
func ResetProbesForTest() { credProbes.Store(0) }

// ProbesForTest 返回自上次清零以来的查表次数（仅供测试使用）。
func ProbesForTest() int64 { return credProbes.Load() }
