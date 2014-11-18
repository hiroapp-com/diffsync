CREATE OR REPLACE RULE noterefs_ignore_dupe_update_nid_uid AS
ON UPDATE TO noterefs
WHERE (EXISTS ( SELECT 1
        FROM noterefs
        WHERE noterefs.uid = NEW.uid  AND noterefs.nid = NEW.nid AND OLD.uid <> NEW.uid)) DO INSTEAD DELETE FROM noterefs WHERE uid = OLD.uid AND nid = OLD.nid;
