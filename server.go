package diffsync

import (
	"database/sql"
	"errors"

	"github.com/hiro/hync/comm"
)

type Server struct {
	db *sql.DB
	TokenConsumer
	NotifyListener
	SessionBackend
	*Store
	*SessionHub
}

func NewServer(db *sql.DB, handler comm.Handler) (*Server, error) {
	srv := &Server{db: db}
	srv.NotifyListener = make(NotifyListener, 250)
	srv.Store = NewStore(srv.NotifyListener, handler)
	srv.SessionBackend = NewSQLSessions(db)
	srv.SessionHub = NewSessionHub(srv.SessionBackend)
	srv.TokenConsumer = NewHiroTokens(srv.SessionBackend, srv.SessionHub, db)
	return srv, nil
}

func (srv *Server) Run() {
	go srv.SessionHub.Run()
	go srv.NotifyListener.Run(srv.SessionBackend, srv.SessionHub.Inbox())

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
	_, err := srv.db.Exec("INSERT INTO tokens (token, kind) VALUES (?, 'anon')", hashed)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (srv *Server) loginToken(uid string) (string, error) {
	token, hashed := generateToken()
	_, err := srv.db.Exec("INSERT INTO tokens (token, kind, uid) VALUES (?, 'login', ?)", hashed, uid)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (srv *Server) NewConn() *Conn {
	return NewConn(srv.SessionHub.Inbox(), srv.TokenConsumer, NewJsonAdapter(), srv.Store)
}

func (srv *Server) Close() {
	close(srv.SessionHub.Inbox())
	close(srv.NotifyListener)
	srv.db.Close()
}
