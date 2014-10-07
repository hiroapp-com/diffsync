package diffsync

const (
	DROP_USERS        = "DROP TABLE IF EXISTS 'users'"
	DROP_NOTES        = "DROP TABLE IF EXISTS 'notes'"
	DROP_NOTEREFS     = "DROP TABLE IF EXISTS 'noterefs'"
	DROP_CONTACTS     = "DROP TABLE IF EXISTS 'contacts'"
	DROP_SESSIONS     = "DROP TABLE IF EXISTS 'sessions'"
	DROP_TOKENS       = "DROP TABLE IF EXISTS 'tokens'"
	DROP_STRIPETOKENS = "DROP TABLE IF EXISTS 'strip_tokens'"
	CREATE_USERS      = `
		CREATE TABLE "users" (
			uid text PRIMARY KEY,
			name text default "",
			tier integer default 0,
			email text default "",
			email_status text default "",
			phone text default "",
			phone_status text default "",
			fb_uid text default "",
			stripe_cust_id text default "",
			tmp_uid text default "",
			created_for_sid text default "",
			password text default NULL,
			signup_at timestamp default NULL,
			created_at timestamp default (datetime('now'))
		);`
	CREATE_NOTES = `
		CREATE TABLE "notes" (
			nid text PRIMARY KEY,
			title text default "",
			txt text default "",
			sharing_token text default "",
			created_at timestamp default (datetime('now')),
			created_by text default ""
		);`
	CREATE_CONTACTS = `
		CREATE TABLE "contacts" (
			uid text not null,
			contact_uid text not null,
			name   text DEFAULT "",
			email  text DEFAULT "",
			phone  text DEFAULT "",
			CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE,
			CONSTRAINT fk_cuid FOREIGN KEY (contact_uid) REFERENCES "users" (uid) ON DELETE CASCADE
		);`
	CREATE_NOTEREFS = `
		CREATE TABLE "noterefs" (
			nid text not null,
			uid text not null,
			status text default "",
			role text default "",
			cursor_pos integer default 0,
			tmp_nid text default "",
			last_seen timestamp default NULL,
			last_edit timestamp default NULL,
			invite_sent timestamp default NULL,
			CONSTRAINT fk_nid FOREIGN KEY (nid) REFERENCES "notes" (nid) ON DELETE CASCADE,
			CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE,
			CONSTRAINT uq_niduid UNIQUE (nid, uid) ON CONFLICT IGNORE
		);`
	CREATE_TOKENS = `
		CREATE TABLE "tokens" (
			token text PRIMARY KEY,
			kind text,
			uid text default "",
			nid text default "",
			email text default "",
			phone text default "",
			valid_from timestamp default (datetime('now')),
			consumed_at timestamp default NULL
		);`
	CREATE_SESSIONS = `
		CREATE TABLE "sessions" (
			sid text PRIMARY KEY,
			uid text not null,
			data text default "",
			token_used text default "",
			created_at timestamp default (datetime('now')),
			saved_at timestamp default NULL,
			CONSTRAINT fk_uid FOREIGN KEY (uid) REFERENCES "users" (uid) ON DELETE CASCADE
		);`
	CREATE_STRIPETOKENS = `	
		CREATE TABLE "stripe_tokens" (
			token text default "",
			uid text default "",
			seen_at timestamp
		);`
)
