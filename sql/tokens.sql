CREATE TABLE "tokens" (
    token text PRIMARY KEY,
    uid text default "",
    nid text default "",
    created_at timestamp default (datetime('now')),
    consumed_at timestamp default NULL
);


