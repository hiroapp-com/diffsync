package diffsync

import (
	"fmt"
	"log"

	"github.com/hiroapp-com/hync/comm"

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
	if err := backend.db.QueryRow("SELECT uid, name, tier, email, phone, email_status, phone_status, signup_at FROM users WHERE uid = $1", uid).Scan(&u.UID, &u.Name, &u.Tier, &u.Email, &u.Phone, &u.EmailStatus, &u.PhoneStatus, &u.SignupAt); err != nil {
		return nil, err
	}
	profile.User = u
	// load contacts from either contacts table and also gather all users we
	// share a note with
	rows, err := backend.db.Query(`SELECT DISTINCT 
										  u.uid,
									      u.tmp_uid,
										  u.name as user_name,
										  u.tier,
										  c.name as contact_name,
										  c.email,
										  c.phone
									FROM users as u
									LEFT OUTER JOIN contacts as c
									  ON c.contact_uid = u.uid AND c.uid = $1 
									WHERE 
										u.tier > -2
									 AND (
										c.uid is not null
									 OR u.uid in (SELECT nr.uid 
													  FROM noterefs as nr
													  LEFT OUTER JOIN noterefs as nr2
													    ON nr.nid = nr2.nid AND nr2.uid = $1
													  WHERE nr.uid <> $1 AND nr2.uid is not null)
										)`, uid)

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
		user.Name = firstNonEmpty(cname.String, uname.String)
		profile.Contacts = append(profile.Contacts, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return profile, nil
}

func createContact(uid1, uid2, name, email, phone string, db *sql.DB, ctx Context) (err error) {
	if _, err = db.Exec("INSERT INTO contacts (uid, contact_uid, name, email, phone) VALUES($1, $2, $3, $4, $5)", uid1, uid2, name, email, phone); err != nil {
		return
	}
	if _, err = db.Exec("INSERT INTO contacts (uid, contact_uid) VALUES($1, $2)", uid2, uid1); err != nil {
		return
	}
	if err = ctx.Router.Handle(Event{UID: uid1, Name: "res-sync", Res: Resource{Kind: "profile", ID: uid1}, ctx: ctx}); err != nil {
		return
	}
	if err = ctx.Router.Handle(Event{UID: uid2, Name: "res-sync", Res: Resource{Kind: "profile", ID: uid2}, ctx: ctx}); err != nil {
		return
	}
	return nil
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
		ref := patch.Value.(User)
		if len(ref.UID) == 8 {
			// official UID provided, ignore everything else
			u, err := findUserByUID(backend.db, ref.UID)
			if err != nil {
				return err
			}
			if u != nil {
				// when adding a user via UID we won't save any user supplied info (e.g. name, email nor phone)
				return createContact(uid, ref.UID, "", "", "", backend.db, ctx)
			}
		}
		if ref.Email == "" && ref.Phone == "" {
			return fmt.Errorf("no useful info provided for add-contact. need either uid, email or phone")
		}
		var u1, u2 *User
		var err error
		if ref.Email != "" {
			u1, err = findUserByEmail(backend.db, ref.Email)
			if err != nil {
				return err
			}
		}
		if ref.Phone != "" {
			u2, err = findUserByPhone(backend.db, ref.Phone)
			if err != nil {
				return err
			}
		}
		if u1 == nil && u2 == nil {
			// none found in db, create new one
			if err = createInvitedUser(backend.db, &ref); err != nil {
				return err
			}
			return createContact(uid, ref.UID, ref.Name, ref.Email, ref.Phone, backend.db, ctx)
		}
		if u1 != nil && u2 != nil {
			// found both via email and phone
			if u1.UID == u2.UID {
				// and they are associated to the same user, sweet! save contact
				return createContact(uid, u1.UID, ref.Name, ref.Email, ref.Phone, backend.db, ctx)
			} else {
				// well we found 2 proper seperate users with those creds. give me both contacts
				if err = createContact(uid, u1.UID, ref.Name, ref.Email, "", backend.db, ctx); err != nil {
					return err
				}
				return createContact(uid, u2.UID, ref.Name, "", ref.Phone, backend.db, ctx)
			}
		}
		// now only one - u1 or u2 - are non-nil. check which one and save it as a contact
		if u1 != nil {
			if err = createContact(uid, u1.UID, ref.Name, ref.Email, "", backend.db, ctx); err != nil {
				return err
			}
			ref.UID = ""
			ref.Email = ""
			if err = createInvitedUser(backend.db, &ref); err != nil {
				return err
			}
			return createContact(uid, ref.UID, ref.Name, "", ref.Phone, backend.db, ctx)
		} else if u2 != nil {
			if err = createContact(uid, u2.UID, ref.Name, "", ref.Phone, backend.db, ctx); err != nil {
				return err
			}
			ref.UID = ""
			ref.Phone = ""
			if err = createInvitedUser(backend.db, &ref); err != nil {
				return err
			}
			return createContact(uid, ref.UID, ref.Name, ref.Email, "", backend.db, ctx)
		}
	case "set-name":
		// patch.Path empty (i.e. only setting own name supported for now)
		// patch.Value contains the new Name
		// patch.OldValue contains the old Name for CAS
		_, err := backend.db.Exec("UPDATE users SET name = $1 WHERE uid = $2 AND name = $3", patch.Value.(string), uid, patch.OldValue.(string))
		if err != nil {
			return err
		}
		result.Tainted(Resource{Kind: "profile", ID: uid})
	case "set-email":
		// patch.Path must be "user/"
		// patch.Value contains the new Email
		// patch.OldValue contains the old Email for CAS
		if patch.Path != "user/" || uid != ctx.uid {
			return nil
		}
		res, err := backend.db.Exec(`UPDATE users 
								     SET email = $1 , email_status = 'unverified' 
									 WHERE uid = $2 
									   AND email = $2 
									   AND (SELECT count(uid) 
											 FROM users 
											 WHERE email = $1 AND tier > 0
										   ) = 0`, patch.Value.(string), uid, patch.OldValue.(string))
		if err != nil {
			return err
		}
		if affected, _ := res.RowsAffected(); affected > 0 {
			go backend.sendVerifyToken(uid, emailRcpt(User{Email: patch.Value.(string)}), ctx.store)
		}
		result.Tainted(Resource{Kind: "profile", ID: uid})

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
		_, err := backend.db.Exec("UPDATE users SET tier = $1 WHERE uid = $2 AND tier = $3", patch.Value.(int64), uid, patch.OldValue.(int64))
		if err != nil {
			return err
		}
		result.Tainted(Resource{Kind: "profile", ID: uid})
	}
	return nil
}

func (backend ProfileSQLBackend) CreateEmpty(ctx Context) (uid string, err error) {
	uid = generateUID()
	_, err = backend.db.Exec("INSERT INTO users (uid, tier, created_for_sid) values ($1, $2, $3)", uid, 0, ctx.sid)
	return
}

func (backend ProfileSQLBackend) sendVerifyToken(uid string, rcpt comm.Rcpt, store *Store) {
	addr, addrKind := rcpt.Addr()
	switch addrKind {
	case "email", "phone":
	default:
		// unsupported address kind, cannot create token
		return
	}
	token, hashed := GenerateToken()
	// WARNING: we're injecting a string into SQL without any escaping.
	//          we need to assure that addrKind is a valid value (see switch above)
	_, err := backend.db.Exec(fmt.Sprintf("INSERT INTO tokens (token, kind, uid, %s) VALUES ($1, 'verify', $2, $3)", addrKind), hashed, uid, addr)
	if err != nil {
		// could not create token oO
		return
	}
	err = store.commHandler(comm.NewRequest("verify", rcpt, map[string]interface{}{
		"token": token,
	}))
	if err != nil {
		log.Printf("error: could not send out verify-token (type %s) to address `%s`; err: %s", addrKind, addr, err)
	}
}

func findUserByUID(db *sql.DB, uid string) (*User, error) {
	return findUserByQuery(db, "uid = $1", uid)
}

func findUserByEmail(db *sql.DB, email string) (*User, error) {
	return findUserByQuery(db, "email = $1 AND NOT (email_status = 'unverified' AND tier > 0)", email)
}

func findUserByPhone(db *sql.DB, phone string) (*User, error) {
	return findUserByQuery(db, "phone = $1 AND NOT (phone_status = 'unverified' AND tier > 0)", phone)
}

func findUserByQuery(db *sql.DB, where string, args ...interface{}) (*User, error) {
	u := User{}
	row := db.QueryRow("SELECT uid, name, email, email_status, phone, phone_status, tier, signup_at from users where "+where, args...)
	err := row.Scan(&u.UID, &u.Name, &u.Email, &u.EmailStatus, &u.Phone, &u.PhoneStatus, &u.Tier, &u.SignupAt)
	if err == sql.ErrNoRows {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &u, nil
}

func createInvitedUser(db *sql.DB, ref *User) (err error) {
	ref.tmpUID = ref.UID
	ref.UID = generateUID()
	if ref.Email != "" {
		ref.EmailStatus = "unverified"
	}
	if ref.Phone != "" {
		ref.PhoneStatus = "unverified"
	}
	_, err = db.Exec("INSERT INTO users (uid, tmp_uid, tier, name, email, phone, email_status, phone_status, created_for_sid) values ($1, $2, $3, $4, $5, $6, $7, $8, $9)",
		ref.UID,
		ref.tmpUID,
		-1,
		ref.Name,
		ref.Email,
		ref.Phone,
		ref.EmailStatus,
		ref.PhoneStatus,
		ref.createdForSID,
	)
	return
}

func generateUID() string {
	return randomString(8)
}
