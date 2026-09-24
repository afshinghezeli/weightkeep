package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/theupdateframework/go-tuf/v2/metadata/config"
	"github.com/theupdateframework/go-tuf/v2/metadata/updater"

	"github.com/afshinghezeli/weightkeep/internal/manifest"
)

// ErrNotListed means the registry has no record for that revision.
var ErrNotListed = errors.New("not in the registry")

// Client reads a synced registry. Every file it returns was checked against
// TUF metadata signed by the registry's maintainers.
type Client struct {
	url  string
	dir  string // local cache: metadata/ and targets/
	root []byte // the trusted root this client was configured with
	up   *updater.Updater
}

// Open prepares a client for the registry at url, caching under dir and
// trusting root (the registry's 1.root.json, distributed out of band).
func Open(url string, root []byte, dir string) (*Client, error) {
	if url == "" || len(root) == 0 {
		return nil, errors.New("no registry configured (set registry.url and registry.root)")
	}
	return &Client{url: strings.TrimRight(url, "/"), dir: dir, root: root}, nil
}

func (c *Client) updaterConfig(local bool) (*config.UpdaterConfig, error) {
	cfg, err := config.New(c.url+"/metadata", c.trustedRoot())
	if err != nil {
		return nil, err
	}
	cfg.LocalMetadataDir = filepath.Join(c.dir, "metadata")
	cfg.LocalTargetsDir = filepath.Join(c.dir, "targets")
	cfg.RemoteTargetsURL = c.url + "/targets"
	cfg.UnsafeLocalMode = local
	if err := cfg.EnsurePathsExist(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// trustedRoot is the newest root already verified by an earlier sync, or
// the configured one. TUF walks root rotations forward from it.
func (c *Client) trustedRoot() []byte {
	if b, err := os.ReadFile(filepath.Join(c.dir, "metadata", "root.json")); err == nil {
		return b
	}
	return c.root
}

// Sync fetches and verifies the latest metadata (signatures, thresholds,
// versions against rollback, expiry against stale mirrors) and every target,
// so lookups work offline afterwards. Records are small; if the registry
// grows large this becomes per-namespace delegations.
func (c *Client) Sync() error {
	cfg, err := c.updaterConfig(false)
	if err != nil {
		return err
	}
	up, err := updater.New(cfg)
	if err != nil {
		return err
	}
	if err := up.Refresh(); err != nil {
		return fmt.Errorf("registry sync: %w", err)
	}
	c.up = up
	for path := range up.GetTopLevelTargets() {
		if _, err := c.target(path); err != nil {
			return err
		}
	}
	return nil
}

// local loads the metadata the last Sync verified, without network.
func (c *Client) local() (*updater.Updater, error) {
	if c.up != nil {
		return c.up, nil
	}
	cfg, err := c.updaterConfig(true)
	if err != nil {
		return nil, err
	}
	up, err := updater.New(cfg)
	if err != nil {
		return nil, err
	}
	if err := up.Refresh(); err != nil {
		return nil, fmt.Errorf("registry metadata is missing or expired; run `weightkeep registry sync`: %w", err)
	}
	c.up = up
	return up, nil
}

// target returns a target's verified bytes, from the cache or the network.
func (c *Client) target(path string) ([]byte, error) {
	up, err := c.local()
	if err != nil {
		return nil, err
	}
	info, err := up.GetTargetInfo(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, ErrNotListed)
	}
	local := filepath.Join(c.dir, "targets", filepath.FromSlash(path))
	if _, data, err := up.FindCachedTarget(info, local); err == nil && data != nil {
		return data, nil
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o755); err != nil {
		return nil, err
	}
	_, data, err := up.DownloadTarget(info, local, "")
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", path, err)
	}
	return data, nil
}

// Record returns the registry's record for repo@commit.
func (c *Client) Record(repo manifest.Repo, commit string) (*Record, error) {
	data, err := c.target(RecordTarget(repo, commit))
	if err != nil {
		return nil, err
	}
	r, err := ParseRecord(data)
	if err != nil {
		return nil, err
	}
	if r.Manifest.Repo != repo || r.Manifest.Commit != commit {
		return nil, fmt.Errorf("registry record at %s describes %s@%s", RecordTarget(repo, commit), r.Manifest.Repo, r.Manifest.Commit)
	}
	return r, nil
}

// Commits lists the commits the registry has records for, for one repo.
func (c *Client) Commits(repo manifest.Repo) ([]string, error) {
	up, err := c.local()
	if err != nil {
		return nil, err
	}
	prefix := repo.Type + "s/" + repo.ID + "/"
	var out []string
	for path := range up.GetTopLevelTargets() {
		rest, ok := strings.CutPrefix(path, prefix)
		if ok && strings.HasSuffix(rest, ".json") && !strings.Contains(rest, "/") {
			out = append(out, strings.TrimSuffix(rest, ".json"))
		}
	}
	sort.Strings(out)
	return out, nil
}

// Size is how many revision records the registry lists.
func (c *Client) Size() (int, error) {
	up, err := c.local()
	if err != nil {
		return 0, err
	}
	return len(up.GetTopLevelTargets()) - 1, nil // minus the denylist
}

// Denylist returns the registry's denylist.
func (c *Client) Denylist() (*Denylist, error) {
	data, err := c.target(DenylistTarget)
	if err != nil {
		return nil, err
	}
	var d Denylist
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse denylist: %w", err)
	}
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return &d, nil
}
