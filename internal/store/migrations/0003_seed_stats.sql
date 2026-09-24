-- Bytes uploaded by `weightkeep seed`, per calendar month (UTC, "2026-09"),
-- for the monthly upload cap.
CREATE TABLE seed_stats (
    month    TEXT PRIMARY KEY,
    uploaded INTEGER NOT NULL
) STRICT;
