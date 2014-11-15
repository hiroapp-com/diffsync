package diffsync

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/sushimako/rollbar"
)

type Context struct {
	sid    string
	uid    string
	ts     time.Time
	store  *Store
	Router EventHandler
	Client EventHandler
}

func NewContext(router EventHandler, store *Store, client EventHandler) Context {
	return Context{
		ts:     time.Now(),
		store:  store,
		Router: router,
		Client: client,
	}
}

func (c Context) User() User {
	if c.store == nil {
		return User{UID: c.uid}
	}
	r := Resource{Kind: "profile", ID: c.uid}
	if err := c.store.Load(&r); err != nil {
		return User{UID: c.uid}
	}
	return r.Value.(Profile).User
}

func (c Context) Clone() Context {
	return c
}

func (c Context) LogError(err error) {
	log.Println("ERROR", err, personFromUser(c.User()))
	rollbar.Error(rollbar.ERR, err, personFromUser(c.User()))
}

func (c Context) LogCritical(err error) {
	log.Println("CRITICAL", err, personFromUser(c.User()))
	rollbar.Error(rollbar.CRIT, err, personFromUser(c.User()))
}

func (c Context) LogInfo(msg string, args ...interface{}) {
	log.Println("INFO", msg, args)
	rollbar.Message(rollbar.INFO, fmt.Sprintf(msg, args...), personFromUser(c.User()))
}

func personFromUser(u User) rollbar.Person {
	return rollbar.Person{
		ID:       u.UID,
		Username: firstNonEmpty(u.Name, u.Email, u.Phone),
		Email:    u.Email,
	}
}

func init() {
	rollbar.Plattform = "hync"
	rollbar.Token = os.Getenv("ROLLBAR_TOKEN")
	env := os.Getenv("ROLLBAR_ENV")
	if env != "" {
		rollbar.Environment = env
	}
	rollbar.Message(rollbar.INFO, "hync started", rollbar.Person{})
}
