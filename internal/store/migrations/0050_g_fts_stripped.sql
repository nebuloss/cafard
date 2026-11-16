-- Track G: the full-text index holds the text without ANSI escapes and
-- control characters, so "error" finds "\x1b[31merror\x1b[0m". The
-- stripping is cafard_fts_text, a Go function the store registers on
-- every connection (store/search.go); entries.text keeps the raw bytes.
-- Only the writer inserts or deletes entries, so the triggers still run
-- in its transactions only.

DROP TRIGGER IF EXISTS entries_fts_insert;
DROP TRIGGER IF EXISTS entries_fts_delete;
DROP TRIGGER IF EXISTS entries_fts_update;

INSERT INTO entries_fts(entries_fts) VALUES ('delete-all');
INSERT INTO entries_fts(rowid, text) SELECT seq, cafard_fts_text(text) FROM entries;

CREATE TRIGGER IF NOT EXISTS entries_fts_insert AFTER INSERT ON entries BEGIN
  INSERT INTO entries_fts(rowid, text) VALUES (new.seq, cafard_fts_text(new.text));
END;

CREATE TRIGGER IF NOT EXISTS entries_fts_delete AFTER DELETE ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, text) VALUES ('delete', old.seq, cafard_fts_text(old.text));
END;

CREATE TRIGGER IF NOT EXISTS entries_fts_update AFTER UPDATE OF text ON entries BEGIN
  INSERT INTO entries_fts(entries_fts, rowid, text) VALUES ('delete', old.seq, cafard_fts_text(old.text));
  INSERT INTO entries_fts(rowid, text) VALUES (new.seq, cafard_fts_text(new.text));
END;
