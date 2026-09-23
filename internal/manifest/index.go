package manifest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/afshinghezeli/weightkeep/internal/ids"
	"github.com/afshinghezeli/weightkeep/internal/store"
)

// Path is where the manifest for repo@commit lives under the store root.
func Path(root string, repo Repo, commit string) string {
	parts := append([]string{root, "manifests", repo.Type + "s"}, strings.Split(repo.ID, "/")...)
	return filepath.Join(append(parts, commit+".json")...)
}

// Save writes the manifest file atomically and indexes it. Saving the same
// revision again replaces it.
func Save(ctx context.Context, st *store.Store, m *Manifest) error {
	data, err := m.Canonical()
	if err != nil {
		return err
	}
	path := Path(st.Root(), m.Repo, m.Commit)
	if err := writeAtomic(path, data); err != nil {
		return fmt.Errorf("write manifest %s@%s: %w", m.Repo, m.Commit, err)
	}

	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	key := []any{m.Repo.Type, m.Repo.ID, m.Commit}
	if _, err := tx.ExecContext(ctx, `DELETE FROM revisions WHERE repo_type = ? AND repo_id = ? AND commit_sha = ?`, key...); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO revisions (repo_type, repo_id, commit_sha, fetched_at, manifest_sha256) VALUES (?, ?, ?, ?, ?)`,
		m.Repo.Type, m.Repo.ID, m.Commit, m.FetchedAt.UTC().Unix(), Digest(data)); err != nil {
		return err
	}
	for _, f := range m.Files {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO files (repo_type, repo_id, commit_sha, path, size, sha256, git_sha1, lfs) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			m.Repo.Type, m.Repo.ID, m.Commit, f.Path, f.Size, f.SHA256, f.GitSHA1, f.LFS); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("index manifest %s@%s: %w", m.Repo, m.Commit, err)
	}
	return nil
}

// Load reads the manifest for repo@commit.
func Load(ctx context.Context, st *store.Store, repo Repo, commit string) (*Manifest, error) {
	if err := ids.ValidateRepoID(repo.ID); err != nil {
		return nil, err
	}
	if !ids.IsCommit(commit) {
		return nil, fmt.Errorf("%s@%s: %w", repo, commit, ErrNotFound)
	}
	data, err := os.ReadFile(Path(st.Root(), repo, commit))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%s@%s: %w", repo, commit, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s@%s: %w", repo, commit, err)
	}
	if m.Repo != repo || m.Commit != commit {
		return nil, fmt.Errorf("manifest at %s describes %s@%s", Path(st.Root(), repo, commit), m.Repo, m.Commit)
	}
	return m, nil
}

// Summary is one row of List.
type Summary struct {
	Repo      Repo
	Commit    string
	FetchedAt time.Time
	Files     int   // in the revision
	Size      int64 // of the whole revision
	KeptFiles int   // whose content is in the store
	KeptSize  int64
	// VerifiedAt is the oldest verification time among the kept blobs.
	VerifiedAt time.Time
}

// List returns every kept revision, sorted by repo then fetch time.
func List(ctx context.Context, st *store.Store) ([]Summary, error) {
	rows, err := st.DB().QueryContext(ctx, `
		SELECT r.repo_type, r.repo_id, r.commit_sha, r.fetched_at,
		       COUNT(f.path), COALESCE(SUM(f.size), 0),
		       COUNT(b.sha256), COALESCE(SUM(CASE WHEN b.sha256 IS NOT NULL THEN f.size END), 0),
		       COALESCE(MIN(b.verified_at), 0)
		FROM revisions r
		LEFT JOIN files f USING (repo_type, repo_id, commit_sha)
		LEFT JOIN blobs b ON b.sha256 = f.sha256
		GROUP BY r.repo_type, r.repo_id, r.commit_sha
		ORDER BY r.repo_type, r.repo_id, r.fetched_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Summary
	for rows.Next() {
		var s Summary
		var fetched, verified int64
		if err := rows.Scan(&s.Repo.Type, &s.Repo.ID, &s.Commit, &fetched, &s.Files, &s.Size,
			&s.KeptFiles, &s.KeptSize, &verified); err != nil {
			return nil, err
		}
		s.FetchedAt = time.Unix(fetched, 0).UTC()
		if verified > 0 {
			s.VerifiedAt = time.Unix(verified, 0).UTC()
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Revisions lists the kept commits of one repo, newest fetch first.
func Revisions(ctx context.Context, st *store.Store, repo Repo) ([]string, error) {
	rows, err := st.DB().QueryContext(ctx,
		`SELECT commit_sha FROM revisions WHERE repo_type = ? AND repo_id = ? ORDER BY fetched_at DESC`,
		repo.Type, repo.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete forgets a revision. Its blobs stay until gc.
func Delete(ctx context.Context, st *store.Store, repo Repo, commit string) error {
	if _, err := st.DB().ExecContext(ctx,
		`DELETE FROM revisions WHERE repo_type = ? AND repo_id = ? AND commit_sha = ?`,
		repo.Type, repo.ID, commit); err != nil {
		return err
	}
	err := os.Remove(Path(st.Root(), repo, commit))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// SetRef records that name (a branch or tag) pointed at commit at time at.
func SetRef(ctx context.Context, st *store.Store, repo Repo, name, commit string, at time.Time) error {
	_, err := st.DB().ExecContext(ctx, `
		INSERT INTO refs (repo_type, repo_id, name, commit_sha, resolved_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (repo_type, repo_id, name) DO UPDATE SET
			commit_sha = excluded.commit_sha, resolved_at = excluded.resolved_at`,
		repo.Type, repo.ID, name, commit, at.UTC().Unix())
	return err
}

// ResolveRef returns the commit name last pointed at and when that was seen.
// A full commit id resolves to itself if that revision is kept.
func ResolveRef(ctx context.Context, st *store.Store, repo Repo, name string) (string, time.Time, error) {
	var commit string
	var at int64
	err := st.DB().QueryRowContext(ctx,
		`SELECT commit_sha, resolved_at FROM refs WHERE repo_type = ? AND repo_id = ? AND name = ?`,
		repo.Type, repo.ID, name).Scan(&commit, &at)
	if errors.Is(err, sql.ErrNoRows) {
		if ids.IsCommit(name) {
			var fetched int64
			err = st.DB().QueryRowContext(ctx,
				`SELECT fetched_at FROM revisions WHERE repo_type = ? AND repo_id = ? AND commit_sha = ?`,
				repo.Type, repo.ID, name).Scan(&fetched)
			if err == nil {
				return name, time.Unix(fetched, 0).UTC(), nil
			}
		}
		return "", time.Time{}, fmt.Errorf("%s@%s: %w", repo, name, ErrNotFound)
	}
	if err != nil {
		return "", time.Time{}, err
	}
	return commit, time.Unix(at, 0).UTC(), nil
}

// ReferencedBlobs returns every SHA-256 some kept revision needs.
func ReferencedBlobs(ctx context.Context, st *store.Store) (map[string]bool, error) {
	rows, err := st.DB().QueryContext(ctx, `SELECT DISTINCT sha256 FROM files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out[s] = true
	}
	return out, rows.Err()
}

func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".manifest-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
