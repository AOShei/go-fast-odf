package gofastodf

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AOShei/go-fast-core"
)

// The files in testdata/encrypted were written by LibreOffice 26.8 with
// the password below; their README lists how each is encrypted.
const fixturePassword = "go-fast"

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "encrypted", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func extractFile(data []byte, pw string) (*gofastcore.Document, error) {
	return Extract(context.Background(), bytes.NewReader(data), int64(len(data)), &gofastcore.Options{Password: pw})
}

type encCase struct {
	file   string
	format string
	want   []string
}

var encCases = []encCase{
	{"cbc-pbkdf2-odf12.odt", "odt", []string{"Encrypted text body: the password is go-fast."}},
	{"cbc-pbkdf2-odf13.odt", "odt", []string{"Encrypted text body: the password is go-fast."}},
	{"cbc-pbkdf2-odf13.ods", "ods", []string{"Encrypted cell", "42"}},
	{"cbc-pbkdf2-odf13.odp", "odp", []string{"Encrypted slide text"}},
}

func checkDecrypted(t *testing.T, c encCase) {
	t.Helper()
	data := readFixture(t, c.file)
	doc, err := extractFile(data, fixturePassword)
	if err != nil {
		t.Fatalf("%s: %v", c.file, err)
	}
	if err := doc.Validate(); err != nil {
		t.Fatalf("%s: Validate: %v", c.file, err)
	}
	text := doc.Text()
	for _, w := range c.want {
		if !strings.Contains(text, w) {
			t.Errorf("%s: text lacks %q:\n%s", c.file, w, text)
		}
	}
	if doc.Source.Format != c.format || doc.Source.Size != int64(len(data)) {
		t.Errorf("%s: source %+v", c.file, doc.Source)
	}
	if !doc.Metadata.Encrypted {
		t.Errorf("%s: Metadata.Encrypted not set", c.file)
	}
	var decrypted []string
	for _, w := range doc.Warnings {
		if w.Code == gofastcore.WarnInputDecrypted && w.Severity == gofastcore.SeverityInfo {
			decrypted = append(decrypted, w.Message)
		}
	}
	scheme := "AES-256-CBC with a key by PBKDF2"
	switch {
	case strings.Contains(c.file, "gcm"):
		scheme = "AES-256-GCM with a key by Argon2id"
	case strings.Contains(c.file, "blowfish"):
		scheme = "Blowfish CFB with a key by PBKDF2"
	}
	if len(decrypted) != 1 || !strings.Contains(decrypted[0], scheme) || !strings.Contains(decrypted[0], "password given") {
		t.Errorf("%s: input.decrypted %q, want one naming %q", c.file, decrypted, scheme)
	}
	if _, err := extractFile(data, ""); !errors.Is(err, gofastcore.ErrPasswordRequired) {
		t.Errorf("%s without a password: %v", c.file, err)
	}
	if _, err := extractFile(data, "go-slow"); !errors.Is(err, gofastcore.ErrWrongPassword) {
		t.Errorf("%s with a wrong password: %v", c.file, err)
	}
}

// ODF 1.4: the whole package is encrypted, in one member, with AES-GCM and
// a key from Argon2id (64 MiB, 3 passes, 4 lanes).
func TestDecryptGCMArgon2id(t *testing.T) {
	checkDecrypted(t, encCase{"gcm-argon2id-odf14.odt", "odt", []string{"Encrypted text body: the password is go-fast."}})
}

// ODF 1.0 and 1.1: Blowfish CFB, PBKDF2 from a SHA-1 start key, 16-byte
// key (the default when the manifest names none).
func TestDecryptBlowfish(t *testing.T) {
	checkDecrypted(t, encCase{"blowfish-odf11.odt", "odt", []string{"Encrypted text body: the password is go-fast."}})
}

func TestDecryptCBC(t *testing.T) {
	for _, c := range encCases {
		t.Run(c.file, func(t *testing.T) { checkDecrypted(t, c) })
	}
}

// encryptedODT builds a text package whose content.xml is encrypted as
// encData describes; the encrypted bytes are 32 zero bytes.
func encryptedODT(t *testing.T, encData string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	manifest := `<?xml version="1.0" encoding="UTF-8"?><manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0">` +
		`<manifest:file-entry manifest:full-path="/" manifest:media-type="application/vnd.oasis.opendocument.text"/>` +
		`<manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml" manifest:size="100">` + encData +
		`</manifest:file-entry></manifest:manifest>`
	for _, m := range [][2]string{{"mimetype", "application/vnd.oasis.opendocument.text"}, {"content.xml", string(make([]byte, 32))}, {"META-INF/manifest.xml", manifest}} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: m[0], Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(m[1]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Key derivation honours the context: a file that asks for as much work
// as the limits allow stops when the context is done.
func TestKeyDerivationHonoursContext(t *testing.T) {
	const cbc = `<manifest:encryption-data manifest:checksum-type="SHA1/1K" manifest:checksum="AAAAAAAAAAAAAAAAAAAAAAAAAAA=">` +
		`<manifest:algorithm manifest:algorithm-name="http://www.w3.org/2001/04/xmlenc#aes256-cbc" manifest:initialisation-vector="AAAAAAAAAAAAAAAAAAAAAA=="/>`
	cases := map[string]string{
		"pbkdf2": cbc + `<manifest:key-derivation manifest:key-derivation-name="PBKDF2" manifest:iteration-count="10000000" manifest:salt="AAAA" manifest:key-size="32"/></manifest:encryption-data>`,
		"argon2id": `<manifest:encryption-data><manifest:algorithm manifest:algorithm-name="http://www.w3.org/2009/xmlenc11#aes256-gcm" manifest:initialisation-vector="AAAAAAAAAAAAAAAA"/>` +
			`<manifest:key-derivation manifest:key-derivation-name="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0#argon2id" manifest:salt="AAAAAAAAAAAAAAAAAAAAAA==" manifest:key-size="32" ` +
			`manifest:argon2-iterations="4" manifest:argon2-memory="262144" manifest:argon2-lanes="1"/></manifest:encryption-data>`,
	}
	for name, enc := range cases {
		data := encryptedODT(t, enc)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		start := time.Now()
		_, err := Extract(ctx, bytes.NewReader(data), int64(len(data)), &gofastcore.Options{Password: "pw"})
		cancel()
		if d := time.Since(start); d > 500*time.Millisecond || !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("%s: %v after %v, want the context's error at once", name, err, d)
		}
	}
}
