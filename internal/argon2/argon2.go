// Package argon2 implements Argon2id (RFC 9106, version 0x13) and the
// BLAKE2b it is built on (RFC 7693), for deriving the keys of encrypted
// OpenDocument packages. It is written from the RFCs and checked against
// their test vectors.
package argon2

import (
	"context"
	"encoding/binary"
	"errors"
	"math/bits"
)

const (
	version   = 0x13
	modeID    = 2
	syncPts   = 4   // slices per pass
	blockLen  = 128 // uint64 words in a 1024-byte block
	maxLanes  = 1<<24 - 1
	minMemory = 8 // KiB per lane, at least
)

type block [blockLen]uint64

// ErrParams: the parameters are outside what RFC 9106 allows.
var ErrParams = errors.New("argon2: parameters out of range")

// IDKey derives a key of keyLen bytes with Argon2id: time passes over
// memory KiB in lanes lanes, with optional secret and associated data.
// The caller bounds memory, time and lanes; this function allocates
// memory KiB.
func IDKey(password, salt, secret, ad []byte, time, memory, lanes, keyLen uint32) ([]byte, error) {
	return IDKeyContext(context.Background(), password, salt, secret, ad, time, memory, lanes, keyLen)
}

// IDKeyContext is IDKey that stops, with the context's error, once ctx is
// done. It looks at ctx every checkEvery blocks: a segment can be tens of
// thousands of blocks, too long to wait for under a slow build such as
// the race detector's.
func IDKeyContext(ctx context.Context, password, salt, secret, ad []byte, time, memory, lanes, keyLen uint32) ([]byte, error) {
	if time < 1 || lanes < 1 || lanes > maxLanes || keyLen < 4 || memory < minMemory*lanes {
		return nil, ErrParams
	}
	h0 := initHash(password, salt, secret, ad, time, memory, lanes, keyLen)
	m := memory / (syncPts * lanes) * (syncPts * lanes)
	q := m / lanes
	seg := q / syncPts
	B := make([]block, m)

	var in [72]byte
	copy(in[:64], h0)
	for l := uint32(0); l < lanes; l++ {
		binary.LittleEndian.PutUint32(in[68:], l)
		for j := uint32(0); j < 2; j++ {
			binary.LittleEndian.PutUint32(in[64:], j)
			buf := hashLong(in[:], 1024)
			for k := range B[l*q+j] {
				B[l*q+j][k] = binary.LittleEndian.Uint64(buf[8*k:])
			}
		}
	}

	for n := uint32(0); n < time; n++ {
		for slice := uint32(0); slice < syncPts; slice++ {
			for lane := uint32(0); lane < lanes; lane++ {
				if err := segment(ctx, B, n, slice, lane, lanes, q, seg, m, time); err != nil {
					return nil, err
				}
			}
		}
	}

	var c block
	for l := uint32(0); l < lanes; l++ {
		last := &B[l*q+q-1]
		for k := range c {
			c[k] ^= last[k]
		}
	}
	var cb [1024]byte
	for k, v := range c {
		binary.LittleEndian.PutUint64(cb[8*k:], v)
	}
	return hashLong(cb[:], keyLen), nil
}

// checkEvery is how many blocks are made between looks at the context:
// a look costs next to nothing beside a thousand compressions.
const checkEvery = 1 << 10

func segment(ctx context.Context, B []block, n, slice, lane, lanes, q, seg, m, time uint32) error {
	var addr, in, zero block
	independent := n == 0 && slice < syncPts/2
	if independent {
		in[0], in[1], in[2], in[3], in[4], in[5] = uint64(n), uint64(lane), uint64(slice), uint64(m), uint64(time), modeID
	}
	index := uint32(0)
	if n == 0 && slice == 0 {
		index = 2 // the first two blocks of each lane are made from H0
		if independent {
			in[6]++
			compress(&addr, &in, &zero, false)
			compress(&addr, &addr, &zero, false)
		}
	}
	offset := lane*q + slice*seg + index
	for ; index < seg; index, offset = index+1, offset+1 {
		if index%checkEvery == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		prev := offset - 1
		if index == 0 && slice == 0 {
			prev += q // the lane's last block
		}
		var rand uint64
		if independent {
			if index%blockLen == 0 {
				in[6]++
				compress(&addr, &in, &zero, false)
				compress(&addr, &addr, &zero, false)
			}
			rand = addr[index%blockLen]
		} else {
			rand = B[prev][0]
		}
		ref := refIndex(rand, q, seg, lanes, n, slice, lane, index)
		compress(&B[offset], &B[prev], &B[ref], n > 0)
	}
	return nil
}

// refIndex maps a pseudo-random word to the block referenced, as RFC
// 9106 section 3.4.1.2 says.
func refIndex(rand uint64, q, seg, lanes, n, slice, lane, index uint32) uint32 {
	refLane := uint32(rand>>32) % lanes
	if n == 0 && slice == 0 {
		refLane = lane
	}
	// The size of the reference set.
	var area uint32
	var start uint32
	if n == 0 {
		area = slice * seg
		if refLane == lane {
			area += index - 1
		} else if index == 0 {
			area--
		}
	} else {
		area = q - seg
		if refLane == lane {
			area += index - 1
		} else if index == 0 {
			area--
		}
		if slice != syncPts-1 {
			start = (slice + 1) * seg
		}
	}
	x := rand & 0xffffffff
	y := (x * x) >> 32
	z := uint64(area) - 1 - (uint64(area)*y)>>32
	return refLane*q + uint32((uint64(start)+z)%uint64(q))
}

// compress is the compression function G: out = P(x ^ y) ^ x ^ y, or,
// with xor, out ^= that.
func compress(out, x, y *block, xor bool) {
	var r, z block
	for i := range r {
		r[i] = x[i] ^ y[i]
	}
	z = r
	for i := 0; i < blockLen; i += 16 {
		blamka(&z[i], &z[i+1], &z[i+2], &z[i+3], &z[i+4], &z[i+5], &z[i+6], &z[i+7],
			&z[i+8], &z[i+9], &z[i+10], &z[i+11], &z[i+12], &z[i+13], &z[i+14], &z[i+15])
	}
	for i := 0; i < 16; i += 2 {
		blamka(&z[i], &z[i+1], &z[i+16], &z[i+17], &z[i+32], &z[i+33], &z[i+48], &z[i+49],
			&z[i+64], &z[i+65], &z[i+80], &z[i+81], &z[i+96], &z[i+97], &z[i+112], &z[i+113])
	}
	if xor {
		for i := range out {
			out[i] ^= z[i] ^ r[i]
		}
		return
	}
	for i := range out {
		out[i] = z[i] ^ r[i]
	}
}

func blamka(v0, v1, v2, v3, v4, v5, v6, v7, v8, v9, v10, v11, v12, v13, v14, v15 *uint64) {
	gb(v0, v4, v8, v12)
	gb(v1, v5, v9, v13)
	gb(v2, v6, v10, v14)
	gb(v3, v7, v11, v15)
	gb(v0, v5, v10, v15)
	gb(v1, v6, v11, v12)
	gb(v2, v7, v8, v13)
	gb(v3, v4, v9, v14)
}

func gb(a, b, c, d *uint64) {
	fb := func(x, y uint64) uint64 { return x + y + 2*(x&0xffffffff)*(y&0xffffffff) }
	*a = fb(*a, *b)
	*d = bits.RotateLeft64(*d^*a, -32)
	*c = fb(*c, *d)
	*b = bits.RotateLeft64(*b^*c, -24)
	*a = fb(*a, *b)
	*d = bits.RotateLeft64(*d^*a, -16)
	*c = fb(*c, *d)
	*b = bits.RotateLeft64(*b^*c, -63)
}

func le32(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

func initHash(password, salt, secret, ad []byte, time, memory, lanes, keyLen uint32) []byte {
	h := newBlake2b(64)
	for _, v := range []uint32{lanes, keyLen, memory, time, version, modeID} {
		h.Write(le32(v))
	}
	for _, b := range [][]byte{password, salt, secret, ad} {
		h.Write(le32(uint32(len(b))))
		h.Write(b)
	}
	return h.Sum()
}

// hashLong is the variable-length hash H' of RFC 9106 section 3.3.
func hashLong(in []byte, size uint32) []byte {
	if size <= 64 {
		h := newBlake2b(int(size))
		h.Write(le32(size))
		h.Write(in)
		return h.Sum()
	}
	out := make([]byte, 0, size)
	h := newBlake2b(64)
	h.Write(le32(size))
	h.Write(in)
	v := h.Sum()
	r := (size+31)/32 - 2
	out = append(out, v[:32]...)
	for i := uint32(1); i < r; i++ {
		v = Blake2b512(v)
		out = append(out, v[:32]...)
	}
	h = newBlake2b(int(size - 32*r))
	h.Write(v)
	return append(out, h.Sum()...)
}
