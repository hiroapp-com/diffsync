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
	rows, err := backend.db.Query("SELECT nid, status, tmp_nid FROM noterefs WHERE uid = ? ", uid)
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
		_, err := backend.db.Exec("DELETE FROM noteref WHERE nid = ? AND uid = ?", patch.Path, uid)
		if err != nil {
			return err
		}
		result.Removed(Resource{Kind: "note", ID: patch.Path})
		result.Taint(Resource{Kind: "note", ID: patch.Path})
		result.Taint(Resource{Kind: "folio", ID: uid})
	case "set-status":
		// patch.Path contains Note ID
		// patch.Value contains new Status
		// patch.OldValue contains old Status for CAS
		status := patch.Value.(string)
		if !(status == "active" || status == "archived") {
			return fmt.Errorf("folioSQLbackend: received invalid status: %s", status)
		}
		_, err := backend.db.Exec("UPDATE noterefs SET status = ? WHERE nid = ? and status = ?", status, patch.Path, patch.OldValue.(string))
		if err != nil {
			return fmt.Errorf("folioSQLbackend: uid(%s) status change for nid(%s): could not persist new status: `%s`", uid, patch.Path, status)
		}
		result.Taint(Resource{Kind: "folio", ID: uid})
	case "add-noteref":
		// patch.Path empty
		// patch.Value contains new NoteRef value
		// patch.OldValue contains old Status for CAS
		noteref := patch.Value.(NoteRef)
		// TODO(flo) check if note with ID already exists. for no just checking against tmp ids
		role := "active"
		if len(noteref.NID) < 5 {
			// save blank note with new NID
			newnote, err := ctx.store.NewResource("note", ctx)
			if err != nil {
				return err
			}
			noteref.tmpNID = noteref.NID
			noteref.NID = newnote.ID
			role = "owner"
		}
		// TODO(flo) check permissin?
		// fire and forgeeeeet
		backend.db.Exec("UPDATE noterefs SET tmp_nid = ?, status = ?, role = ? WHERE uid = ? and nid = ?", noteref.tmpNID, noteref.Status, role, uid, noteref.NID)
		result.Taint(Resource{Kind: "folio", ID: uid})
		result.Reset(Resource{Kind: "note", ID: noteref.NID})
		result.Taint(Resource{Kind: "note", ID: noteref.NID})
	}
	return nil
}

func (backend FolioSQLBackend) CreateEmpty(ctx Context) (string, error) {
	return "", fmt.Errorf("folioSQLbackend: one does not simply createn a new Folio (use Get(uid) instead)")
}
