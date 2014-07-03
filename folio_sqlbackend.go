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

func (backend FolioSQLBackend) Patch(uid string, patch Patch, store *Store, ctx context) error {
	log.Printf("received Folio-Patch: %#v", patch)
	switch patch.Op {
	case "rem-noteref":
		// patch.Path contains Note ID
		// patch.Value empty
		// patch.OldValue empty
		_, err := backend.db.Exec("DELETE FROM noteref WHERE nid = ? AND uid = ?", patch.Path, uid)
		if err != nil {
			return err
		}
		// TODO(taint): note(nid) & folio(uid)
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
	case "add-noteref":
		// patch.Path empty
		// patch.Value contains new NoteRef value
		// patch.OldValue contains old Status for CAS
		note := patch.Value.(NoteRef)
		// TODO(flo) check if note with ID already exists. for no just checking against tmp ids
		role := "active"
		if len(note.NID) < 5 {
			// save blank note with new NID
			newnote, err := store.NewResource("note", ctx)
			if err != nil {
				return err
			}
			note.tmpNID = note.NID
			note.NID = newnote.ID
			role = "owner"
		}
		// TODO(flo) check permissin?
		// fire and forgeeeeet
		// (uid, nid) needs to be unique key.
		// TBD: should we just ignore the inserts if the constraints fail? (i.e. noteref already exists for user)
		backend.db.Exec("INSERT INTO noterefs (uid, nid, tmp_nid, status, role) VALUES(?, ?, ?, ?, ?)", uid, note.NID, note.tmpNID, note.Status, role)
		store.NotifyReset("note", note.NID, ctx)
	}
	return nil
}

func (backend FolioSQLBackend) CreateEmpty(ctx context) (string, error) {
	return "", fmt.Errorf("folioSQLbackend: one does not simply createn a new Folio (use Get(uid) instead)")
}
