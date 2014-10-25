CREATE TABLE "notes" (
    nid text PRIMARY KEY,
    title text default "",
    txt text default "",
    sharing_token text default "",
    created_at timestamp default NOW(),
    created_by text default ""
);
