// Package hashk 定义环空间 [0, 2^32) 上的散列与虚节点位置计算。
// 不依赖其他包。
package hashk

// mult 是奇数，故 H 是 [0, 2^32) 上的双射（uint32 乘法自然回绕即 mod 2^32）。
const mult uint32 = 2654435761

// H 返回 x 在环上的散列位置：H(x) = x · 2654435761 (mod 2^32)。
func H(x uint32) uint32 { return x * mult }

// VNodePos 返回节点 nodeID 的第 i 个虚节点位置：H(nodeID·256 + i)。
func VNodePos(nodeID, i uint32) uint32 { return H(nodeID*256 + i) }

// KeyPos 返回键在环上的位置：H(key)。
func KeyPos(key uint32) uint32 { return H(key) }
