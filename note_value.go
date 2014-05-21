package diffsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
)

var (
	_ = log.Print
)

type User struct {
	UID   string `json:"uid,omitempty"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
	Phone string `json:"phone,omitempty"`
}

type Note struct {
	Title     string        `json:"title"`
	Text      TextValue     `json:"text"`
	Tribe     []TribeMember `json:"tribe"`
	CreatedAt time.Time     `json:"created_at"`
	CreatedBy User          `json:"created_by"`
}

func NewNote(text string) Note {
	return Note{Text: TextValue(text), CreatedAt: time.Now(), Tribe: []TribeMember{}}
}

func (note Note) Clone() ResourceValue {
	return note
}

func (note Note) String() string {
	return fmt.Sprintf("%#v", note)
}

func (note Note) GetDelta(latest ResourceValue) Delta {
	master := latest.(Note)
	delta := NewNoteDelta()
	// calculate TextDelta
	delta.Text = note.Text.GetDelta(master.Text).(TextDelta)
	if note.Title != master.Title {
		delta.Title = [2]string{note.Title, master.Title}
	}
	// TODO(flo) calculate TribeDelta
	//delta.Tribe = note.Tribe.GetDelta(master.Tribe).(TribeDelta)
	return delta
}

func (note Note) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Title string        `json:"title"`
		Text  string        `json:"text"`
		Tribe []TribeMember `json:"tribe"`
	}{note.Title, string(note.Text), note.Tribe})
}

func (note *Note) UnmarshalJSON(from []byte) error {
	if err := json.Unmarshal(from, note); err != nil {
		return err
	}
	return nil
}

type TribeMember struct {
	UID            string     `json:"uid,omitempty"`
	CursorPosition int64      `json:"cursor_pos,omitempty"`
	LastSeen       *time.Time `json:"last_seen,omitempty"`
	LastEdit       *time.Time `json:"last_edit,omitempty"`
}

//type tribePatch struct {
//	action string
//	ref    string
//	obj    *TribeMember
//}

type notePatch struct {
	property string
	payload  interface{}
}

// maybe notify should be a global chan
func (patch notePatch) Patch(val ResourceValue, notify chan<- Event) (ResourceValue, error) {
	var err error
	note := val.(Note)
	newnote := note.Clone().(Note)
	switch patch.property {
	case "text":
		txtpatch, ok := patch.payload.(textPatch)
		if !ok {
			return nil, errors.New("invalid textPatch received")
		}
		newtxt, err := txtpatch.Patch(note.Text, notify)
		if err != nil {
			return nil, err
		}
		newnote.Text = newtxt.(TextValue)
	case "title":
		newnote.Title = patch.payload.(string)
	case "tribe":
		//todo
	}
	return newnote, err
}

type TribeDelta struct {
	Additions     []TribeMember          `json:"add"`
	Removals      []TribeMember          `json:"rem"`
	Modifications map[string]TribeMember `json:"mod"`
}

func (delta TribeDelta) HasChanges() bool {
	return (len(delta.Additions) + len(delta.Removals) + len(delta.Modifications)) > 0
}

type NoteDelta struct {
	Text  TextDelta  `json:"text"`
	Title [2]string  `json:"title"`
	Tribe TribeDelta `json:"tribe"`
}

func NewNoteDelta() NoteDelta {
	return NoteDelta{Tribe: TribeDelta{Modifications: make(map[string]TribeMember)}}
}

func (delta NoteDelta) HasChanges() bool {
	if delta.Text.HasChanges() {
		return true
	}
	if delta.Title[0] != delta.Title[1] {
		return true
	}
	if delta.Tribe.HasChanges() {
		return true
	}
	return false
}

func (delta NoteDelta) Apply(to ResourceValue) (ResourceValue, []Patcher, error) {
	original, ok := to.(Note)
	newres := original.Clone().(Note)
	if !ok {
		return nil, nil, errors.New("cannot apply NoteDelta to non Note")
	}
	var err error
	var tmptxt ResourceValue
	patches := []Patcher{}
	if delta.Text.HasChanges() {
		tmptxt, patches, err = delta.Text.Apply(original.Text)
		if err != nil {
			return nil, nil, err
		}
		newres.Text = tmptxt.(TextValue)
		log.Println("PATCH: ", patches)
		for i := range patches {
			patches[i] = notePatch{"text", patches[i]}
		}
		log.Println("PATCH: ", patches)
	}
	if delta.Title[0] != delta.Title[1] && original.Title == delta.Title[0] {
		newres.Title = delta.Title[1]
		patches = append(patches, notePatch{"title", delta.Title[1]})
	}
	if delta.Tribe.HasChanges() {
		//TODO
		fmt.Println("todo: tribe deltas")
	}
	return newres, patches, nil
}
