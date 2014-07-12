CREATE TABLE "tokens" (
    token text PRIMARY KEY,
    kind text,
    uid text default "",
    nid text default "",
    email text default "",
    phone text default "",
    created_at timestamp default (datetime('now')),
    consumed_at timestamp default NULL
);


