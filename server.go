package diffsync

import "database/sql"

type Server struct {
	db *sql.DB
	TokenConsumer
	NotifyListener
	SessionBackend
	*Store
	*SessionHub
}

func NewServer(db *sql.DB, comm chan<- CommRequest) (*Server, error) {
	srv := &Server{db: db}
	srv.NotifyListener = make(NotifyListener, 250)
	srv.Store = NewStore(db, srv.NotifyListener, comm)
	srv.SessionBackend = NewSQLSessions(db)
	srv.SessionHub = NewSessionHub(srv.SessionBackend)
	srv.TokenConsumer = NewHiroTokens(srv.SessionBackend, db)
	return srv, nil
}

func (srv *Server) Run() {
	go srv.SessionHub.Run()
	go srv.NotifyListener.Run(srv.SessionBackend, srv.SessionHub.Inbox())

}

func (srv *Server) anonToken() (string, error) {
	token, hashed := generateToken()
	_, err := srv.db.Exec("INSERT INTO tokens (token, kind) VALUES (?, 'anon')", hashed)
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
