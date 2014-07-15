package diffsync

import (
	"log"

	"database/sql"
)

var (
	_ = log.Print
)

type ProfileSQLBackend struct {
	db *sql.DB
}

func NewProfileSQLBackend(db *sql.DB) ProfileSQLBackend {
	return ProfileSQLBackend{db}
}

func (backend ProfileSQLBackend) Get(uid string) (ResourceValue, error) {
	profile := NewProfile()
	u := User{}
	if err := backend.db.QueryRow("SELECT uid, name, tier, email, phone, plan, signup_at FROM users WHERE uid = ?", uid).Scan(&u.UID, &u.Name, &u.Tier, &u.Email, &u.Phone, &u.Plan, &u.SignupAt); err != nil {
		return nil, err
	}
	profile.User = u
	rows, err := backend.db.Query("SELECT uid, tmp_uid, name, signup_at FROM users WHERE uid IN (select contact_uid from contacts where uid = ?) ", uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		user := User{}
		if err := rows.Scan(&user.UID, &user.tmpUID, &user.Name, &user.SignupAt); err != nil {
			return nil, err
		}
		profile.Contacts = append(profile.Contacts, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return profile, nil
}

func (backend ProfileSQLBackend) Patch(uid string, patch Patch, store *Store, ctx context) error {
	log.Printf("received Profile-Patch: %#v", patch)
	switch patch.Op {
	case "add-user":
		// patch.Path namespace prefix for new user (currently only "contacts/" supported
		// patch.Value contains a User object
		// patch.OldValue empty
		if patch.Path != "contacts/" {
			// noop
			return nil
		}
		userRef := patch.Value.(User)
		user, _, err := getOrCreateUser(userRef, backend.db)
		if err != nil {
			return err
		}
		// create contact, fire and forget
		backend.db.Exec("INSERT INTO contacts (uid, contact_uid ) VALUES(?, ?)", uid, user.UID)
		backend.db.Exec("INSERT INTO contacts (uid, contact_uid ) VALUES(?, ?)", user.UID, uid)
	case "set-name":
		// patch.Path empty (i.e. only setting own name supported for now)
		// patch.Value contains the new Name
		// patch.OldValue contains the old Name for CAS
		_, err := backend.db.Exec("UPDATE users SET name = ? WHERE uid = ? AND name = ?", patch.Value.(string), uid, patch.OldValue.(string))
		if err != nil {
			return err
		}
	}
	return nil
}

func (backend ProfileSQLBackend) CreateEmpty(ctx context) (string, error) {
	user, _, err := getOrCreateUser(User{createdForSID: ctx.sid}, backend.db)
	return user.UID, err
}

func getOrCreateUser(userRef User, db *sql.DB) (user User, created bool, err error) {
	txn, err := db.Begin()
	if err != nil {
		return
	}
	var row *sql.Row
	switch {
	case userRef.UID != "":
		row = db.QueryRow("SELECT uid, name, email, phone, plan, signup_at from users where uid = ?", userRef.UID)
	case userRef.Email != "" && userRef.Phone != "":
		row = db.QueryRow("SELECT uid, name, email, phone, plan, signup_at from users where (email = ? AND email_status IN ('verified', 'invited')) OR (phone = ? AND phone_status IN ('verified', 'invited'))", userRef.Email, userRef.Phone)
	case userRef.Email != "":
		row = db.QueryRow("SELECT uid, name, email, phone, plan, signup_at from users where email = ? AND phone_status IN ('verified', 'invited')", userRef.Email)
	case userRef.Phone != "":
		row = db.QueryRow("SELECT uid, name, email, phone, plan, signup_at from users where phone = ? AND phone_status IN ('verified', 'invited')", userRef.Phone)
	default:
	}
	user = User{}
	if row == nil {
		err = sql.ErrNoRows
	} else {
		err = row.Scan(&user.UID, &user.Name, &user.Email, &user.Phone, &user.Plan, &user.SignupAt)
	}
	if err == sql.ErrNoRows {
		// not found, lets create it
		// copy data over from reference
		user = userRef
		user.tmpUID = user.UID
		user.UID = generateUID()
		_, err = txn.Exec("INSERT INTO users (uid, tmp_uid, name, email, phone, created_for_sid) values (?, ?, ?, ?, ?, ?)", user.UID, user.tmpUID, user.Name, user.Email, user.Phone, user.createdForSID)
		if err != nil {
			txn.Rollback()
			log.Printf("error while creatting new user: %s\n", err)
			return
		}
		created = true
	} else if err != nil {
		txn.Rollback()
		log.Printf("error while fetching existing user: %s\n", err)
		return
	}
	txn.Commit()
	return
}

func generateUID() string {
	return randomString(8)
}
