package odpkg

import (
	"fmt"

	"github.com/AOShei/go-fast-odf/internal/argon2"
	"github.com/AOShei/go-fast-odf/internal/blowfish"
)

// Limits on Argon2id. Memory is in KiB, as the manifest gives it; the
// work of a derivation is its memory times its passes. They are variables
// only so that the fuzz test can lower them.
var (
	MaxArgonMemory           = 1 << 20 // 1 GiB
	MaxArgonIterations       = 64
	MaxArgonLanes            = 64
	maxArgonWork       int64 = 4 << 20 // KiB-passes: 1 GiB four times over
	maxPackageArgon    int64 = 8 << 20
)

// checkArgon checks the Argon2id parameters of a manifest entry.
func checkArgon(e *Encryption) error {
	switch {
	case e.ArgonMemory < 1 || e.ArgonIterates < 1 || e.ArgonLanes < 1 || e.ArgonMemory < 8*e.ArgonLanes:
		return fmt.Errorf("%w: Argon2id parameters: %d KiB, %d passes, %d lanes", ErrDamaged, e.ArgonMemory, e.ArgonIterates, e.ArgonLanes)
	case e.ArgonMemory > MaxArgonMemory:
		return fmt.Errorf("%w: Argon2id memory %d KiB, limit %d", ErrWork, e.ArgonMemory, MaxArgonMemory)
	case e.ArgonIterates > MaxArgonIterations:
		return fmt.Errorf("%w: Argon2id passes %d, limit %d", ErrWork, e.ArgonIterates, MaxArgonIterations)
	case e.ArgonLanes > MaxArgonLanes:
		return fmt.Errorf("%w: Argon2id lanes %d, limit %d", ErrWork, e.ArgonLanes, MaxArgonLanes)
	case int64(e.ArgonMemory)*int64(e.ArgonIterates) > maxArgonWork:
		return fmt.Errorf("%w: Argon2id memory times passes is %d KiB, limit %d", ErrWork, int64(e.ArgonMemory)*int64(e.ArgonIterates), maxArgonWork)
	}
	return nil
}

// argonKey derives a key with Argon2id from the start key.
func (p *Package) argonKey(c *cipherSpec, start []byte) ([]byte, error) {
	e := c.enc
	if p.argonWork.Add(int64(e.ArgonMemory)*int64(e.ArgonIterates)) > maxPackageArgon {
		return nil, fmt.Errorf("%w: the members ask for more Argon2id work than %d KiB-passes in all", ErrWork, maxPackageArgon)
	}
	k, err := argon2.IDKey(start, c.salt, nil, nil, uint32(e.ArgonIterates), uint32(e.ArgonMemory), uint32(e.ArgonLanes), uint32(c.keySize))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
	}
	return k, nil
}

// blowfishCFB decrypts Blowfish in 64-bit CFB mode.
func blowfishCFB(key, iv, ct []byte) ([]byte, error) {
	c, err := blowfish.New(key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDamaged, err)
	}
	return c.DecryptCFB(iv, ct), nil
}
