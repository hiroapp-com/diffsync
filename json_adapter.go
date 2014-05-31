package diffsync

import (
	"encoding/json"
	"log"
)

type jsonAdapter struct {
	buf jsonMsg
}

func NewJsonAdapter() MessageAdapter {
	return jsonAdapter{buf: jsonMsg{}}
}

func (a jsonAdapter) MsgToEvent(from []byte) (Event, error) {
	log.Printf("jsonAdapter: parsing message: %s\n", from)
	err := json.Unmarshal(from, &a.buf)
	if err != nil {
		return Event{}, err
	}
	log.Printf("jsonAdapter: parsed object %v\n", a.buf)
	ev := Event{
		Name:  a.buf.Name,
		SID:   a.buf.SID,
		Tag:   a.buf.Tag,
		Token: a.buf.Token,
	}
	if a.buf.Res == nil {
		return ev, nil
	}
	ev.Res = Resource{Kind: a.buf.Res.Kind, ID: a.buf.Res.ID}
	if a.buf.Res.Value != nil {
		log.Println("TODO: res.value in incoming payload")
	}
	if a.buf.Name == "res-sync" && a.buf.Changes != nil {
		ev.Changes = make([]Edit, len(a.buf.Changes))
		for i, c := range a.buf.Changes {
			d, err := deltas[a.buf.Res.Kind](c.RawDelta)
			if err != nil {
				return Event{}, err
			}
			ev.Changes[i] = Edit{c.Clock, d}
		}
	}
	return ev, nil
}

func (a jsonAdapter) EventToMsg(ev Event) ([]byte, error) {
	a.buf.Name = ev.Name
	a.buf.SID = ev.SID
	a.buf.Tag = ev.Tag
	a.buf.Token = ev.Token
	a.buf.Changes = make([]jsonEdit, len(ev.Changes))
	for i, edit := range ev.Changes {
		rawDelta, err := json.Marshal(edit.Delta)
		if err != nil {
			return nil, err
		}
		a.buf.Changes[i] = jsonEdit{edit.Clock, json.RawMessage(rawDelta)}
	}
	if ev.Res.ID != "" {
		a.buf.Res = &jsonResource{
			Kind: ev.Res.Kind,
			ID:   ev.Res.ID,
		}
		if ev.Res.Value != nil {
			val, err := json.Marshal(ev.Res.Value)
			if err != nil {
				return nil, err
			}
			a.buf.Res.Value = json.RawMessage(val)
		}
	}
	if ev.Session != nil {
		a.buf.Session = jsonSession(ev.Session)
	}
	return json.Marshal(a.buf)
}

func (a jsonAdapter) Mux(msgs [][]byte) ([]byte, error) {
	res := []byte("[")
	for i := range msgs {
		res = append(res, msgs[i]...)
		res = append(res, byte(','))
	}
	res[len(res)-1] = byte(']')
	return res, nil
}

func (a jsonAdapter) Demux(msg []byte) ([][]byte, error) {
	tmp := []json.RawMessage{}
	if err := json.Unmarshal(msg, &tmp); err != nil {
		return nil, err
	}
	res := make([][]byte, len(tmp))
	for i := range tmp {
		res[i] = []byte(tmp[i])
	}
	return res, nil
}

type jsonEdit struct {
	Clock    SessionClock    `json:"clock"`
	RawDelta json.RawMessage `json:"delta"`
}

type jsonResource struct {
	Kind  string          `json:"kind"`
	ID    string          `json:"id"`
	Value json.RawMessage `json:"val,omitempty"`
}

type jsonMsg struct {
	Name    string                 `json:"name"`
	SID     string                 `json:"sid"`
	Tag     string                 `json:"tag, omitempty"`
	Token   string                 `json:"token,omitempty"`
	Changes []jsonEdit             `json:"changes,omitempty"`
	Res     *jsonResource          `json:"res,omitempty"`
	Session map[string]interface{} `json:"session,omitempty"`
}

var deltas = map[string]func([]byte) (Delta, error){
	"note": func(from []byte) (Delta, error) {
		log.Printf("jsonAdapter: parsing note delta\n")
		delta := NewNoteDelta()
		if err := json.Unmarshal(from, &delta); err != nil {
			return nil, err
		}
		log.Println("jsonAdapter: parsed note delta", delta)
		return delta, nil
	},
	"folio": func(from []byte) (Delta, error) {
		log.Printf("jsonAdapter: parsing folio delta\n")
		delta := NewFolioDelta()
		if err := json.Unmarshal(from, &delta); err != nil {
			return nil, err
		}
		return delta, nil
	},
}

func jsonSession(sess *Session) map[string]interface{} {
	folio := Resource{}
	notes := make(map[string]*Resource)

	for _, shadow := range sess.shadows {
		switch shadow.res.Kind {
		case "folio":
			folio = shadow.res
		case "note":
			notes[shadow.res.ID] = &shadow.res
		default:
		}

	}
	return map[string]interface{}{
		"sid":   sess.id,
		"uid":   sess.uid,
		"folio": folio,
		"notes": notes,
	}
}
