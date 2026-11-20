package odpkg

import (
	"bytes"
	"compress/flate"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"

	"github.com/AOShei/go-fast-odf/internal/clean"
)

// Errors of decryption. The extractor maps them to the shared errors.
var (
	// ErrWrongPassword: the member's checksum, or its authentication tag,
	// does not match what the password gives.
	ErrWrongPassword = errors.New("odpkg: wrong password")
	// ErrCipher: the member is encrypted in a way this package does not
	// decrypt (an algorithm, key derivation or checksum it does not know).
	ErrCipher = errors.New("odpkg: unsupported encryption")
	// ErrWork: the manifest asks for more key-derivation work than the
	// limits allow.
	ErrWork = errors.New("odpkg: key derivation over the limit")
)

// Limits on the work a manifest may ask for. A real file asks for far
// less: LibreOffice writes 100000 PBKDF2 iterations, and Argon2id with 64
// MiB, 3 passes and 4 lanes.
// They are variables only so that the fuzz test can lower them.
var (
	// MaxIterations is the largest PBKDF2 iteration count accepted for
	// one member.
	MaxIterations = 10_000_000
	// maxPackageIterations bounds the PBKDF2 iterations of all members
	// of a package together.
	maxPackageIterations int64 = 30_000_000
)

// Names the manifest uses. ODF 1.2 part 3, section 3.8 (manifest), and
// what LibreOffice writes.
const (
	nsManifest = "urn:oasis:names:tc:opendocument:xmlns:manifest:1.0#"
	algAES256  = "http://www.w3.org/2001/04/xmlenc#aes256-cbc"
	algAES192  = "http://www.w3.org/2001/04/xmlenc#aes192-cbc"
	algAES128  = "http://www.w3.org/2001/04/xmlenc#aes128-cbc"
	algGCM256  = "http://www.w3.org/2009/xmlenc11#aes256-gcm"
	algGCM192  = "http://www.w3.org/2009/xmlenc11#aes192-gcm"
	algGCM128  = "http://www.w3.org/2009/xmlenc11#aes128-gcm"
	algBlowfsh = "Blowfish CFB"
)

// cipherSpec is what decrypting a member takes, resolved from the manifest.
type cipherSpec struct {
	mode     string // "cbc", "gcm", "blowfish"
	iv       []byte
	checksum []byte
	sum      func() hash.Hash // of the checksum, or nil
	start    func() hash.Hash // of the start key
	argon    bool
	salt     []byte
	iter     int
	keySize  int
	enc      *Encryption
}

func b64(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
}

// resolve checks what the manifest says of a member against what can be
// decrypted.
func resolve(e *Encryption) (*cipherSpec, error) {
	c := cipherSpec{enc: e}
	var err error
	switch e.Algorithm {
	case algAES256, algAES192, algAES128:
		c.mode = "cbc"
	case algGCM256, algGCM192, algGCM128:
		c.mode = "gcm"
	case algBlowfsh:
		c.mode = "blowfish"
	default:
		return nil, fmt.Errorf("%w: algorithm %q", ErrCipher, clean.Label(e.Algorithm))
	}
	if c.iv, err = b64(e.IV); err != nil {
		return nil, fmt.Errorf("%w: initialisation vector is not base64", ErrDamaged)
	}
	switch {
	case c.mode == "cbc" && len(c.iv) != aes.BlockSize, c.mode == "gcm" && len(c.iv) == 0, c.mode == "blowfish" && len(c.iv) != 8:
		return nil, fmt.Errorf("%w: initialisation vector of %d bytes", ErrDamaged, len(c.iv))
	}
	switch e.StartKey {
	case "", "SHA1", "http://www.w3.org/2000/09/xmldsig#sha1":
		c.start = sha1.New
	case "SHA256", "http://www.w3.org/2000/09/xmldsig#sha256", "http://www.w3.org/2001/04/xmlenc#sha256":
		c.start = sha256.New
	default:
		return nil, fmt.Errorf("%w: start key generation %q", ErrCipher, clean.Label(e.StartKey))
	}
	switch e.ChecksumType {
	case "":
		if c.mode != "gcm" {
			return nil, fmt.Errorf("%w: no checksum type", ErrCipher)
		}
	case "SHA1/1K", nsManifest + "sha1-1k":
		c.sum = sha1.New
	case "SHA256/1K", nsManifest + "sha256-1k":
		c.sum = sha256.New
	default:
		return nil, fmt.Errorf("%w: checksum type %q", ErrCipher, clean.Label(e.ChecksumType))
	}
	if c.sum != nil {
		if c.checksum, err = b64(e.Checksum); err != nil || len(c.checksum) == 0 {
			return nil, fmt.Errorf("%w: checksum is not base64", ErrDamaged)
		}
	}
	if c.salt, err = b64(e.Salt); err != nil {
		return nil, fmt.Errorf("%w: salt is not base64", ErrDamaged)
	}
	c.keySize = e.KeySize
	if c.keySize == 0 {
		c.keySize = 16 // the default of ODF 1.2 part 3, section 3.8.6
	}
	switch c.mode {
	case "cbc", "gcm":
		want := map[string]int{algAES256: 32, algAES192: 24, algAES128: 16, algGCM256: 32, algGCM192: 24, algGCM128: 16}[e.Algorithm]
		if e.KeySize == 0 {
			c.keySize = want
		}
		if c.keySize != want {
			return nil, fmt.Errorf("%w: key size %d for %s", ErrDamaged, c.keySize, clean.Label(e.Algorithm))
		}
	case "blowfish":
		if c.keySize < 4 || c.keySize > 56 {
			return nil, fmt.Errorf("%w: Blowfish key size %d", ErrDamaged, c.keySize)
		}
	}
	switch e.Derivation {
	case "PBKDF2", "http://www.w3.org/2009/xmlenc11#pbkdf2":
		c.iter = e.Iterations
		if c.iter < 1 {
			return nil, fmt.Errorf("%w: PBKDF2 iteration count %d", ErrDamaged, c.iter)
		}
		if c.iter > MaxIterations {
			return nil, fmt.Errorf("%w: PBKDF2 iteration count %d, limit %d", ErrWork, c.iter, MaxIterations)
		}
	case argonLO, nsManifest + "argon2id":
		c.argon = true
		if err := checkArgon(e); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("%w: key derivation %q", ErrCipher, clean.Label(e.Derivation))
	}
	return &c, nil
}

const argonLO = "urn:org:documentfoundation:names:experimental:office:manifest:argon2id"

// SetPassword sets the password encrypted members are opened with.
func (p *Package) SetPassword(pw string) { p.password = pw }

// SetContext sets the context key derivation looks at: once it is done,
// deriving a key stops with its error.
func (p *Package) SetContext(ctx context.Context) { p.ctx = ctx }

func (p *Package) context() context.Context {
	if p.ctx == nil {
		return context.Background()
	}
	return p.ctx
}

// key derives the key of a member, charging the work to the package.
func (p *Package) key(c *cipherSpec) ([]byte, error) {
	h := c.start()
	h.Write([]byte(p.password))
	start := h.Sum(nil)
	if c.argon {
		return p.argonKey(c, start)
	}
	if p.kdfWork.Add(int64(c.iter)) > maxPackageIterations {
		return nil, fmt.Errorf("%w: the members ask for more than %d PBKDF2 iterations in all", ErrWork, maxPackageIterations)
	}
	return pbkdf2SHA1(p.context(), start, c.salt, c.iter, c.keySize)
}

// pbkdf2SHA1 is PBKDF2 with HMAC-SHA1 (RFC 8018, section 5.2), as
// crypto/pbkdf2 computes it, but stopping with the context's error once
// ctx is done.
func pbkdf2SHA1(ctx context.Context, password, salt []byte, iter, keyLen int) ([]byte, error) {
	prf := hmac.New(sha1.New, password)
	size := prf.Size()
	var dk, u []byte
	for i := uint32(1); len(dk) < keyLen; i++ {
		prf.Reset()
		prf.Write(salt)
		prf.Write(binary.BigEndian.AppendUint32(nil, i))
		dk = prf.Sum(dk)
		t := dk[len(dk)-size:]
		u = append(u[:0], t...)
		for n := 1; n < iter; n++ {
			if n&0xfff == 0 && ctx.Err() != nil {
				return nil, ctx.Err()
			}
			prf.Reset()
			prf.Write(u)
			u = prf.Sum(u[:0])
			subtle.XORBytes(t, t, u)
		}
	}
	return dk[:keyLen], nil
}

// decrypt returns a member's plaintext: still compressed, as raw DEFLATE.
func (p *Package) decrypt(name string, ct []byte) ([]byte, error) {
	e := p.manifest[name].Encryption
	c, err := resolve(e)
	if err != nil {
		return nil, err
	}
	p.keyMu.Lock()
	key, ok := p.keys[name]
	p.keyMu.Unlock()
	if !ok {
		if key, err = p.key(c); err != nil {
			return nil, err
		}
		p.keyMu.Lock()
		if p.keys == nil {
			p.keys = map[string][]byte{}
		}
		p.keys[name] = key
		p.keyMu.Unlock()
	}
	var pt []byte
	switch c.mode {
	case "cbc":
		if len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
			return nil, fmt.Errorf("%w: %s: encrypted data of %d bytes is not whole blocks", ErrDamaged, clean.Label(name), len(ct))
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCipher, err)
		}
		pt = make([]byte, len(ct))
		cipher.NewCBCDecrypter(block, c.iv).CryptBlocks(pt, ct)
		// W3C padding: the last byte gives the length of the padding;
		// the other padding bytes may be anything.
		pad := int(pt[len(pt)-1])
		if pad < 1 || pad > aes.BlockSize {
			pad = 0
		}
		// For data under 1K, writers differ on whether the digest covers
		// the padding; either is taken.
		if !checksumOK(c, pt) && (pad == 0 || !checksumOK(c, pt[:len(pt)-pad])) {
			return nil, ErrWrongPassword
		}
		if pad == 0 {
			return nil, fmt.Errorf("%w: %s: padding of the encrypted data is not valid", ErrDamaged, clean.Label(name))
		}
		pt = pt[:len(pt)-pad]
	case "gcm":
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCipher, err)
		}
		aead, err := cipher.NewGCMWithNonceSize(block, len(c.iv))
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrCipher, err)
		}
		// LibreOffice writes the nonce again before the ciphertext, as
		// XML Encryption 1.1 does (section 5.2.4); the tag follows it.
		if bytes.HasPrefix(ct, c.iv) {
			ct = ct[len(c.iv):]
		}
		if len(ct) < aead.Overhead() {
			return nil, fmt.Errorf("%w: %s: encrypted data of %d bytes is shorter than its tag", ErrDamaged, clean.Label(name), len(ct))
		}
		pt, err = aead.Open(ct[:0], c.iv, ct, nil)
		if err != nil {
			return nil, ErrWrongPassword
		}
		if c.sum != nil && !checksumOK(c, pt) {
			return nil, ErrWrongPassword
		}
	case "blowfish":
		pt, err = blowfishCFB(key, c.iv, ct)
		if err != nil {
			return nil, err
		}
		if !checksumOK(c, pt) {
			return nil, ErrWrongPassword
		}
	}
	return pt, nil
}

// checksumOK checks the digest of the first 1024 bytes of the decrypted,
// still compressed data against the manifest's.
func checksumOK(c *cipherSpec, pt []byte) bool {
	h := c.sum()
	h.Write(pt[:min(len(pt), 1024)])
	return subtle.ConstantTimeCompare(h.Sum(nil), c.checksum) == 1
}

// openEncrypted decrypts a member and returns a reader of its inflated
// content, charged to the budget as any member's is.
func (p *Package) openEncrypted(name string) (io.ReadCloser, error) {
	f := p.files[name]
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrDamaged, clean.Label(name), err)
	}
	var src io.Reader = rc
	limit := p.Remaining() + int64(f.CompressedSize64)
	if f.Method != 0 {
		// Compressed by the archive as well (not what a writer does):
		// what comes out is charged like any decompressed data, and
		// ciphertext, which does not compress, is no larger than the
		// package that holds it.
		src = &budgetReader{rc: rc, p: p}
		limit = min(p.Remaining(), p.size)
	}
	ct, err := io.ReadAll(io.LimitReader(src, limit+1))
	rc.Close()
	if err != nil {
		if errors.Is(err, ErrBudget) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s: %v", ErrDamaged, clean.Label(name), err)
	}
	if f.Method != 0 && int64(len(ct)) > p.size {
		return nil, fmt.Errorf("%w: %s: compressed encrypted data inflates past the %d bytes of the package", ErrDamaged, clean.Label(name), p.size)
	}
	pt, err := p.decrypt(name, ct)
	if err != nil {
		return nil, err
	}
	return &budgetReader{rc: io.NopCloser(flate.NewReader(bytes.NewReader(pt))), p: p}, nil
}

// Check decrypts a member to see whether the password opens it, without
// reading it.
func (p *Package) Check(name string) error {
	rc, err := p.Open(name)
	if err != nil {
		return err
	}
	return rc.Close()
}

// ReadEncryptedPackage decrypts and inflates the member that holds a whole
// package (ODF 1.4's encrypted-package), and returns its bytes. They are
// charged to the budget.
func (p *Package) ReadEncryptedPackage(name string) ([]byte, error) {
	rc, err := p.Open(name)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		if errors.Is(err, ErrBudget) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %s: %v", ErrDamaged, clean.Label(name), err)
	}
	return b, nil
}
