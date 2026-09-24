// Package oms reads and writes OpenSSF Model Signing (OMS) v1.0 bundles for
// kept revisions: a Sigstore bundle holding a DSSE envelope whose payload is
// an in-toto statement listing every file's SHA-256.
//
// Only the "key" signing method is implemented (ECDSA P-256/384/521), which
// needs no network or Sigstore infrastructure. Bundles verify with the
// reference implementation (`model_signing verify key`) against a
// directory written by `weightkeep export --to`, and bundles it produces
// verify here. Spec: https://github.com/ossf/model-signing-spec (v1.0).
package oms

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"hash"
	"sort"
	"strings"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

const (
	PredicateType = "https://model_signing/signature/v1.0"
	StatementType = "https://in-toto.io/Statement/v1"
	PayloadType   = "application/vnd.in-toto+json"
	MediaType     = "application/vnd.dev.sigstore.bundle.v0.3+json"
)

// defaultIgnored are excluded from every OMS manifest (spec §6.2), matched
// as top-level path components.
var defaultIgnored = []string{".git", ".gitignore", ".gitattributes", ".github"}

// Statement is the in-toto statement an OMS bundle signs.
type Statement struct {
	Type          string    `json:"_type"`
	Subject       []Subject `json:"subject"`
	PredicateType string    `json:"predicateType"`
	Predicate     Predicate `json:"predicate"`
}

type Subject struct {
	Name   string            `json:"name"`
	Digest map[string]string `json:"digest"`
}

type Predicate struct {
	Resources     []Resource    `json:"resources"`
	Serialization Serialization `json:"serialization"`
}

type Resource struct {
	Name      string `json:"name"`
	Digest    string `json:"digest"`
	Algorithm string `json:"algorithm"`
}

type Serialization struct {
	Method        string   `json:"method"`
	HashType      string   `json:"hash_type"`
	ShardSize     int64    `json:"shard_size,omitempty"`
	AllowSymlinks bool     `json:"allow_symlinks"`
	IgnorePaths   []string `json:"ignore_paths,omitempty"`
}

// ignored reports whether a repo path falls under a default exclusion.
func ignored(path string) bool {
	top, _, _ := strings.Cut(path, "/")
	for _, d := range defaultIgnored {
		if top == d {
			return true
		}
	}
	return false
}

// FromManifest builds the statement for a kept revision. name is the
// subject name, usually the directory the revision is exported to.
func FromManifest(m *manifest.Manifest, name string) (*Statement, error) {
	var res []Resource
	for _, f := range m.Files {
		if ignored(f.Path) {
			continue
		}
		if f.SHA256 == "" {
			return nil, fmt.Errorf("%s has no SHA-256 (not kept?)", f.Path)
		}
		res = append(res, Resource{Name: f.Path, Digest: f.SHA256, Algorithm: "sha256"})
	}
	if len(res) == 0 {
		return nil, errors.New("nothing to sign: the revision has no files outside the default exclusions")
	}
	// Code point order, which is byte order for UTF-8.
	sort.Slice(res, func(i, j int) bool { return res[i].Name < res[j].Name })
	root, err := RootDigest(res)
	if err != nil {
		return nil, err
	}
	return &Statement{
		Type:          StatementType,
		Subject:       []Subject{{Name: name, Digest: map[string]string{"sha256": root}}},
		PredicateType: PredicateType,
		Predicate: Predicate{
			Resources: res,
			Serialization: Serialization{
				Method: "files", HashType: "sha256", AllowSymlinks: false,
				IgnorePaths: append([]string(nil), defaultIgnored...),
			},
		},
	}, nil
}

// RootDigest is SHA-256 over the concatenated raw resource digests (§6.5.1).
func RootDigest(res []Resource) (string, error) {
	h := sha256.New()
	for _, r := range res {
		b, err := hex.DecodeString(r.Digest)
		if err != nil {
			return "", fmt.Errorf("digest of %s: %w", r.Name, err)
		}
		h.Write(b)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Bundle is the Sigstore bundle wrapping the signed statement.
type Bundle struct {
	MediaType            string               `json:"mediaType"`
	VerificationMaterial VerificationMaterial `json:"verificationMaterial"`
	DSSEEnvelope         Envelope             `json:"dsseEnvelope"`
}

type VerificationMaterial struct {
	PublicKey   *PublicKeyIdentifier `json:"publicKey,omitempty"`
	TlogEntries []json.RawMessage    `json:"tlogEntries"`
}

type PublicKeyIdentifier struct {
	Hint     string `json:"hint,omitempty"`
	RawBytes string `json:"rawBytes,omitempty"` // bundles from model_signing before 1.1
}

type Envelope struct {
	Payload     string      `json:"payload"`
	PayloadType string      `json:"payloadType"`
	Signatures  []Signature `json:"signatures"`
}

type Signature struct {
	Sig   string  `json:"sig"`
	KeyID *string `json:"keyid"`
}

// pae is DSSE's pre-authentication encoding, which is what gets signed.
func pae(payloadType string, payload []byte) []byte {
	return []byte(fmt.Sprintf("DSSEv1 %d %s %d %s", len(payloadType), payloadType, len(payload), payload))
}

// curveHash picks the hash ECDSA uses for a curve, as model_signing does.
func curveHash(c elliptic.Curve) (func() hash.Hash, error) {
	switch c {
	case elliptic.P256():
		return sha256.New, nil
	case elliptic.P384():
		return sha512.New384, nil
	case elliptic.P521():
		return sha512.New, nil
	}
	return nil, errors.New("unsupported curve: OMS keys are P-256, P-384 or P-521")
}

// KeyHint is the key fingerprint OMS bundles carry: SHA-256 of the public
// key's PEM (SubjectPublicKeyInfo) encoding.
func KeyHint(pub *ecdsa.PublicKey) (string, error) {
	p, err := PublicKeyPEM(pub)
	if err != nil {
		return "", err
	}
	s := sha256.Sum256(p)
	return hex.EncodeToString(s[:]), nil
}

// Sign wraps st in a signed bundle.
func Sign(st *Statement, key *ecdsa.PrivateKey) ([]byte, error) {
	payload, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	newHash, err := curveHash(key.Curve)
	if err != nil {
		return nil, err
	}
	h := newHash()
	h.Write(pae(PayloadType, payload))
	sig, err := ecdsa.SignASN1(rand.Reader, key, h.Sum(nil))
	if err != nil {
		return nil, err
	}
	hint, err := KeyHint(&key.PublicKey)
	if err != nil {
		return nil, err
	}
	empty := ""
	b := Bundle{
		MediaType:            MediaType,
		VerificationMaterial: VerificationMaterial{PublicKey: &PublicKeyIdentifier{Hint: hint}, TlogEntries: []json.RawMessage{}},
		DSSEEnvelope: Envelope{
			Payload:     base64.StdEncoding.EncodeToString(payload),
			PayloadType: PayloadType,
			Signatures:  []Signature{{Sig: base64.StdEncoding.EncodeToString(sig), KeyID: &empty}},
		},
	}
	return json.MarshalIndent(b, "", "  ")
}

// Verify checks a bundle's signature with pub and returns the statement.
func Verify(bundle []byte, pub *ecdsa.PublicKey) (*Statement, error) {
	var b Bundle
	if err := json.Unmarshal(bundle, &b); err != nil {
		return nil, fmt.Errorf("read bundle: %w", err)
	}
	if !strings.HasPrefix(b.MediaType, "application/vnd.dev.sigstore.bundle") {
		return nil, fmt.Errorf("not a Sigstore bundle (mediaType %q)", b.MediaType)
	}
	if b.VerificationMaterial.PublicKey == nil {
		return nil, errors.New("bundle isn't signed with a key (certificate and Sigstore bundles aren't supported)")
	}
	if hint := b.VerificationMaterial.PublicKey.Hint; hint != "" {
		want, err := KeyHint(pub)
		if err != nil {
			return nil, err
		}
		if hint != want {
			return nil, errors.New("the bundle was signed with a different key")
		}
	}
	env := b.DSSEEnvelope
	if env.PayloadType != PayloadType || len(env.Signatures) != 1 {
		return nil, errors.New("unexpected DSSE envelope")
	}
	payload, err := base64.StdEncoding.DecodeString(env.Payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	sig, err := base64.StdEncoding.DecodeString(env.Signatures[0].Sig)
	if err != nil {
		return nil, fmt.Errorf("signature: %w", err)
	}
	newHash, err := curveHash(pub.Curve)
	if err != nil {
		return nil, err
	}
	h := newHash()
	h.Write(pae(env.PayloadType, payload))
	if !ecdsa.VerifyASN1(pub, h.Sum(nil), sig) {
		return nil, errors.New("signature does not verify")
	}

	var st Statement
	if err := json.Unmarshal(payload, &st); err != nil {
		return nil, fmt.Errorf("statement: %w", err)
	}
	if st.Type != StatementType || st.PredicateType != PredicateType {
		return nil, fmt.Errorf("statement type %q / predicate %q is not OMS v1.0", st.Type, st.PredicateType)
	}
	s := st.Predicate.Serialization
	if s.Method != "files" || s.HashType != "sha256" {
		return nil, fmt.Errorf("only files/sha256 serialization is supported, got %s/%s", s.Method, s.HashType)
	}
	return &st, nil
}

// Mismatch is one difference between a statement and a manifest.
type Mismatch struct {
	Path   string
	Reason string
}

// Compare checks every signed resource against the manifest, and that the
// manifest has no unsigned files outside the exclusions.
func Compare(st *Statement, m *manifest.Manifest) []Mismatch {
	var out []Mismatch
	ignore := map[string]bool{}
	for _, p := range st.Predicate.Serialization.IgnorePaths {
		ignore[p] = true
	}
	isIgnored := func(p string) bool {
		if ignored(p) {
			return true
		}
		for q := range ignore {
			if p == q || strings.HasPrefix(p, q+"/") {
				return true
			}
		}
		return false
	}
	signed := map[string]string{}
	for _, r := range st.Predicate.Resources {
		signed[r.Name] = r.Digest
	}
	for _, f := range m.Files {
		want, ok := signed[f.Path]
		switch {
		case isIgnored(f.Path):
		case !ok:
			out = append(out, Mismatch{f.Path, "not signed"})
		case want != f.SHA256:
			out = append(out, Mismatch{f.Path, "SHA-256 differs from the signed one"})
		}
		delete(signed, f.Path)
	}
	for p := range signed {
		out = append(out, Mismatch{p, "signed but not in the revision"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// GenerateKey makes a P-256 key.
func GenerateKey() (*ecdsa.PrivateKey, error) {
	return ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
}

// PrivateKeyPEM encodes a key as PKCS #8 PEM.
func PrivateKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// PublicKeyPEM encodes a public key as SubjectPublicKeyInfo PEM, the same
// bytes OpenSSL and Python's cryptography produce.
func PublicKeyPEM(pub *ecdsa.PublicKey) ([]byte, error) {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), nil
}

// ParsePrivateKey reads a PKCS #8 or SEC 1 ("EC PRIVATE KEY") PEM key.
func ParsePrivateKey(data []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block in private key file")
	}
	if k, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	ec, ok := k.(*ecdsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not an elliptic curve key")
	}
	if _, err := curveHash(ec.Curve); err != nil {
		return nil, err
	}
	return ec, nil
}

// ParsePublicKey reads a SubjectPublicKeyInfo PEM key.
func ParsePublicKey(data []byte) (*ecdsa.PublicKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("no PEM block in public key file")
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	ec, ok := k.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("public key is not an elliptic curve key")
	}
	if _, err := curveHash(ec.Curve); err != nil {
		return nil, err
	}
	return ec, nil
}

// VerifyDir checks the files in dir against a verified statement, as
// `model_signing verify` does: every signed file must match, and every file
// outside the exclusions (and the bundle itself, bundleName) must be signed.
func VerifyDir(st *Statement, dir, bundleName string) ([]Mismatch, error) {
	sums := map[string]string{}
	err := walkFiles(dir, func(rel string, sum string) {
		sums[rel] = sum
	})
	if err != nil {
		return nil, err
	}
	m := &manifest.Manifest{}
	for p, s := range sums {
		if p == bundleName {
			continue
		}
		m.Files = append(m.Files, manifest.File{Path: p, SHA256: s})
	}
	return Compare(st, m), nil
}
