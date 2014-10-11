CREATE TYPE id_status AS ENUM('verified', 'unverified', '');

CREATE TABLE "users" (
    uid varchar(10) PRIMARY KEY,
    name varchar(128) default '',
    tier smallint default 0,
    email varchar(255) default '',
    phone varchar(64) default '',
    fb_uid varchar(64) default '',
    password varchar(255) default '',
    email_status id_status default '',
    phone_status id_status default '',
    plan_expires_at timestamptz,
    stripe_customer_id varchar(64) default '',
    tmp_uid varchar(10) default '',
    created_for_sid varchar(32) default '',
    signup_at timestamptz default NULL,
    created_at timestamptz default NOW()
);

CREATE TABLE "notes" (
    nid varchar(10) PRIMARY KEY,
    title varchar(200) default '',
    txt text default '',
    sharing_token varchar(32) default '',
    created_at timestamptz default NOW(),
    created_by varchar(10) default ''
);

CREATE TYPE noteref_status AS ENUM('active', 'archived');
CREATE TYPE noteref_role AS ENUM('owner', 'peer');

CREATE TABLE "noterefs" (
    nid varchar(10) not null,
    uid varchar(10) not null,
    status noteref_status default 'active',
    role noteref_role default 'peer',
    cursor_pos smallint default 0,
    tmp_nid varchar(10) default '',
    last_seen timestamptz default NULL,
    last_edit timestamptz default NULL,
    --CONSTRAINT fk_nid FOREIGN KEY (nid) REFERENCES "notes" (nid) ON DELETE CASCADE,
    --CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE,
    CONSTRAINT uq_niduid UNIQUE (nid, uid) 
);

CREATE OR REPLACE RULE noterefs_ignore_dupe_nid_uid AS
ON INSERT TO noterefs
WHERE (EXISTS ( SELECT 1
        FROM noterefs
        WHERE noterefs.uid = NEW.uid AND noterefs.nid = NEW.nid)) DO INSTEAD NOTHING;

CREATE TABLE "sessions" (
    sid varchar(32) PRIMARY KEY,
    uid varchar(10) not null,
    data text default '',
    created_at timestamptz default NOW(),
    saved_at timestamptz,
    CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE
);

CREATE TABLE "stripe_tokens" (
    token varchar(255)  default '',
    uid varchar(10) default '',
    seen_at timestamptz,
    CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE
);

CREATE TYPE token_kind AS ENUM('anon', 'login', 'verify', 'share', 'share-url');

CREATE TABLE "tokens" (
    token varchar(128) PRIMARY KEY,
    kind token_kind,
    uid varchar(10) default '',
    nid varchar(10) default '',
    email varchar(255) default '',
    phone varchar(64) default '',
    valid_from timestamptz default NOW(),
    times_consumed smallint default 0
);

CREATE TABLE "contacts" (
    uid varchar(10) not null,
    contact_uid varchar(10) not null,
    name  varchar(128) DEFAULT '',
    email varchar(255) DEFAULT '',
    phone varchar(64) DEFAULT '',
    CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE,
    CONSTRAINT fk_cuid FOREIGN KEY (contact_uid) REFERENCES "users" (uid) ON DELETE CASCADE
);

CREATE OR REPLACE RULE contacts_ignore_dupe_contacts AS
ON INSERT TO contacts
WHERE (EXISTS ( SELECT 1
        FROM contacts
        WHERE contacts.uid = NEW.uid AND contacts.contact_uid = NEW.contact_uid)) DO INSTEAD NOTHING;
