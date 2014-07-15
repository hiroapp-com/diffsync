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
	GetSubscriptions(Resource) ([][2]string, error)
}

type NotifyListener chan Event

func (notify NotifyListener) Run(subscriptions SubscriberChecker, sesshub chan<- Event) {
	log.Printf("notify (%v) starting up ...", notify)
	for event := range notify {
		log.Println("notify: received", event)
		switch event.Name {
		case "res-taint", "res-reset":
			break
		default:
			log.Printf("notify: received event that i cannot handle (name: `%s`), doing nothing.", event.Name)
			continue
		}
		if event.SID != "" {
			// addressed directly to a certain session, send only there
			sesshub <- Event{Name: event.Name, SID: event.SID, Res: event.Res}
			continue
		}
		subs, err := subscriptions.GetSubscriptions(event.Res)
		if err != nil {
			continue
		}
		for i := range subs {
			log.Printf("notify: found subscribed session (%s), forwarding event to inbox.\n", subs[i])
			res := event.Res
			switch res.Kind {
			case "folio", "profile":
				res.ID = subs[i][1]
			}
			sesshub <- Event{Name: event.Name, SID: subs[i][0], Res: res, store: event.store, ctx: event.ctx}
		}
	}
	log.Printf("notify (%v) channel closed, shutting down, notify")
}

func NewNotifyListener(buffer int) NotifyListener {
	return NotifyListener(make(chan Event, buffer))
}
