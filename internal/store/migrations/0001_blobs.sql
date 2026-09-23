CREATE TABLE blobs (
    sha256      TEXT PRIMARY KEY,
    git_sha1    TEXT,
    size        INTEGER NOT NULL,
    created_at  INTEGER NOT NULL,
    verified_at INTEGER NOT NULL
) STRICT;

CREATE INDEX blobs_git_sha1 ON blobs (git_sha1) WHERE git_sha1 IS NOT NULL;
