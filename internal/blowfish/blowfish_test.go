package blowfish

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// The digits Blowfish's tables are known by: the start of the P-array,
// its last word, the first word of the first S-box and the last word of
// the last.
func TestPiTables(t *testing.T) {
	w := PiWords(words)
	for i, want := range map[int]uint32{0: 0x243f6a88, 1: 0x85a308d3, 2: 0x13198a2e, 3: 0x03707344, 17: 0x8979fb1b, 18: 0xd1310ba6, words - 1: 0x3ac372e6} {
		if w[i] != want {
			t.Errorf("word %d: %08x, want %08x", i, w[i], want)
		}
	}
}

// Schneier's published test vectors (Eric Young's set, vectors.txt).
func TestSchneierVectors(t *testing.T) {
	for _, v := range [][3]string{
		{"0000000000000000", "0000000000000000", "4ef997456198dd78"},
		{"ffffffffffffffff", "ffffffffffffffff", "51866fd5b85ecb8a"},
		{"3000000000000000", "1000000000000001", "7d856f9a613063f2"},
		{"0123456789abcdef", "1111111111111111", "61f9c3802281b096"},
		{"fedcba9876543210", "0123456789abcdef", "0aceab0fc6a0a28d"},
	} {
		key, _ := hex.DecodeString(v[0])
		pt, _ := hex.DecodeString(v[1])
		want, _ := hex.DecodeString(v[2])
		c, err := New(key)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]byte, 8)
		c.Encrypt(got, pt)
		if !bytes.Equal(got, want) {
			t.Errorf("key %s: %x, want %s", v[0], got, v[2])
		}
		c.Decrypt(got, got)
		if !bytes.Equal(got, pt) {
			t.Errorf("key %s: decrypts to %x", v[0], got)
		}
	}
}
