// Package khash implements the key hash, bucket mapping and sampling
// decision. It depends on nothing else in the module.
package khash

// Buckets is the fixed number of hash buckets; rates are integer
// ten-thousandths in [0, Buckets].
const Buckets = 10000

// Hash folds the UTF-8 bytes of key into a uint64: h starts at 0 and each
// byte b applies h = h*31 + b, with natural uint64 wrap-around.
func Hash(key string) uint64 {
	var h uint64
	for i := 0; i < len(key); i++ {
		h = h*31 + uint64(key[i])
	}
	return h
}

// Bucket maps key into [0, Buckets).
func Bucket(key string) int {
	return int(Hash(key) % Buckets)
}

// Sampled reports whether key is sampled at rate r (r in [0, Buckets]):
// strictly bucket < r, so r == 0 samples nothing and r == Buckets everything.
func Sampled(key string, r int) bool {
	return Bucket(key) < r
}
