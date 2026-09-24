package oms

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
	"github.com/afshinghezeli/weightkeep/internal/testutil"
)

func sampleManifest() *manifest.Manifest {
	return &manifest.Manifest{
		Version: manifest.FormatVersion, Repo: manifest.Repo{Type: "model", ID: "acme/tiny"},
		Commit: strings.Repeat("a", 40), FetchedAt: time.Now(),
		Files: []manifest.File{
			{Path: ".gitattributes", Size: 1, SHA256: strings.Repeat("0", 64), GitSHA1: strings.Repeat("0", 40)},
			{Path: "weights.bin", Size: 1, SHA256: "4555555dc68d872c2270ba89ecc5f6f094812f65372b37e50071fe5168031c49", GitSHA1: strings.Repeat("1", 40), LFS: true},
			{Path: "config.json", Size: 1, SHA256: "5e472403951781d18d5f790aa5b3316a5f535fc37a1052f6659c9c6af82e3643", GitSHA1: strings.Repeat("2", 40)},
		},
	}
}

// Resources and root digest from the spec's test vector
// test-vectors/v1.0/valid/key.bundle.json. (The spec's Appendix A pairs
// other file digests with this same root digest; the vector is the one
// that's consistent.)
func TestRootDigestMatchesSpecVector(t *testing.T) {
	got, err := RootDigest([]Resource{
		{Name: "signme-1", Digest: "bc9a717db6064a32cbe9eae6fc40651dd6d36ef1757183f590eff2b1765d7ebc"},
		{Name: "signme-2", Digest: "32dcb1f8e1ae52d5963e56a6ba06ab58b424c15ed2a6b70d2feca1d089370028"},
	})
	if err != nil || got != "92745110b1ab4368471cbe31664e10174e954b113a3df333db860317b2c6dec4" {
		t.Errorf("root digest = %s, %v", got, err)
	}
}

func TestFromManifestExcludesAndSorts(t *testing.T) {
	st, err := FromManifest(sampleManifest(), "my-model")
	if err != nil {
		t.Fatal(err)
	}
	names := []string{st.Predicate.Resources[0].Name, st.Predicate.Resources[1].Name}
	if len(st.Predicate.Resources) != 2 || names[0] != "config.json" || names[1] != "weights.bin" {
		t.Errorf("resources = %v; .gitattributes must be excluded and the rest sorted", names)
	}
}

func TestSignVerifyRoundTrip(t *testing.T) {
	for _, curve := range []elliptic.Curve{elliptic.P256(), elliptic.P384(), elliptic.P521()} {
		t.Run(curve.Params().Name, func(t *testing.T) {
			key, err := ecdsa.GenerateKey(curve, rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			st, _ := FromManifest(sampleManifest(), "tiny")
			bundle, err := Sign(st, key)
			if err != nil {
				t.Fatal(err)
			}
			got, err := Verify(bundle, &key.PublicKey)
			if err != nil {
				t.Fatal(err)
			}
			if mm := Compare(got, sampleManifest()); len(mm) != 0 {
				t.Errorf("mismatches against the signed manifest: %+v", mm)
			}

			other, _ := ecdsa.GenerateKey(curve, rand.Reader)
			if _, err := Verify(bundle, &other.PublicKey); err == nil {
				t.Error("verified with the wrong key")
			}
		})
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	key, _ := GenerateKey()
	st, _ := FromManifest(sampleManifest(), "tiny")
	bundle, _ := Sign(st, key)
	var b Bundle
	if err := json.Unmarshal(bundle, &b); err != nil {
		t.Fatal(err)
	}
	payload, _ := base64.StdEncoding.DecodeString(b.DSSEEnvelope.Payload)
	b.DSSEEnvelope.Payload = base64.StdEncoding.EncodeToString([]byte(strings.Replace(string(payload), "4555", "4556", 1)))
	tampered, _ := json.Marshal(b)
	if _, err := Verify(tampered, &key.PublicKey); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Errorf("tampered payload: %v", err)
	}
}

func TestCompareFindsDifferences(t *testing.T) {
	st, _ := FromManifest(sampleManifest(), "tiny")
	m := sampleManifest()
	m.Files[1].SHA256 = strings.Repeat("f", 64)
	m.Files = append(m.Files, manifest.File{Path: "extra.bin", SHA256: strings.Repeat("e", 64)})
	mm := Compare(st, m)
	if len(mm) != 2 || mm[0].Path != "extra.bin" || mm[1].Path != "weights.bin" {
		t.Errorf("mismatches = %+v", mm)
	}
}

// A bundle made by the reference implementation (see testdata/reference).
func TestVerifiesReferenceBundle(t *testing.T) {
	bundle, err := os.ReadFile("testdata/reference/model.sig")
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, _ := os.ReadFile("testdata/reference/pub.pem")
	pub, err := ParsePublicKey(pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	st, err := Verify(bundle, pub)
	if err != nil {
		t.Fatalf("reference bundle: %v", err)
	}
	// Our hint computation agrees with theirs.
	if h, _ := KeyHint(pub); !strings.Contains(string(bundle), h) {
		t.Error("our key hint differs from the reference implementation's")
	}
	mm, err := VerifyDir(st, "testdata/reference/model", "")
	if err != nil || len(mm) != 0 {
		t.Fatalf("reference model dir: %+v, %v", mm, err)
	}
}

// TestReferenceVerifiesOurBundle runs `model_signing verify key` on a
// bundle weightkeep made.
func TestReferenceVerifiesOurBundle(t *testing.T) {
	testutil.Network(t) // installs model-signing with uv
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("uv not installed")
	}
	dir := t.TempDir()
	model := filepath.Join(dir, "model")
	files := map[string]string{"config.json": `{"a":1}`, "sub/w.bin": "weights", ".gitattributes": "x"}
	m := &manifest.Manifest{Version: manifest.FormatVersion, Repo: manifest.Repo{Type: "model", ID: "acme/tiny"}, Commit: strings.Repeat("a", 40)}
	for p, c := range files {
		fp := filepath.Join(model, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(fp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fp, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := walkFiles(model, func(rel, sum string) {
		m.Files = append(m.Files, manifest.File{Path: rel, SHA256: sum})
	}); err != nil {
		t.Fatal(err)
	}
	key, _ := GenerateKey()
	st, err := FromManifest(m, "model")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := Sign(st, key)
	if err != nil {
		t.Fatal(err)
	}
	pub, _ := PublicKeyPEM(&key.PublicKey)
	sig, pubPath := filepath.Join(dir, "model.sig"), filepath.Join(dir, "pub.pem")
	if err := os.WriteFile(sig, bundle, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pubPath, pub, 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(uv, "run", "--quiet", "--no-project", "--python", "3.13", "--with", "model-signing==1.1.1",
		"model_signing", "verify", "key", "--signature", sig, "--public_key", pubPath, model).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "Verification succeeded") {
		t.Fatalf("model_signing rejected our bundle: %v\n%s", err, out)
	}
}
