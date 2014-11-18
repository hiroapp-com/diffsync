CREATE TYPE changelog_op AS ENUM('patch-text', 'set-title', 'invite-user', 'rem-peer');

CREATE TABLE "note_changelog" (
    nid varchar(10) NOT NULL,
    uid varchar(10) DEFAULT '',
    op changelog_op,
    delta text,
    txt_snapshot text default '',
    ts timestamptz default now()
);
insert into note_changelog (nid, op, delta, txt_snapshot) select nid, 'patch-text', concat('=', length(txt)), txt from notes;
