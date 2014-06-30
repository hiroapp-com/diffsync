CREATE TABLE "notes" (
    nid text PRIMARY KEY,
    title text default "",
    txt text default "",
    created_at timestamp default (datetime('now')),
    created_by text default ""
);
