CREATE TABLE "notes" (
    nid text PRIMARY KEY,
    title varchar(200) default '',
    txt text default '',
    sharing_token char(32) default '',
    created_at timestamptz default NOW(),
    created_by char(10) default ''
);
