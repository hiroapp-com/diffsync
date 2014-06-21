package diffsync

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"encoding/json"

	DMP "github.com/sergi/go-diff/diffmatchpatch"
)

var (
	dmp = DMP.New()
	_   = log.Print
	_   = fmt.Print
)

type TextValue string
type TextDelta string
type textPatch []DMP.Patch

func NewTextValue(text string) *TextValue {
	nv := TextValue(text)
	return &nv
}

func (txt TextValue) Empty() ResourceValue {
	return NewTextValue("")
}
func (note TextValue) Clone() ResourceValue {
	return note
}

func (note TextValue) String() string {
	return string(note)
}

func (note TextValue) GetDelta(latest ResourceValue) Delta {
	master := latest.(TextValue)
	diffs := dmp.DiffMain(string(note), string(master), false)
	diffs = dmp.DiffCleanupEfficiency(diffs)
	return TextDelta(dmp.DiffToDelta(diffs))
}

func (note TextValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(note))
}

func (note *TextValue) UnmarshalJSON(from []byte) error {
	var s string
	if err := json.Unmarshal(from, &s); err != nil {
		return err
	}
	*note = TextValue(s)
	return nil
}

func (patch textPatch) Patch(val ResourceValue, store *Store) (ResourceValue, error) {
	original := val.(TextValue)
	patched, _ := dmp.PatchApply([]DMP.Patch(patch), string(original))
	return TextValue(patched), nil
}

func (delta TextDelta) HasChanges() bool {
	log.Println("DELTA", delta)
	if string(delta) == "" {
		return false
	}
	return string(delta)[0] != '=' || len(strings.SplitN(string(delta), "\t", 2)) > 1
}

func (delta TextDelta) Apply(to ResourceValue) (ResourceValue, []Patcher, error) {
	original, ok := to.(TextValue)
	if !ok {
		return nil, nil, errors.New("Cannot apply delta to non-TextValue type")
	}
	diffs, err := dmp.DiffFromDelta(string(original), string(delta))
	if err != nil {
		return nil, nil, err
	}
	patch := textPatch(dmp.PatchMake(string(original), diffs))
	newres := TextValue(dmp.DiffText2(diffs))
	return newres, []Patcher{patch}, nil
}
