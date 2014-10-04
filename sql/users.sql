CREATE TABLE "users" (
    uid text PRIMARY KEY,
    name text default "",
    tier integer default 0,
    plan_expires_at timestamp default NULL,
    email text default "",
    email_status text default "",
    phone text default "",
    phone_status text default "",
    fb_uid text default "",
    stripe_customer_id text default "",
    tmp_uid text default "",
    created_for_sid text default "",
    password text default NULL,
    signup_at timestamp default NULL,
    created_at timestamp default (datetime('now'))
);
