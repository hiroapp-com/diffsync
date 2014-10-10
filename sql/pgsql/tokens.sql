CREATE TYPE token_kind AS ENUM('anon', 'login', 'verify', 'share', 'share-url');
CREATE TABLE "tokens" (
    token char(128) PRIMARY KEY,
    kind token_kind,
    uid char(10) default '',
    nid char(10) default '',
    email varchar(255) default '',
    phone varchar(64) default '',
    valid_from timestamptz default NOW(),
    times_consumed smallint default 0,
    CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE,
    CONSTRAINT fk_nid FOREIGN KEY (nid) REFERENCES "notes" (nid) ON DELETE CASCADE
);


