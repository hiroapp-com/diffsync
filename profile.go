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
	Phone         string     `json:"phone,omitempty"`
	Plan          string     `json:"plan,omitempty"`
	SignupAt      *time.Time `json:"signup_at"`
	CreatedAt     *time.Time `json:"-"`
	tmpUID        string     `json:"-"`
	createdForSID string     `json:"-"`
}

type Profile struct {
	User     User   `json:"user"`
	Contacts []User `json:"contacts"`
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
	return fmt.Sprintf("%#v", prof)
}

type ProfileDelta []UserChange
type UserChange struct {
	Op    string      `json:"op"`
	Path  string      `json:"path"`
	Value interface{} `json:"value,omitempty"`
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
