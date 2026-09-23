-- One row per kept revision. The manifest file under manifests/ is the
-- record; these tables index it.
CREATE TABLE revisions (
    repo_type       TEXT NOT NULL,
    repo_id         TEXT NOT NULL,
    commit_sha      TEXT NOT NULL,
    fetched_at      INTEGER NOT NULL,
    manifest_sha256 TEXT NOT NULL,
    PRIMARY KEY (repo_type, repo_id, commit_sha)
) STRICT;

CREATE TABLE files (
    repo_type  TEXT NOT NULL,
    repo_id    TEXT NOT NULL,
    commit_sha TEXT NOT NULL,
    path       TEXT NOT NULL,
    size       INTEGER NOT NULL,
    sha256     TEXT NOT NULL,
    git_sha1   TEXT NOT NULL,
    lfs        INTEGER NOT NULL,
    PRIMARY KEY (repo_type, repo_id, commit_sha, path),
    FOREIGN KEY (repo_type, repo_id, commit_sha)
        REFERENCES revisions (repo_type, repo_id, commit_sha) ON DELETE CASCADE
) STRICT;

CREATE INDEX files_sha256 ON files (sha256);

-- Branch and tag names as last resolved. Hints, not identities.
CREATE TABLE refs (
    repo_type   TEXT NOT NULL,
    repo_id     TEXT NOT NULL,
    name        TEXT NOT NULL,
    commit_sha  TEXT NOT NULL,
    resolved_at INTEGER NOT NULL,
    PRIMARY KEY (repo_type, repo_id, name)
) STRICT;
