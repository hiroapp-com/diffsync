package diffsync

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"crypto/sha1"
	"encoding/json"
)

var (
	_ = log.Print
)

type User struct {
	UID           string     `json:"uid,omitempty"`
	Name          string     `json:"name,omitempty"`
	Email         string     `json:"email,omitempty"`
	EmailStatus   string     `json:"email_status"`
	Phone         string     `json:"phone,omitempty"`
	PhoneStatus   string     `json:"email_status"`
	Tier          int64      `json:"tier"`
	SignupAt      *UnixTime  `json:"signup_at,omitempty"`
	CreatedAt     *time.Time `json:"-"`
	tmpUID        string     `json:"-"`
	createdForSID string     `json:"-"`
}

type Profile struct {
	User     User   `json:"user"`
	Contacts []User `json:"contacts"`
}

type emailRcpt User
type phoneRcpt User
type preferredRcpt User

func (rcpt emailRcpt) Addr() (string, string) {
	return User(rcpt).Email, "email"
}
func (rcpt emailRcpt) DisplayName() string {
	u := User(rcpt)
	return firstNonEmpty(u.Name, u.Email, "Anonymous")
}

func (rcpt phoneRcpt) Addr() (string, string) {
	return User(rcpt).Phone, "phone"
}

func (rcpt phoneRcpt) DisplayName() string {
	u := User(rcpt)
	return firstNonEmpty(u.Name, u.Phone, "Anonymous")
}

func (rcpt preferredRcpt) Addr() (string, string) {
	u := User(rcpt)
	switch {
	case u.Phone != "" && u.PhoneStatus != "unverified":
		// verified or invited phone has preference over all
		return u.Phone, "phone"
	case u.Phone != "" && u.Email != "" && u.EmailStatus == "unverified":
		// both phone and email are unverified, give phone preference
		return u.Phone, "phone"
	case u.Phone != "" && u.Email == "":
		// unverified phone nr is everything we have
		return u.Phone, "phone"
	case u.Email != "":
		// email (any status) is everything we've got. use that
		return u.Email, "email"
	default:
		// have nothing. ignore
		return "", ""

	}
}

func (rcpt preferredRcpt) DisplayName() string {
	u := User(rcpt)
	switch _, kind := rcpt.Addr(); kind {
	case "email":
		return firstNonEmpty(u.Name, u.Email, "Anonymous")
	case "phone":
		return firstNonEmpty(u.Name, u.Phone, "Anonymous")
	default:
		return ""
	}
}

func NewProfile() Profile {
	return Profile{User{}, []User{}}
}

func (p Profile) Empty() ResourceValue {
	return Profile{Contacts: []User{}}
}

func (u User) Hash() string {
	hasher := sha1.New()
	fmt.Fprintf(hasher, "%v", u)
	return fmt.Sprintf("%x", hasher.Sum(nil))[:12]
}

func (u User) pathRef(prefix string) string {
	switch {
	case u.UID != "":
		return fmt.Sprintf("%s/uid:%s", prefix, u.UID)
	case u.Email != "":
		return fmt.Sprintf("%s/email:%s", prefix, u.Email)
	case u.Phone != "":
		return fmt.Sprintf("%s/phone:%s", prefix, u.Phone)
	}
	return "!!invalid"
}

func (prof Profile) Clone() ResourceValue {
	return prof
}

func (prof Profile) String() string {
	return fmt.Sprintf("<profile uid: %s, email: %s/%s, phone: %s/%s, name: %s, tier: %s, signup: %s, contacts: %s",
		prof.User.UID,
		prof.User.Email,
		prof.User.EmailStatus,
		prof.User.Phone,
		prof.User.PhoneStatus,
		prof.User.Name,
		prof.User.Tier,
		prof.User.SignupAt,
		prof.Contacts,
	)
}

type ProfileDelta []UserChange
type UserChange struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
}

func (change UserChange) String() string {
	return fmt.Sprintf("<delta op: %s, path: %s, val: %s", change.Op, change.Path, change.Value)
}

func (prof Profile) GetDelta(latest ResourceValue) Delta {
	master := latest.(Profile)
	delta := ProfileDelta{}
	userPath := prof.User.pathRef("user")
	if master.User.UID != prof.User.UID {
		delta = append(delta, UserChange{"set-uid", userPath, master.User.UID})
		prof.User.UID = master.User.UID
		// re-set userPath to make sure we use the new UID
		userPath = prof.User.pathRef("user")

	}
	if master.User.Name != prof.User.Name {
		delta = append(delta, UserChange{"set-name", userPath, master.User.Name})
	}
	if master.User.Email != prof.User.Email {
		delta = append(delta, UserChange{"set-email", userPath, master.User.Email})
	}
	if master.User.Phone != prof.User.Phone {
		delta = append(delta, UserChange{"set-phone", userPath, master.User.Phone})
	}
	if master.User.Tier != prof.User.Tier {
		delta = append(delta, UserChange{"set-tier", userPath, master.User.Tier})
	}
	// pupulate lookup objects of old versions
	oldExisting := map[string]User{}
	oldDangling := []User{}
	for _, contact := range prof.Contacts {
		if contact.UID == "" {
			oldDangling = append(oldDangling, contact)
			continue
		}
		oldExisting[contact.UID] = contact
	}
	// now check out the current master-version
	for i := range master.Contacts {
		old, ok := oldExisting[master.Contacts[i].UID]
		if ok {
			if old.Hash() != master.Contacts[i].Hash() {
				delta = append(delta, UserChange{"swap-user", old.pathRef("contacts"), master.Contacts[i]})
			}
			delete(oldExisting, old.UID)
			continue
		}
		if idx, found := indexOfContacts(master.Contacts[i], oldDangling); found {
			delta = append(delta, UserChange{"swap-user", oldDangling[idx].pathRef("contacts"), master.Contacts[i]})
			oldDangling = append(oldDangling[:idx], oldDangling[idx+1:]...)
			continue
		}
		// nothing matched, Looks like a new one!
		cpy := master.Contacts[i]
		delta = append(delta, UserChange{"add-user", "contacts/", cpy})
	}
	// remove everything that's left in the bag.
	for uid, _ := range oldExisting {
		delta = append(delta, UserChange{Op: "rem-user", Path: fmt.Sprintf("contacts/uid:%s", uid)})
	}
	for i := range oldDangling {
		// everything left in the dangling-array did not have a matching entry in the master-list, thus remove
		delta = append(delta, UserChange{Op: "rem-user", Path: oldDangling[i].pathRef("contacts")})
	}
	return delta
}

func (uc *UserChange) UnmarshalJSON(from []byte) (err error) {
	tmp := struct {
		Op       string          `json:"op"`
		Path     string          `json:"path"`
		RawValue json.RawMessage `json:"value"`
	}{}
	if err = json.Unmarshal(from, &tmp); err != nil {
		return
	}
	uc.Op = tmp.Op
	uc.Path = tmp.Path
	switch tmp.Op {
	case "add-user", "swap-user":
		u := User{}
		if err = json.Unmarshal(tmp.RawValue, &u); err == nil {
			uc.Value = u
		}
	default:
		s := ""
		if err = json.Unmarshal(tmp.RawValue, &s); err == nil {
			uc.Value = s
		}
	}
	return
}

func (delta ProfileDelta) HasChanges() bool {
	return len(delta) > 0
}

func (delta ProfileDelta) Apply(to ResourceValue) (ResourceValue, []Patch, error) {
	original, ok := to.(Profile)
	if !ok {
		return nil, nil, errors.New("cannot apply ProfileDelta to non Profile resource")
	}
	newres := original.Clone().(Profile)
	patches := []Patch{}
	for _, diff := range delta {
		switch diff.Op {
		case "add-user":
			if diff.Path != "contacts/" {
				// cannot add users to anywhere but contacts for now
				continue
			}
			user, ok := diff.Value.(User)
			if !ok {
				// malformed payload, ignore
				continue
			}
			newres.Contacts = append(newres.Contacts, user)
			patches = append(patches, Patch{Op: diff.Op, Path: "contacts/", Value: user})
		case "set-name":
			if !strings.HasPrefix(diff.Path, "user/") {
				// cannot change name of anyone but own user for now
				continue
			}
			newName, ok := diff.Value.(string)
			if !ok {
				// wut not a string? ABORT
				continue
			}
			patches = append(patches, Patch{Op: "set-name", Value: newName, OldValue: newres.User.Name})
			newres.User.Name = newName
		case "set-email":
			if !strings.HasPrefix(diff.Path, "user/") {
				// cannot change name of anyone but own user for now
				continue
			}
			newEmail, ok := diff.Value.(string)
			if !ok {
				// wut not a string? ABORT
				continue
			}
			patches = append(patches, Patch{Op: "set-email", Path: "user/", Value: newEmail, OldValue: newres.User.Email})
			newres.User.Email = newEmail
		}
	}
	return newres, patches, nil
}

// these functions will be fleshed out and possibly put somewhere else, as soon as we have the proper DB logic

func indexOfContacts(needle User, haystack []User) (idx int, found bool) {
	chkFn := func(rhs User) bool {
		switch {
		case needle.UID == rhs.UID && needle.UID != "":
			return true
		case needle.Email == rhs.Email && needle.Email != "":
			return true
		case needle.Phone == rhs.Phone && needle.Phone != "":
			return true
		}
		return false
	}
	for idx = range haystack {
		if chkFn(haystack[idx]) {
			return idx, true
		}
	}
	return 0, false
}
