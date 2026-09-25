package registry

import (
	"crypto"
	"crypto/ecdsa"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"

	"github.com/afshinghezeli/weightkeep/internal/oms"
)

// Role key files a maintainer keeps in a keys directory. root and targets
// may have several keys (root-1.key, root-2.key, ...) for thresholds.
const (
	roleRoot      = "root"
	roleTargets   = "targets"
	roleSnapshot  = "snapshot"
	roleTimestamp = "timestamp"
)

// Expiry of each role's metadata. The timestamp is short so a mirror can't
// serve a stale registry (with an old denylist) for long; CI re-signs it.
var expiry = map[string]time.Duration{
	roleRoot:      365 * 24 * time.Hour,
	roleTargets:   90 * 24 * time.Hour,
	roleSnapshot:  30 * 24 * time.Hour,
	roleTimestamp: 7 * 24 * time.Hour,
}

// Keys holds the private keys of each role.
type Keys map[string][]*ecdsa.PrivateKey

// GenerateKeys makes one P-256 key per role, rootKeys for root.
func GenerateKeys(rootKeys int) (Keys, error) {
	k := Keys{}
	counts := map[string]int{roleRoot: rootKeys, roleTargets: 1, roleSnapshot: 1, roleTimestamp: 1}
	for role, n := range counts {
		for range n {
			key, err := oms.GenerateKey()
			if err != nil {
				return nil, err
			}
			k[role] = append(k[role], key)
		}
	}
	return k, nil
}

// WriteKeys stores keys as <role>-<n>.key PEM files (mode 0600).
func WriteKeys(dir string, k Keys) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for role, keys := range k {
		for i, key := range keys {
			p := filepath.Join(dir, fmt.Sprintf("%s-%d.key", role, i+1))
			if _, err := os.Stat(p); err == nil {
				return fmt.Errorf("%s exists; not overwriting a key", p)
			}
			b, err := oms.PrivateKeyPEM(key)
			if err != nil {
				return err
			}
			if err := os.WriteFile(p, b, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReadKeys loads every <role>-<n>.key in dir.
func ReadKeys(dir string) (Keys, error) {
	k := Keys{}
	for _, role := range []string{roleRoot, roleTargets, roleSnapshot, roleTimestamp} {
		matches, err := filepath.Glob(filepath.Join(dir, role+"-*.key"))
		if err != nil {
			return nil, err
		}
		for _, p := range matches {
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			key, err := oms.ParsePrivateKey(b)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", p, err)
			}
			k[role] = append(k[role], key)
		}
	}
	return k, nil
}

// Init writes the first root metadata for a new registry into out/metadata.
// Only root keys are needed to sign it; every role's public key goes in.
func Init(out string, k Keys, rootThreshold int, now time.Time) error {
	if len(k[roleRoot]) < rootThreshold || rootThreshold < 1 {
		return fmt.Errorf("root threshold %d needs at least that many root keys", rootThreshold)
	}
	root := metadata.Root(now.Add(expiry[roleRoot]))
	root.Signed.ConsistentSnapshot = false
	for role, keys := range k {
		for _, key := range keys {
			pub, err := metadata.KeyFromPublicKey(&key.PublicKey)
			if err != nil {
				return err
			}
			if err := root.Signed.AddKey(pub, role); err != nil {
				return err
			}
		}
	}
	root.Signed.Roles[roleRoot].Threshold = rootThreshold
	if err := signWith(root, k[roleRoot]); err != nil {
		return err
	}
	dir := filepath.Join(out, "metadata")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := root.ToFile(filepath.Join(dir, "1.root.json"), true); err != nil {
		return err
	}
	return root.ToFile(filepath.Join(dir, "root.json"), true)
}

// BuildInput is what a registry publish contains.
type BuildInput struct {
	Out      string // published directory (metadata/ and targets/)
	Records  []*Record
	Denylist *Denylist
	Keys     Keys
	Now      time.Time
}

// Build writes targets and signs targets, snapshot and timestamp metadata.
// Versions continue from whatever was published in Out before, so clients
// that saw the previous registry accept this one and reject older ones.
func Build(in BuildInput) error {
	metaDir := filepath.Join(in.Out, "metadata")
	targetsDir := filepath.Join(in.Out, "targets")
	if _, err := os.Stat(filepath.Join(metaDir, "root.json")); err != nil {
		return fmt.Errorf("no root.json in %s; run init first", metaDir)
	}
	if in.Denylist == nil {
		in.Denylist = &Denylist{Version: FormatVersion}
	}
	if err := in.Denylist.Validate(); err != nil {
		return err
	}

	targets := metadata.Targets(in.Now.Add(expiry[roleTargets]))
	targets.Signed.Version = nextVersion(filepath.Join(metaDir, "targets.json"))
	add := func(path string, v any) error {
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		p := filepath.Join(targetsDir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			return err
		}
		tf, err := metadata.TargetFile().FromBytes(path, data, "sha256")
		if err != nil {
			return err
		}
		targets.Signed.Targets[path] = tf
		return nil
	}
	seen := map[string]bool{}
	for _, r := range in.Records {
		if err := r.Validate(); err != nil {
			return err
		}
		path := RecordTarget(r.Manifest.Repo, r.Manifest.Commit)
		if seen[path] {
			return fmt.Errorf("two records for %s", path)
		}
		seen[path] = true
		if err := add(path, r); err != nil {
			return err
		}
	}
	if err := add(DenylistTarget, in.Denylist); err != nil {
		return err
	}
	if err := signWith(targets, in.Keys[roleTargets]); err != nil {
		return fmt.Errorf("sign targets: %w", err)
	}
	if err := targets.ToFile(filepath.Join(metaDir, "targets.json"), true); err != nil {
		return err
	}

	snapshot := metadata.Snapshot(in.Now.Add(expiry[roleSnapshot]))
	snapshot.Signed.Version = nextVersion(filepath.Join(metaDir, "snapshot.json"))
	snapshot.Signed.Meta["targets.json"] = metadata.MetaFile(targets.Signed.Version)
	if err := signWith(snapshot, in.Keys[roleSnapshot]); err != nil {
		return fmt.Errorf("sign snapshot: %w", err)
	}
	if err := snapshot.ToFile(filepath.Join(metaDir, "snapshot.json"), true); err != nil {
		return err
	}
	return Timestamp(in.Out, in.Keys, in.Now)
}

// Timestamp re-signs timestamp.json for the current snapshot. CI runs it
// daily so the registry doesn't expire between publishes.
func Timestamp(out string, k Keys, now time.Time) error {
	metaDir := filepath.Join(out, "metadata")
	snap, err := metadata.Snapshot().FromFile(filepath.Join(metaDir, "snapshot.json"))
	if err != nil {
		return fmt.Errorf("read snapshot: %w", err)
	}
	ts := metadata.Timestamp(now.Add(expiry[roleTimestamp]))
	ts.Signed.Version = nextVersion(filepath.Join(metaDir, "timestamp.json"))
	ts.Signed.Meta["snapshot.json"] = metadata.MetaFile(snap.Signed.Version)
	if err := signWith(ts, k[roleTimestamp]); err != nil {
		return fmt.Errorf("sign timestamp: %w", err)
	}
	return ts.ToFile(filepath.Join(metaDir, "timestamp.json"), true)
}

type signable interface {
	Sign(signature.Signer) (*metadata.Signature, error)
}

func signWith(md signable, keys []*ecdsa.PrivateKey) error {
	if len(keys) == 0 {
		return errors.New("no key for this role")
	}
	for _, k := range keys {
		s, err := signature.LoadSigner(k, crypto.SHA256)
		if err != nil {
			return err
		}
		if _, err := md.Sign(s); err != nil {
			return err
		}
	}
	return nil
}

// nextVersion reads the version of a published metadata file and returns
// one more, or 1 if there is none.
func nextVersion(path string) int64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 1
	}
	var v struct {
		Signed struct {
			Version int64 `json:"version"`
		} `json:"signed"`
	}
	if json.Unmarshal(b, &v) != nil {
		return 1
	}
	return v.Signed.Version + 1
}
