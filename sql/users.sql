CREATE TABLE "users" (
    uid text PRIMARY KEY,
    name text default "",
    email text default "",
    phone text default "",
    plan text default "",
    tmp_uid text default "",
    created_for_sid text default "",
    signup_at timestamp default NULL,
    created_at timestamp default (datetime('now'))
);
