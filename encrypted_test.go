package gofastodf

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// Blowfish CFB (ODF 1.0, 1.1) is not decrypted.
func TestBlowfishUnsupported(t *testing.T) {
	data := readFixture(t, "blowfish-odf11.odt")
	if _, err := extractFile(data, fixturePassword); !errors.Is(err, gofastcore.ErrUnsupported) {
		t.Errorf("with the password: %v", err)
	}
	if _, err := extractFile(data, ""); !errors.Is(err, gofastcore.ErrPasswordRequired) {
		t.Errorf("without a password: %v", err)
	}
}

func TestDecryptCBC(t *testing.T) {
	for _, c := range encCases {
		t.Run(c.file, func(t *testing.T) { checkDecrypted(t, c) })
	}
}
