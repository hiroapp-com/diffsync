CREATE TABLE "contacts" (
    uid text not null,
    contact_uid text not null,
    name  text DEFAULT "",
    email text DEFAULT "",
    phone text DEFAULT "",
    CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE,
    CONSTRAINT fk_cuid FOREIGN KEY (contact_uid) REFERENCES "users" (uid) ON DELETE CASCADE
);
