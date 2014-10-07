CREATE TABLE "tokens" (
    token text PRIMARY KEY,
    kind text,
    uid text default "",
    nid text default "",
    email text default "",
    phone text default "",
    valid_from timestamp default (datetime('now')),
    times_consumed integer default 0
);


