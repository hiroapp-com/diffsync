package diffsync

import (
)

type Shadow struct {
    res Resource
    backup ResourceValue
    pending []Edit
    SessionClock
}

func (shadow *Shadow) Rollback() {
    shadow.res.ResourceValue = shadow.backup
    shadow.pending = []Edit{}
}

func (shadow *Shadow) UpdatePending(store chan<- Event) error {
    //send "res-load" to store
    res := shadow.res.CloneEmpty()
    // this re
    event, done := NewResLoadEvent(res)
    store <- event
    // after we receive done, our 'res' has been modified inplace
    <- done
    delta, err := shadow.res.GetDelta(res.ResourceValue)
    if err != nil {
        return err
    }
    shadow.pending = append(shadow.pending, Edit{shadow.SessionClock.Clone(), delta})
    shadow.IncSv()
    return nil
}

func (shadow *Shadow) SyncIncoming(edit Edit, res_store chan<- Event) (changed bool, err error){
    // Make sure clocks are in sync or recoverable
    if err := shadow.SyncSvWith(edit, shadow); err != nil {
        return false, err
    }
    pending := make([]Edit, len(shadow.pending))
    for _, instack := range shadow.pending {
        if !edit.Ack(instack) {
            pending = append(pending, instack)
        }
    }
    if dupe, err := shadow.CheckCV(edit); dupe {
        return false, nil
    } else if err != nil {
        return false, err
    }
    fake_notify := make(chan Event)
    patch, err := shadow.res.ApplyDelta(edit.delta, fake_notify)
    shadow.backup = shadow.res.ResourceValue
    if err != nil {
        return false, err
    }
    shadow.IncCv()
    if patch == nil {
        // no changes, we're finished
        return false, nil
    }
    // TODO send res-patch down to res_store
    return true, nil
}
