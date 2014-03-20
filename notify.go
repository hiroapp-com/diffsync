package diffsync

import (
    "log"
)

var (
    notify NotifyListener
)

type NotifyListener chan Event 

func (notify NotifyListener) Run() {
    log.Printf("notify (%v) starting up ...", notify)
    for event := range notify {
        log.Printf("notify (%v) received: %v", notify, event)
    }
    log.Printf("notify (%v) channel closed, shutting down, notify")
}

func NewNotifyListener(buffer int) NotifyListener {
    return NotifyListener(make(chan Event, buffer))
}


func init() {
    notify = NewNotifyListener(128)
    // this should be in the main app at a determined point
    go notify.Run()
}


