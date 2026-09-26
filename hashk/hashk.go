// Package hashk defines the fixed ring hash H and virtual-node placement.
// It depends on no other package.
package hashk

// multiplier is the odd Knuth-style constant; multiplication wraps mod 2^32.
const multiplier uint32 = 2654435761

// H returns x*2654435761 as uint32; natural wrap is mod 2^32.
// The multiplier is odd, so H is a bijection on [0,2^32).
func H(x uint32) uint32 {
	return x * multiplier
}

// VNodePos returns H(nodeID*256+i), the ring position of node's i-th vnode.
// nodeID*256+i is computed in uint32, so it wraps exactly as specified.
func VNodePos(nodeID uint32, i int) uint32 {
	return H(nodeID*256 + uint32(i))
}
