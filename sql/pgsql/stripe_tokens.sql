CREATE TABLE "stripe_tokens" (
    token varchar(255)  default '',
    uid char(10) default '',
    seen_at timestamptz,
    CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE
);

