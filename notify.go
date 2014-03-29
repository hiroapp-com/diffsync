package diffsync

import (
	"log"
)

type Subscription struct {
	sid, uid string
	// pushid string
	res Resource
}

type SubscriberChecker interface {
	GetSubscriptions(Subscription) []Subscription
}

func (sub Subscription) CloneFor(sid, uid string) Subscription {
	// todo make sure we canuse `sub` for this
	log.Println("WWWOOOPP", sub, sid, uid)
	return Subscription{sid: sid, uid: uid, res: sub.res.CloneEmpty()}
}

type NotifyListener chan Event

//func (notify NotifyListener) notify_iospush(event Event)

func (notify NotifyListener) Run(subs SubscriberChecker, sesshub chan<- Event) {
	log.Printf("notify (%v) starting up ...", notify)
	for event := range notify {
		log.Println("notify: received", event)
		switch event.Name {
		case "res-taint":
		case "res-reset":
			break
		default:
			log.Printf("notify: received event that i cannot handle (name: `%s`), doing nothing.", event.Name)
			continue
		}
		uid_subs := map[string]struct{}{}
		for _, sub := range subs.GetSubscriptions(Subscription{res: *event.Res}) {
			log.Println("notify: found subscriber ", sub)
			if sub.uid != "" {
				// aggregate UIDs. We might get multiple sessions for same user
				uid_subs[sub.uid] = struct{}{}
			}
			if sub.sid != "" && sub.sid != event.SID {
				// forward Event to session
				log.Println("notify: sending event to sessub-inbox")
				sesshub <- Event{Name: event.Name, SID: sub.sid, Res: event.Res}
			}
		}
		//for uid, _ := range uid_subs {
		// not implemented
		// check if user had "ONLINE" session and already seen the update. the
		// connection layer will be adapted to keep a registry of currently active
		// notify user "
		//}

	}
	log.Printf("notify (%v) channel closed, shutting down, notify")
}

func NewNotifyListener(buffer int) NotifyListener {
	return NotifyListener(make(chan Event, buffer))
}
