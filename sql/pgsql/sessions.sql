CREATE TABLE "sessions" (
    sid char(32) PRIMARY KEY,
    uid char(10) not null,
    data text default '',
    created_at timestamptz default NOW(),
    saved_at timestamptz,
    CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE
);

