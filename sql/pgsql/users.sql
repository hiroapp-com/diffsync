CREATE TYPE id_status AS ENUM('verified', 'unverified');

CREATE TABLE "users" (
    uid text PRIMARY KEY,
    name varchar(128) default '',
    tier smallint default 0,
    plan_expires_at timestamptz,
    email varchar(255) default '',
    email_status id_status,
    phone varchar(64) default '',
    phone_status id_status,
    fb_uid varchar(64) default '',
    stripe_customer_id varchar(64) default '',
    tmp_uid char(10) default '',
    created_for_sid char(32) default '',
    password varchar(255),
    signup_at timestamptz default NULL,
    created_at timestamptz default NOW()
);
