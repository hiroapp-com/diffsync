package diffsync

import (
	DMP "github.com/sergi/go-diff/diffmatchpatch"
)

type NoteValue string

func NewNoteValue(text string) NoteValue {
	return NoteValue("")
}

//note maybe make notify a global chan
func (note *NoteValue) ApplyDelta(delta Delta) (Patch, error) {
	original := string(*note)
	diffs, err := dmp.DiffFromDelta(original, delta.(string))
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
	return Patch{val: patch}, nil
}

// maybe notify should be a global chan
func (note *NoteValue) ApplyPatch(patch Patch, notify chan<- Event) (changed bool, err error) {
	patched_str, _ := dmp.PatchApply([]DMP.Patch{patch.val.(DMP.Patch)}, string(*note))
	changed = string(*note) != patched_str
	*note = NoteValue(patched_str)
	// more logical patches (like meta) could send res-taint events to notify after modifying others' resources (e.g. folio)
	return changed, nil
}

func (note *NoteValue) GetDelta(other ResourceValue) (Delta, error) {
	return "", nil
}

func (note *NoteValue) MarshalJSON() ([]byte, error) {
	return []byte(*note), nil
}

func (note *NoteValue) UnmarshalJSON(from []byte) error {
	*note = NoteValue(from)
	return nil
}
