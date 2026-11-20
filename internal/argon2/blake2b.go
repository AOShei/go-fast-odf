package argon2

import (
	"encoding/binary"
	"math/bits"
)

// BLAKE2b, unkeyed, as RFC 7693 gives it: the hash Argon2 is built on.

var blakeIV = [8]uint64{
	0x6a09e667f3bcc908, 0xbb67ae8584caa73b, 0x3c6ef372fe94f82b, 0xa54ff53a5f1d36f1,
	0x510e527fade682d1, 0x9b05688c2b3e6c1f, 0x1f83d9abfb41bd6b, 0x5be0cd19137e2179,
}

var sigma = [10][16]byte{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15},
	{14, 10, 4, 8, 9, 15, 13, 6, 1, 12, 0, 2, 11, 7, 5, 3},
	{11, 8, 12, 0, 5, 2, 15, 13, 10, 14, 3, 6, 7, 1, 9, 4},
	{7, 9, 3, 1, 13, 12, 11, 14, 2, 6, 5, 10, 4, 0, 15, 8},
	{9, 0, 5, 7, 2, 4, 10, 15, 14, 1, 11, 12, 6, 8, 3, 13},
	{2, 12, 6, 10, 0, 11, 8, 3, 4, 13, 7, 5, 15, 14, 1, 9},
	{12, 5, 1, 15, 14, 13, 4, 10, 0, 7, 6, 3, 9, 2, 8, 11},
	{13, 11, 7, 14, 12, 1, 3, 9, 5, 0, 15, 4, 8, 6, 2, 10},
	{6, 15, 14, 9, 11, 3, 0, 8, 12, 2, 13, 7, 1, 4, 10, 5},
	{10, 2, 8, 4, 7, 6, 1, 5, 15, 11, 9, 14, 3, 12, 13, 0},
}

// blake2b is an unkeyed BLAKE2b state with an output of size bytes (1 to
// 64).
type blake2b struct {
	h    [8]uint64
	t    uint64 // bytes compressed so far (inputs here stay far below 2^64)
	buf  [128]byte
	n    int // bytes in buf
	size int
}

func newBlake2b(size int) *blake2b {
	b := &blake2b{h: blakeIV, size: size}
	b.h[0] ^= 0x01010000 ^ uint64(size)
	return b
}

func (b *blake2b) Write(p []byte) {
	for len(p) > 0 {
		if b.n == len(b.buf) {
			// The buffer is compressed only once more input follows, so
			// that the last block is always the one flagged final.
			b.t += uint64(len(b.buf))
			b.compress(false)
			b.n = 0
		}
		c := copy(b.buf[b.n:], p)
		b.n += c
		p = p[c:]
	}
}

func (b *blake2b) Sum() []byte {
	b.t += uint64(b.n)
	clear(b.buf[b.n:])
	b.compress(true)
	var out [64]byte
	for i, v := range b.h {
		binary.LittleEndian.PutUint64(out[8*i:], v)
	}
	return out[:b.size]
}

func (b *blake2b) compress(last bool) {
	var m [16]uint64
	for i := range m {
		m[i] = binary.LittleEndian.Uint64(b.buf[8*i:])
	}
	var v [16]uint64
	copy(v[:8], b.h[:])
	copy(v[8:], blakeIV[:])
	v[12] ^= b.t
	if last {
		v[14] = ^v[14]
	}
	g := func(a, bb, c, d int, x, y uint64) {
		v[a] += v[bb] + x
		v[d] = bits.RotateLeft64(v[d]^v[a], -32)
		v[c] += v[d]
		v[bb] = bits.RotateLeft64(v[bb]^v[c], -24)
		v[a] += v[bb] + y
		v[d] = bits.RotateLeft64(v[d]^v[a], -16)
		v[c] += v[d]
		v[bb] = bits.RotateLeft64(v[bb]^v[c], -63)
	}
	for r := 0; r < 12; r++ {
		s := &sigma[r%10]
		g(0, 4, 8, 12, m[s[0]], m[s[1]])
		g(1, 5, 9, 13, m[s[2]], m[s[3]])
		g(2, 6, 10, 14, m[s[4]], m[s[5]])
		g(3, 7, 11, 15, m[s[6]], m[s[7]])
		g(0, 5, 10, 15, m[s[8]], m[s[9]])
		g(1, 6, 11, 12, m[s[10]], m[s[11]])
		g(2, 7, 8, 13, m[s[12]], m[s[13]])
		g(3, 4, 9, 14, m[s[14]], m[s[15]])
	}
	for i := range b.h {
		b.h[i] ^= v[i] ^ v[i+8]
	}
}

// Blake2b512 returns the 64-byte BLAKE2b digest of data.
func Blake2b512(data []byte) []byte {
	b := newBlake2b(64)
	b.Write(data)
	return b.Sum()
}
