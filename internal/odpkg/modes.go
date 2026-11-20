package odpkg

import "fmt"

// checkArgon checks the Argon2id parameters of a manifest entry.
func checkArgon(e *Encryption) error {
	return fmt.Errorf("%w: Argon2id key derivation is not implemented", ErrCipher)
}

// argonKey derives a key with Argon2id.
func (p *Package) argonKey(c *cipherSpec, start []byte) ([]byte, error) {
	return nil, fmt.Errorf("%w: Argon2id key derivation is not implemented", ErrCipher)
}

// blowfishCFB decrypts Blowfish in 64-bit CFB mode.
func blowfishCFB(key, iv, ct []byte) ([]byte, error) {
	return nil, fmt.Errorf("%w: Blowfish CFB (ODF 1.0 and 1.1) is not implemented", ErrCipher)
}
