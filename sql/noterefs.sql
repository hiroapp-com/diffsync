CREATE TABLE "noterefs" (
    nid text not null,
    uid text not null,
    status text default "",
    role text default "",
    cursor_pos integer default 0,
    tmp_nid text default "",
    last_seen timestamp default NULL,
    last_edit timestamp default NULL,
    CONSTRAINT fk_nid FOREIGN KEY (nid) REFERENCES "notes" (nid) ON DELETE CASCADE,
    CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE
);

