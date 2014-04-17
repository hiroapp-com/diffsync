package diffsync

// Type Folio Supports the following changes:
//
//  - Docs
//   * (c,s) New File Added
//   * (c) Change Doc status from "active" to "archived"
//   * (c) Change Doc status from "archived" to "active"
//
//  - Settings
//   * (c) Change Email from $prev to $new
//   * (c) Change Tel from $prev to $new
//   * (c) Change Name from $prev to $new
//   * (s) cange Plan from $old to $new

// changeprop: key


import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"time"
)

var (
	validName          = regexp.MustCompile(`^[a-zA-Z0-9 ]+$`)
	validTel           = regexp.MustCompile(`^[0-9 -\(\)]+$`)
	validDoclistChange = regexp.MustCompile(`^[+][a-zA-Z0-9]{3,12}$`)  // todo specify actual docid schema
	validArchiveChange = regexp.MustCompile(`^[+-][a-zA-Z0-9]{3,12}$`) // todo specify actual docid schema
)

type FolioDelta struct {
	Props   map[string]string `json:"props,omitempty"`
	Docs    []string          `json:"docs,omitempty"`
	Archive []string          `json:"archive,omitempty"`
}

func NewFolioDelta() FolioDelta {
	return FolioDelta{map[string]string{}, []string{}, []string{}}
}

type UserInfo struct {
	UID      string `json:"uid,omitempty"`
	Email    string `json:"email,omitempty"`
	Tel      string `json:"tel,omitempty"`
	Name     string `json:"name,omitempty"`
	Token    string `json:"token,omitempty"`
	signupAt *time.Time
}

type Settings struct {
	Plan         string `json:"plan"`
	FBUID        string `json:"fb_uid"`
	stripeCustID string
	isRoot       bool
}

type Folio struct {
	// User contians all information about the Folio-Owner
	User UserInfo

	// Settings contains the Folio-owner's Account Settings
	Settings Settings

	// Docs is an unordered Slice containing all the IDs of the documents the User has access to
	// Each docID in Docs references a document that the folio-owner
	Docs []string

	// Archive is an unordered Slice containing docIDs of all Documents that are archives
	// Each element in Archive *must* also be present in Docs. Thus, the elements of
	// Archive merely reference the elements in Docs and indicate which Documents
	// should be considered Archived.
	// The meaninig of being archived needs to be documented more thorougly
	Archive []string
}

func NewFolio() *Folio {
	return new(Folio)
}

func (folio *Folio) CloneValue() ResourceValue {
	f := new(Folio)
	*f = *folio
	f.Docs = append([]string{}, folio.Docs...)
	f.Archive = append([]string{}, folio.Archive...)
	return f
}

func (folio *Folio) AddDoc(docID string) bool {
	for i := range folio.Docs {
		if folio.Docs[i] == docID {
			// already in doclist
			return false
		}
	}
	folio.Docs = append(folio.Docs, docID)
	return true
}

func (folio *Folio) ToArchive(docID string) bool {
	var found bool
	for i := range folio.Docs {
		if folio.Docs[i] == docID {
			found = true
			break
		}
	}
	if !found {
		// docID is not in Doclist, cannot put it into Archive
		return false
	}
	for i := range folio.Archive {
		if folio.Archive[i] == docID {
			// already in archive, noop
			return false
		}
	}
	folio.Archive = append(folio.Archive, docID)
	return true

}

func (folio *Folio) UnArchive(docID string) bool {
	for i := range folio.Archive {
		if folio.Archive[i] == docID {
			// swap item to end and truncate Docs-slice
			folio.Archive[i] = folio.Archive[len(folio.Archive)-1]
			folio.Archive = folio.Archive[0 : len(folio.Archive)-1]
			return true
		}
	}
	return false
}

func (folio *Folio) ApplyDelta(delta json.RawMessage) (Patch, error) {
	log.Printf("received (supposedly Folio-)Delta: %#v", delta)
	fdelta := NewFolioDelta()
	if err := json.Unmarshal([]byte(delta), &fdelta); err != nil {
		return Patch{}, errors.New("Invalid Delta for Folio")
	}
	log.Println("received delta: ", fdelta)
	changes := [][3]string{}
	// Process all property-changes. These are simple s/old/new string-changes
	var ok bool
	var newProp string
	if newProp, ok = fdelta.Props["user.name"]; ok && validName.MatchString(newProp) {
		folio.User.Name = newProp
		changes = append(changes, [3]string{"user.name", "set", newProp})
	}
	if newProp, ok = fdelta.Props["user.tel"]; ok && validTel.MatchString(newProp) {
		folio.User.Tel = newProp
		changes = append(changes, [3]string{"user.tel", "set", newProp})
	}
	// Check all changes to the document list.
	// note that for now only *additions* are supported. Either the server or the client
	// chan send a newly added doc-id. Neither side will ever promote a "removed" note.
	// if a client wants to 'remove' a file from his folio, he (currently) archives it
	for i := range fdelta.Docs {
		if !validDoclistChange.MatchString(fdelta.Docs[i]) {
			// skip invalid entry
			continue
		}
		op, docid := fdelta.Docs[i][0], fdelta.Docs[i][1:]
		if op == '+' && folio.AddDoc(docid) {
			changes = append(changes, [3]string{"docs", "add", docid})
		}
	}

	// Process all changes to the Doc Archive
	for i := range fdelta.Archive {
		if !validArchiveChange.MatchString(fdelta.Archive[i]) {
			// skip invalid entry
			continue
		}
		op, docid := fdelta.Archive[i][0], fdelta.Archive[i][1:]
		if op == '+' && folio.ToArchive(docid) {
			changes = append(changes, [3]string{"archive", "add", docid})
		} else if op == '-' && folio.UnArchive(docid) {
			changes = append(changes, [3]string{"archive", "rem", docid})
		}
	}
	return Patch{val: changes}, nil
}

func (folio *Folio) ApplyPatch(patch Patch, notify chan<- Event) (changed bool, err error) {
	if patch.val == nil {
		return false, nil
	}
	changes := patch.val.([][3]string)
	var prop, op, val string
	for i := range changes {
		prop, op, val = changes[i][0], changes[i][1], changes[i][2]
		switch {
		case prop == "user.name" && op == "set":
			if val != folio.User.Name {
				changed = true
				folio.User.Name = val
			}
		case prop == "user.tel" && op == "set":
			if val != folio.User.Tel {
				changed = true
				folio.User.Tel = val
			}
		case prop == "docs" && op == "add":
			if folio.AddDoc(val) {
				changed = true
			}
		case prop == "archive" && op == "add":
			if folio.ToArchive(val) {
				changed = true
			}
		case prop == "archive" && op == "rem":
			if folio.UnArchive(val) {
				changed = true
			}
		default:
			//todo log unsupported patch
		}
	}
	return changed, nil
}

func (folio *Folio) GetDelta(latest ResourceValue) (json.RawMessage, error) {
	master, ok := latest.(*Folio)
	if !ok {
		return nil, fmt.Errorf("received illegal master-value (not of type *Folio) for delta calculation")
	}
	// do the delta dance
	delta := NewFolioDelta()
	if folio.User.Name != master.User.Name {
		delta.Props["user.name"] = master.User.Name
	}
	if folio.User.Tel != master.User.Tel {
		delta.Props["user.tel"] = master.User.Tel
	}
	if folio.User.UID != master.User.UID {
		delta.Props["user.uid"] = master.User.UID
	}
	if folio.User.Email != master.User.Email {
		delta.Props["user.email"] = master.User.Email
	}
	delta.Docs = diffStringSlices(folio.Docs, master.Docs)
	delta.Archive = diffStringSlices(folio.Archive, master.Archive)
	return json.Marshal(delta)
}

func (folio *Folio) String() string {
	s, _ := json.MarshalIndent(folio, "", "  ")
	return string(s)
}

func diffStringSlices(old, current []string) []string {
	diff := []string{}
	tmp := make(map[string]struct{})
	for i := range old {
		tmp[old[i]] = struct{}{}
	}
	for i := range current {
		if _, ok := tmp[current[i]]; ok {
			delete(tmp, current[i])
			continue
		}
		diff = append(diff, "+"+current[i])
	}
	for dropped := range tmp {
		diff = append(diff, "-"+dropped)
	}
	return diff
}

func (folio *Folio) MarshalJSON() ([]byte, error) {
	return json.Marshal(*folio)
}

func (folio *Folio) UnmarshalJSON(from []byte) error {
	return json.Unmarshal(from, folio)
}
