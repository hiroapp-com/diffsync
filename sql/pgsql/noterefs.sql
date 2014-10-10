CREATE TYPE noteref_status AS ENUM('active', 'archived');
CREATE TYPE noteref_role AS ENUM('owner', 'peer');
CREATE TABLE "noterefs" (
    nid char(10) not null,
    uid char(10) not null,
    status noteref_status,
    role noteref_role,
    cursor_pos smallint default 0,
    tmp_nid char(10) default '',
    last_seen timestamptz default NULL,
    last_edit timestamptz default NULL,
    CONSTRAINT fk_nid FOREIGN KEY (nid) REFERENCES "notes" (nid) ON DELETE CASCADE,
    CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE,
    CONSTRAINT uq_niduid UNIQUE (nid, uid) 
);

CREATE OR REPLACE RULE noterefs_ignore_dupe_nid_uid AS
ON INSERT TO noterefs
WHERE (EXISTS ( SELECT 1
        FROM noterefs
        WHERE noterefs.uid = NEW.uid AND noterefs.nid = NEW.nid)) DO INSTEAD NOTHING;
