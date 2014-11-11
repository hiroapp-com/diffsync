package diffsync

import (
	"fmt"
	"log"

	"database/sql"
)

var (
	_ = log.Print
)

type FolioSQLBackend struct {
	db *sql.DB
}

func NewFolioSQLBackend(db *sql.DB) FolioSQLBackend {
	return FolioSQLBackend{db}
}

func (backend FolioSQLBackend) Get(uid string) (ResourceValue, error) {
	folio := Folio{}
	rows, err := backend.db.Query("SELECT nid, status, tmp_nid FROM noterefs WHERE uid = $1 ", uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		noteRef := NoteRef{}
		if err := rows.Scan(&noteRef.NID, &noteRef.Status, &noteRef.tmpNID); err != nil {
			return nil, err
		}
		folio = append(folio, noteRef)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return folio, nil
}

func (backend FolioSQLBackend) Patch(uid string, patch Patch, result *SyncResult, ctx Context) error {
	switch patch.Op {
	case "rem-noteref":
		// patch.Path contains Note ID
		// patch.Value empty
		// patch.OldValue empty
		_, err := backend.db.Exec("DELETE FROM noteref WHERE nid = $1 AND uid = $2", patch.Path, uid)
		if err != nil {
			return err
		}
		ctx.Router.Handle(Event{UID: uid, Name: "res-remove", Res: Resource{Kind: "note", ID: patch.Path}, ctx: ctx})
		result.Tainted(Resource{Kind: "folio", ID: uid})
		result.Tainted(Resource{Kind: "note", ID: patch.Path})
	case "set-status":
		// patch.Path contains Note ID
		// patch.Value contains new Status
		// patch.OldValue contains old Status for CAS
		status := patch.Value.(string)
		if !(status == "active" || status == "archived") {
			return fmt.Errorf("folioSQLbackend: received invalid status: %s", status)
		}
		_, err := backend.db.Exec("UPDATE noterefs SET status = $1 WHERE uid = $2 and nid = $3 and status = $4", status, uid, patch.Path, patch.OldValue.(string))
		if err != nil {
			return fmt.Errorf("folioSQLbackend: uid(%s) status change for nid(%s): could not persist new status: `%s`", uid, patch.Path, status)
		}
		result.Tainted(Resource{Kind: "folio", ID: uid})
	case "add-noteref":
		// patch.Path empty
		// patch.Value contains new NoteRef value
		// patch.OldValue empty
		ref := patch.Value.(NoteRef)
		var res sql.Result
		var err error
		if len(ref.NID) < 5 {
			// save blank note with new NID
			newnote, err := ctx.store.NewResource("note", ctx)
			if err != nil {
				return err
			}
			ref.tmpNID = ref.NID
			ref.NID = newnote.ID
			if res, err = backend.db.Exec("UPDATE noterefs SET tmp_nid = $1, status = $2, role = 'owner' WHERE uid = $3 and nid = $4",
				ref.tmpNID,
				ref.Status,
				uid,
				ref.NID,
			); err != nil {
				return err
			}
		} else {
			// add existing note to folio
			if res, err = backend.db.Exec("INSERT INTO noterefs (uid, nid, status, role) VALUES ($1, $2, 'active', 'peer')", uid, ref.NID); err != nil {
				return err
			}
		}
		if added, _ := res.RowsAffected(); added > 0 {
			// TODO(flo) check permissin?
			if err = ctx.Router.Handle(Event{UID: uid, Name: "res-add", Res: Resource{Kind: "note", ID: ref.NID}, ctx: ctx}); err != nil {
				return err
			}
			result.Tainted(Resource{Kind: "profile", ID: uid})
			result.Tainted(Resource{Kind: "folio", ID: uid})
			result.Tainted(Resource{Kind: "note", ID: ref.NID})
		}
	}
	return nil
}

func (backend FolioSQLBackend) CreateEmpty(ctx Context) (string, error) {
	return "", fmt.Errorf("folioSQLbackend: one does not simply createn a new Folio (use Get(uid) instead)")
}
