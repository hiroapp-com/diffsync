package diffsync

import (
	"fmt"
	"log"

	"github.com/hiro/hync/comm"

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
	if err := backend.db.QueryRow("SELECT uid, name, tier, email, phone, email_status, phone_status, plan, signup_at FROM users WHERE uid = ?", uid).Scan(&u.UID, &u.Name, &u.Tier, &u.Email, &u.Phone, &u.EmailStatus, &u.PhoneStatus, &u.Plan, &u.SignupAt); err != nil {
		return nil, err
	}
	profile.User = u
	// load contacts
	rows, err := backend.db.Query(`SELECT u.uid,
									      u.tmp_uid,
										  u.name as user_name,
										  u.tier,
										  c.name as contact_name,
										  c.email,
										  c.phone
									FROM contacts as c
									LEFT JOIN users as u 
										ON u.uid = c.contact_uid
									WHERE c.uid = ? and u.tier <> 0`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		user := User{}
		var uname, cname, email, phone sql.NullString
		if err := rows.Scan(&user.UID, &user.tmpUID, &uname, &user.Tier, &cname, &email, &phone); err != nil {
			return nil, err
		}
		user.Email = email.String
		user.Phone = phone.String
		user.Name = firstNonEmpty(cname.String, uname.String, phone.String, email.String)
		profile.Contacts = append(profile.Contacts, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return profile, nil
}

func (backend ProfileSQLBackend) Patch(uid string, patch Patch, result *SyncResult, ctx Context) error {
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
		if userRef.Email != "" {
			userRef.EmailStatus = "invited"
		}
		if userRef.Phone != "" {
			userRef.PhoneStatus = "invited"
		}
		user, _, err := getOrCreateUser(userRef, backend.db)
		if err != nil {
			return err
		}
		// create contact, fire and forget
		backend.db.Exec("INSERT INTO contacts (uid, contact_uid, name, email, phone) VALUES(?, ?, ?, ?, ?)", uid, user.UID, user.Name, user.Email, user.Phone)
		backend.db.Exec("INSERT INTO contacts (uid, contact_uid ) VALUES(?, ?)", user.UID, uid)
		result.Taint(Resource{Kind: "profile", ID: uid})
	case "set-name":
		// patch.Path empty (i.e. only setting own name supported for now)
		// patch.Value contains the new Name
		// patch.OldValue contains the old Name for CAS
		_, err := backend.db.Exec("UPDATE users SET name = ? WHERE uid = ? AND name = ?", patch.Value.(string), uid, patch.OldValue.(string))
		if err != nil {
			return err
		}
		result.Taint(Resource{Kind: "profile", ID: uid})
	case "set-email":
		// patch.Path must be "user/"
		// patch.Value contains the new Email
		// patch.OldValue contains the old Email for CAS
		if patch.Path != "user/" || uid != ctx.uid {
			return nil
		}
		res, err := backend.db.Exec("UPDATE users SET email = ?, email_status = 'unverified' WHERE uid = ? AND email = ?", patch.Value.(string), uid, patch.OldValue.(string))
		if err != nil {
			return err
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			go backend.sendVerifyToken(uid, emailRcpt(User{Email: patch.Value.(string)}), ctx.store)
		}
		result.Taint(Resource{Kind: "profile", ID: uid})

	case "set-tier":
		// patch.Path must be "user/"
		// patch.Value contains the new Tier
		// patch.OldValue contains the old Tier for CAS
		if ctx.uid != "sys" {
			log.Printf("non-`sys` context tried to set a user's tier. target-uid: %s context uid: %s", uid, ctx.uid)
			return nil
		}
		if patch.Path != "user/" {
			return nil
		}
		_, err := backend.db.Exec("UPDATE users SET tier = ? WHERE uid = ? AND tier = ?", patch.Value.(int64), uid, patch.OldValue.(int64))
		if err != nil {
			return err
		}
		result.Taint(Resource{Kind: "profile", ID: uid})
	}
	return nil
}

func (backend ProfileSQLBackend) CreateEmpty(ctx Context) (string, error) {
	user, _, err := getOrCreateUser(User{createdForSID: ctx.sid}, backend.db)
	return user.UID, err
}

func (backend ProfileSQLBackend) sendVerifyToken(uid string, rcpt comm.Rcpt, store *Store) {
	addr, addrKind := rcpt.Addr()
	switch addrKind {
	case "email", "phone":
	default:
		// unsupported address kind, cannot create token
		return
	}
	token, hashed := generateToken()
	// WARNING: we're injecting a string into SQL without any escaping.
	//          we need to assure that addrKind is a valid value (see switch above)
	_, err := backend.db.Exec(fmt.Sprintf("INSERT INTO tokens (token, kind, uid, %s) VALUES (?, 'verify', ?, ?)", addrKind), hashed, uid, addr)
	if err != nil {
		// could not create token oO
		return
	}
	err = store.commHandler(comm.NewRequest("verify", rcpt, map[string]string{
		"token": token,
	}))
	if err != nil {
		log.Printf("error: could not send out verify-token (type %s) to address `%s`; err: %s", addrKind, addr, err)
	}
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
		_, err = txn.Exec("INSERT INTO users (uid, tmp_uid, tier, name, email, phone, email_status, phone_status, created_for_sid) values (?, ?, ?, ?, ?, ?, ?, ?, ?)", user.UID, user.tmpUID, user.Tier, user.Name, user.Email, user.Phone, user.EmailStatus, user.PhoneStatus, user.createdForSID)
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
