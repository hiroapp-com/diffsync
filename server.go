package diffsync

import (
	"database/sql"
	"errors"
	"time"

	"github.com/hiro/hync/comm"
)

type Server struct {
	db             *sql.DB
	sessionBackend SessionBackend
	sessionHub     *SessionHub
	Store          *Store
	tokenConsumer  *TokenConsumer
}

func NewServer(db *sql.DB, handler comm.Handler) (*Server, error) {
	srv := &Server{db: db}
	srv.Store = NewStore(handler)
	srv.sessionBackend = NewSQLSessions(db)
	srv.sessionHub = NewSessionHub(srv.sessionBackend)
	srv.tokenConsumer = NewTokenConsumer(srv.sessionBackend, srv.sessionHub, db)
	return srv, nil
}

func (srv *Server) Handle(event Event) (err error) {
	event.ctx.ts = time.Now()
	event.ctx.store = srv.Store
	event.ctx.Router = RouteHandler{srv.sessionHub}
	if err = srv.tokenConsumer.Handle(event, RouteHandler{srv.sessionHub}); err != nil {
		event.ctx.LogError(err)
	}
	return
}

func (srv *Server) Run() {
	go srv.sessionHub.Run()

}

func (srv *Server) Token(kind string) (string, error) {
	switch kind {
	case "anon":
		return srv.anonToken()
	}
	return "", errors.New("unknown tokenkind")
}
func (srv *Server) anonToken() (string, error) {
	token, hashed := generateToken()
	_, err := srv.db.Exec("INSERT INTO tokens (token, kind) VALUES ($1, 'anon')", hashed)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (srv *Server) loginToken(uid string) (string, error) {
	token, hashed := generateToken()
	_, err := srv.db.Exec("INSERT INTO tokens (token, kind, uid) VALUES ($1, 'login', $2)", hashed, uid)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (srv *Server) Stop() {
	srv.sessionHub.Stop()
	srv.db.Close()
}
