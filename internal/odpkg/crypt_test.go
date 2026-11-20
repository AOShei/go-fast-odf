package odpkg

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/sha1"
	"errors"
	"io"
	"testing"
	"time"
)

const manifestHead = `<?xml version="1.0" encoding="UTF-8"?><manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" xmlns:loext="urn:org:documentfoundation:names:experimental:office:xmlns:loext:1.0">` +
	`<manifest:file-entry manifest:full-path="/" manifest:media-type="application/vnd.oasis.opendocument.text"/>` +
	`<manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml" manifest:size="100">`

// encPackage builds a package whose content.xml is the given bytes and is
// described by the given encryption data.
func encPackage(t testing.TB, encData string, content []byte) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, m := range []struct {
		name string
		data []byte
	}{
		{"mimetype", []byte("application/vnd.oasis.opendocument.text")},
		{"content.xml", content},
		{"META-INF/manifest.xml", []byte(manifestHead + encData + `</manifest:file-entry></manifest:manifest>`)},
	} {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: m.name, Method: zip.Store})
		w.Write(m.data)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func openContent(t testing.TB, data []byte, pw string) error {
	p, err := Open(bytes.NewReader(data), int64(len(data)), Limits{MaxDecodedBytes: 1 << 20, MaxMembers: 16})
	if err != nil {
		return err
	}
	p.SetPassword(pw)
	rc, err := p.Open("content.xml")
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(io.Discard, rc)
	return err
}

const (
	cbcAlg   = `<manifest:algorithm manifest:algorithm-name="http://www.w3.org/2001/04/xmlenc#aes256-cbc" manifest:initialisation-vector="AAAAAAAAAAAAAAAAAAAAAA=="/>`
	sha256SK = `<manifest:start-key-generation manifest:start-key-generation-name="http://www.w3.org/2000/09/xmldsig#sha256" manifest:key-size="32"/>`
	gcmAlg   = `<manifest:algorithm manifest:algorithm-name="http://www.w3.org/2009/xmlenc11#aes256-gcm" manifest:initialisation-vector="AAAAAAAAAAAAAAAA"/>`
	argonLOD = `<manifest:key-derivation manifest:key-derivation-name="urn:org:documentfoundation:names:experimental:office:manifest:argon2id" manifest:salt="AAAAAAAAAAAAAAAAAAAAAA==" manifest:key-size="32" `
)

func cbcData(kd string) string {
	return `<manifest:encryption-data manifest:checksum-type="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0#sha256-1k" manifest:checksum="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=">` +
		cbcAlg + sha256SK + kd + `</manifest:encryption-data>`
}

// What the manifest asks for is bounded before any work is done: each
// case returns at once.
func TestEncryptionLimits(t *testing.T) {
	block := make([]byte, 32)
	cases := []struct {
		name string
		enc  string
		want error
	}{
		{"pbkdf2 iterations", cbcData(`<manifest:key-derivation manifest:key-derivation-name="PBKDF2" manifest:iteration-count="2000000000" manifest:salt="AAAA" manifest:key-size="32"/>`), ErrWork},
		{"argon2 memory", `<manifest:encryption-data>` + gcmAlg + sha256SK + argonLOD + `loext:argon2-iterations="1" loext:argon2-memory="4194304" loext:argon2-lanes="1"/></manifest:encryption-data>`, ErrWork},
		{"argon2 passes", `<manifest:encryption-data>` + gcmAlg + sha256SK + argonLOD + `loext:argon2-iterations="1000000" loext:argon2-memory="64" loext:argon2-lanes="1"/></manifest:encryption-data>`, ErrWork},
		{"argon2 lanes", `<manifest:encryption-data>` + gcmAlg + sha256SK + argonLOD + `manifest:argon2-iterations="1" manifest:argon2-memory="65536" manifest:argon2-lanes="1000"/></manifest:encryption-data>`, ErrWork},
		{"argon2 work", `<manifest:encryption-data>` + gcmAlg + sha256SK + argonLOD + `loext:argon2-iterations="64" loext:argon2-memory="1048576" loext:argon2-lanes="4"/></manifest:encryption-data>`, ErrWork},
		{"unknown algorithm", `<manifest:encryption-data manifest:checksum-type="SHA1/1K" manifest:checksum="AAAA"><manifest:algorithm manifest:algorithm-name="rot13" manifest:initialisation-vector="AAAA"/></manifest:encryption-data>`, ErrCipher},
		{"unknown derivation", cbcData(`<manifest:key-derivation manifest:key-derivation-name="scrypt" manifest:salt="AAAA" manifest:key-size="32"/>`), ErrCipher},
		{"wrong password", cbcData(`<manifest:key-derivation manifest:key-derivation-name="PBKDF2" manifest:iteration-count="10" manifest:salt="AAAA" manifest:key-size="32"/>`), ErrWrongPassword},
	}
	for _, c := range cases {
		start := time.Now()
		err := openContent(t, encPackage(t, c.enc, block), "pw")
		if !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if d := time.Since(start); d > time.Second {
			t.Errorf("%s: took %v", c.name, d)
		}
	}
}

// pbkdf2SHA1 gives what crypto/pbkdf2 gives, for key sizes of one block
// and of several.
func TestPBKDF2MatchesStdlib(t *testing.T) {
	for _, c := range []struct{ iter, size int }{{1, 16}, {2, 20}, {1024, 16}, {1000, 32}, {4097, 56}} {
		want, err := pbkdf2.Key(sha1.New, "start key", []byte("salt"), c.iter, c.size)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pbkdf2SHA1(context.Background(), []byte("start key"), []byte("salt"), c.iter, c.size)
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%d iterations, %d bytes: %x, %v; want %x", c.iter, c.size, got, err, want)
		}
	}
}

// FuzzEncryptionData fuzzes the manifest's encryption data of an
// encrypted member and the member's bytes. The limits on key derivation
// are lowered so that each input is cheap.
func FuzzEncryptionData(f *testing.F) {
	saved := []any{MaxIterations, maxPackageIterations, MaxArgonMemory, MaxArgonIterations, MaxArgonLanes, maxArgonWork, maxPackageArgon}
	MaxIterations, maxPackageIterations = 1000, 3000
	MaxArgonMemory, MaxArgonIterations, MaxArgonLanes, maxArgonWork, maxPackageArgon = 256, 4, 4, 512, 1024
	f.Cleanup(func() {
		MaxIterations, maxPackageIterations = saved[0].(int), saved[1].(int64)
		MaxArgonMemory, MaxArgonIterations, MaxArgonLanes = saved[2].(int), saved[3].(int), saved[4].(int)
		maxArgonWork, maxPackageArgon = saved[5].(int64), saved[6].(int64)
	})
	kd := `<manifest:key-derivation manifest:key-derivation-name="PBKDF2" manifest:iteration-count="100" manifest:salt="AAAA" manifest:key-size="32"/>`
	f.Add(cbcData(kd), make([]byte, 64))
	f.Add(`<manifest:encryption-data>`+gcmAlg+sha256SK+argonLOD+`loext:argon2-iterations="1" loext:argon2-memory="64" loext:argon2-lanes="2"/></manifest:encryption-data>`, make([]byte, 64))
	f.Add(`<manifest:encryption-data manifest:checksum-type="SHA1/1K" manifest:checksum="AAAAAAAAAAAAAAAAAAAAAAAAAAA="><manifest:algorithm manifest:algorithm-name="Blowfish CFB" manifest:initialisation-vector="AAAAAAAAAAA="/>`+kd+`</manifest:encryption-data>`, make([]byte, 40))
	f.Fuzz(func(t *testing.T, enc string, content []byte) {
		if len(enc) > 4096 || len(content) > 1<<16 {
			return
		}
		openContent(t, encPackage(t, enc, content), "pw")
	})
}
