package diffsync

import (
	"encoding/json"
	"fmt"
	DMP "github.com/sergi/go-diff/diffmatchpatch"
	"log"
)

var (
	_ = log.Print
)

type NoteValue string
type NoteDelta string

func NewNoteValue(text string) *NoteValue {
	nv := NoteValue(text)
	return &nv
}

func (note *NoteValue) CloneValue() ResourceValue {
	return NewNoteValue(string(*note))
}

//note maybe make notify a global chan
func (note *NoteValue) ApplyDelta(delta Delta) (Patch, error) {
	original := string(*note)
	diffs, err := dmp.DiffFromDelta(original, string(delta.(NoteDelta)))
	if err != nil {
		return Patch{}, err
	}
	patch := dmp.PatchMake(original, diffs)
	*note = NoteValue(dmp.DiffText2(diffs))
	if original == string(*note) {
		// nil-value indicates that no changes happened
		// todo doc this behaviour nearby Patch definition
		return Patch{val: nil}, nil
	}
	return Patch{val: patch[0]}, nil
}

// maybe notify should be a global chan
func (note *NoteValue) ApplyPatch(patch Patch, notify chan<- Event) (changed bool, err error) {
	patched_str, _ := dmp.PatchApply([]DMP.Patch{patch.val.(DMP.Patch)}, string(*note))
	changed = string(*note) != patched_str
	*note = NoteValue(patched_str)
	// more logical patches (like meta) could send res-taint events to notify after modifying others' resources (e.g. folio)
	return changed, nil
}

func (note *NoteValue) GetDelta(latest ResourceValue) (Delta, error) {
	master, ok := latest.(*NoteValue)
	if !ok {
		return nil, fmt.Errorf("received illegal master-value for delta calculation")
	}
	diffs := dmp.DiffMain(string(*note), string(*master), false)
	diffs = dmp.DiffCleanupEfficiency(diffs)
	return NoteDelta(dmp.DiffToDelta(diffs)), nil
}

func (note *NoteValue) String() string {
	return string(*note)
}

func (delta NoteDelta) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(delta))
}

func (note *NoteValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(string(*note))
}

func (note *NoteValue) UnmarshalJSON(from []byte) error {
	var s string
	if err := json.Unmarshal(from, &s); err != nil {
		return err
	}
	*note = NoteValue(s)
	return nil
}
