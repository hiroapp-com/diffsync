package diffsync 

import (
//    DMP "github.com/sergi/go-diff/diffmatchpatch"
)

type NoteValue string

func NewNoteValue(text string) NoteValue {
    return NoteValue("")
}


//func (note *NoteValue) ApplyDelta(delta Delta, notify chan<- Event)  (Patch, error) {
//    strnote := string(*note)
//    diffs, err := dmp.DiffFromDelta(strnote, delta.(string))
//    if err != nil {
//        return nil, err
//    }
//    patch := dmp.PatchMake(strnote, diffs)
//    *note = NoteValue(dmp.DiffText2(diffs))
//    return patch, nil
//}
//
//func (note *NoteValue) ApplyPatch(patch Patch) (patched_note ResourceValue, changed bool, err error) {
//    patched_str, _ := dmp.PatchApply([]DMP.Patch{patch.(DMP.Patch)}, string(*note))
//    changed = string(*note) != patched_str
//    patched := NoteValue(patched_str)
//    return &patched, changed, nil 
//}
