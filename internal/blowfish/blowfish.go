// Package blowfish implements the Blowfish block cipher (Schneier, 1993)
// and its 64-bit CFB mode, for the encrypted packages of ODF 1.0 and 1.1.
//
// The initial P-array and S-boxes are the hexadecimal digits of the
// fractional part of pi. They are not typed into the source: they are
// computed once, on first use, from Machin's formula with math/big, and
// the test checks the result against the digits every description of
// Blowfish starts with and against Schneier's published test vectors.
package blowfish

import (
	"encoding/binary"
	"errors"
	"math/big"
	"sync"
)

// BlockSize is Blowfish's block size in bytes.
const BlockSize = 8

const words = 18 + 4*256 // the P-array and the four S-boxes

var (
	piOnce  sync.Once
	piWords [words]uint32
)

// PiWords returns the first n 32-bit words of the fractional part of pi
// in hexadecimal: 0x243f6a88, 0x85a308d3, ...
func PiWords(n int) []uint32 {
	bitsN := uint(32*n + 64) // guard bits for the rounding of the series
	one := new(big.Int).Lsh(big.NewInt(1), bitsN)
	// pi = 16 arctan(1/5) - 4 arctan(1/239)
	pi := new(big.Int).Mul(arctanInv(5, one), big.NewInt(16))
	pi.Sub(pi, new(big.Int).Mul(arctanInv(239, one), big.NewInt(4)))
	pi.Sub(pi, new(big.Int).Mul(one, big.NewInt(3))) // the fraction
	pi.Rsh(pi, 64)
	b := pi.FillBytes(make([]byte, 4*n))
	out := make([]uint32, n)
	for i := range out {
		out[i] = binary.BigEndian.Uint32(b[4*i:])
	}
	return out
}

// arctanInv returns arctan(1/x) scaled by one.
func arctanInv(x int64, one *big.Int) *big.Int {
	sum := new(big.Int)
	xx := big.NewInt(x * x)
	pow := new(big.Int).Div(one, big.NewInt(x)) // one / x^(2k+1)
	term := new(big.Int)
	for k := int64(0); pow.Sign() != 0; k++ {
		term.Div(pow, big.NewInt(2*k+1))
		if k%2 == 0 {
			sum.Add(sum, term)
		} else {
			sum.Sub(sum, term)
		}
		pow.Div(pow, xx)
	}
	return sum
}

// Cipher is a keyed Blowfish instance.
type Cipher struct {
	p [18]uint32
	s [4][256]uint32
}

// ErrKeySize: the key is not 4 to 56 bytes.
var ErrKeySize = errors.New("blowfish: key must be 4 to 56 bytes")

// New returns a Blowfish cipher for key.
func New(key []byte) (*Cipher, error) {
	if len(key) < 4 || len(key) > 56 {
		return nil, ErrKeySize
	}
	piOnce.Do(func() { copy(piWords[:], PiWords(words)) })
	c := &Cipher{}
	copy(c.p[:], piWords[:18])
	for i := range c.s {
		copy(c.s[i][:], piWords[18+256*i:18+256*(i+1)])
	}
	j := 0
	for i := range c.p {
		var w uint32
		for k := 0; k < 4; k++ {
			w = w<<8 | uint32(key[j])
			j = (j + 1) % len(key)
		}
		c.p[i] ^= w
	}
	var l, r uint32
	for i := 0; i < 18; i += 2 {
		l, r = c.encrypt(l, r)
		c.p[i], c.p[i+1] = l, r
	}
	for b := range c.s {
		for i := 0; i < 256; i += 2 {
			l, r = c.encrypt(l, r)
			c.s[b][i], c.s[b][i+1] = l, r
		}
	}
	return c, nil
}

func (c *Cipher) f(x uint32) uint32 {
	return ((c.s[0][x>>24] + c.s[1][x>>16&0xff]) ^ c.s[2][x>>8&0xff]) + c.s[3][x&0xff]
}

func (c *Cipher) encrypt(l, r uint32) (uint32, uint32) {
	for i := 0; i < 16; i++ {
		l ^= c.p[i]
		r ^= c.f(l)
		l, r = r, l
	}
	l, r = r, l
	r ^= c.p[16]
	l ^= c.p[17]
	return l, r
}

func (c *Cipher) decrypt(l, r uint32) (uint32, uint32) {
	for i := 17; i > 1; i-- {
		l ^= c.p[i]
		r ^= c.f(l)
		l, r = r, l
	}
	l, r = r, l
	r ^= c.p[1]
	l ^= c.p[0]
	return l, r
}

// BlockSize returns 8.
func (c *Cipher) BlockSize() int { return BlockSize }

// Encrypt encrypts the 8-byte block src into dst.
func (c *Cipher) Encrypt(dst, src []byte) {
	l, r := c.encrypt(binary.BigEndian.Uint32(src), binary.BigEndian.Uint32(src[4:]))
	binary.BigEndian.PutUint32(dst, l)
	binary.BigEndian.PutUint32(dst[4:], r)
}

// Decrypt decrypts the 8-byte block src into dst.
func (c *Cipher) Decrypt(dst, src []byte) {
	l, r := c.decrypt(binary.BigEndian.Uint32(src), binary.BigEndian.Uint32(src[4:]))
	binary.BigEndian.PutUint32(dst, l)
	binary.BigEndian.PutUint32(dst[4:], r)
}

// DecryptCFB decrypts ct in CFB mode with 64-bit feedback, as
// OpenOffice.org's rtl cipher does: each byte of keystream comes from
// the encrypted register, and the register takes the ciphertext.
func (c *Cipher) DecryptCFB(iv, ct []byte) []byte {
	reg := make([]byte, BlockSize)
	copy(reg, iv)
	ks := make([]byte, BlockSize)
	out := make([]byte, len(ct))
	for i := range ct {
		k := i % BlockSize
		if k == 0 {
			c.Encrypt(ks, reg)
		}
		out[i] = ct[i] ^ ks[k]
		reg[k] = ct[i]
	}
	return out
}
