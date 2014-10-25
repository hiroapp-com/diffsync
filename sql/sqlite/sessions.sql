CREATE TABLE "sessions" (
    sid text PRIMARY KEY,
    uid text not null,
    data text default "",
    token_used text default "",
    created_at timestamp default (datetime('now')),
    saved_at timestamp default NULL,
    CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE
);

