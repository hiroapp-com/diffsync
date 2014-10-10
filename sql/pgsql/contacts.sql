CREATE TABLE "contacts" (
    uid char(10) not null,
    contact_uid char(10) not null,
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
