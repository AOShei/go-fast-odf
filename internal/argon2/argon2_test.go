package argon2

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// RFC 7693, appendix A: BLAKE2b-512 of "abc".
func TestBlake2bRFC7693(t *testing.T) {
	want := unhex(t, "ba80a53f981c4d0d6a2797b69f12f6e94c212f14685ac4b74b12bb6fdbffa2d1"+
		"7d87c5392aab792dc252d5de4533cc9518d38aa8dbf1925ab92386edd4009923")
	if got := Blake2b512([]byte("abc")); !bytes.Equal(got, want) {
		t.Fatalf("BLAKE2b-512(abc) = %x", got)
	}
}

// Messages that end on, before and after a block boundary hash the same
// fed whole or byte by byte.
func TestBlake2bChunking(t *testing.T) {
	for _, n := range []int{0, 1, 127, 128, 129, 256, 300} {
		msg := bytes.Repeat([]byte{0x5a}, n)
		h := newBlake2b(64)
		for i := range msg {
			h.Write(msg[i : i+1])
		}
		if got, want := h.Sum(), Blake2b512(msg); !bytes.Equal(got, want) {
			t.Errorf("%d bytes: chunked %x, whole %x", n, got, want)
		}
	}
}

// RFC 9106, section 5.3: Argon2id.
func TestArgon2idRFC9106(t *testing.T) {
	password := bytes.Repeat([]byte{1}, 32)
	salt := bytes.Repeat([]byte{2}, 16)
	secret := bytes.Repeat([]byte{3}, 8)
	ad := bytes.Repeat([]byte{4}, 12)
	got, err := IDKey(password, salt, secret, ad, 3, 32, 4, 32)
	if err != nil {
		t.Fatal(err)
	}
	want := unhex(t, "0d640df58d78766c08c037a34a8b53c9d01ef0452d75b65eb52520e96b01e659")
	if !bytes.Equal(got, want) {
		t.Fatalf("tag %x\nwant %x", got, want)
	}
}

// The reference implementation's test (phc-winner-argon2, src/test.c):
// Argon2id, t=2, m=2^16 KiB, p=1, "password", "somesalt". It covers what
// the RFC's small vector does not: segments longer than one address
// block.
func TestArgon2idReference(t *testing.T) {
	if testing.Short() {
		t.Skip("64 MiB")
	}
	got, err := IDKey([]byte("password"), []byte("somesalt"), nil, nil, 2, 1<<16, 1, 32)
	if err != nil {
		t.Fatal(err)
	}
	want := unhex(t, "09316115d5cf24ed5a15a31a3ba326e5cf32edc24702987c02b6566f61913cf7")
	if !bytes.Equal(got, want) {
		t.Fatalf("tag %x\nwant %x", got, want)
	}
}

func TestArgon2idParams(t *testing.T) {
	for _, c := range [][4]uint32{{0, 32, 4, 32}, {1, 31, 4, 32}, {1, 32, 0, 32}, {1, 32, 4, 3}} {
		if _, err := IDKey(nil, nil, nil, nil, c[0], c[1], c[2], c[3]); err == nil {
			t.Errorf("%v: no error", c)
		}
	}
}
