# Encrypted ODF fixtures

Written on 2026-11-20 by LibreOffice 26.8.2.2 through its UNO API (`storeToURL` with `Password`), from content made for these tests. Password for all: `go-fast`.

| File | Manifest version | Cipher | Key derivation | Start key | Checksum |
|---|---|---|---|---|---|
| `cbc-pbkdf2-odf12.odt` | 1.2 | AES-256-CBC | PBKDF2, 100000 iterations | SHA-256 | SHA-256 of the first 1K |
| `cbc-pbkdf2-odf13.odt`, `.ods`, `.odp` | 1.3 | AES-256-CBC | PBKDF2, 100000 iterations | SHA-256 | SHA-256 of the first 1K |
| `gcm-argon2id-odf14.odt` | 1.4 | AES-256-GCM | Argon2id (LibreOffice's experimental name) | SHA-256 | none (the GCM tag) |
| `blowfish-odf11.odt` | none (ODF 1.0/1.1) | Blowfish CFB | PBKDF2 | SHA-1 (the default; not named) | SHA-1 of the first 1K |

Text in them:
- `.odt`: "Encrypted text body: the password is go-fast."
- `.ods`: "Encrypted cell" in A1, 42 in B1.
- `.odp`: "Encrypted slide text".
