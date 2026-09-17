package files

import (
	"fmt"
	"strings"

	"github.com/fosslife/drive/internal/index"
)

// Search finds a user's files and folders whose name contains term, ignoring
// case, and returns them with their full paths. Trashed items are excluded, and
// so is every other account: the user_id is in the query, not in a filter the
// caller could forget.
//
// more reports that there were further matches beyond limit. It comes from
// asking for one row more than the caller wanted, which is cheaper than
// counting the whole match set to answer "is there a page two".
//
// ponytail: a LIKE scan over the user's rows, no FTS. A second engine buys a
// word index for something that is not a word search — "tax" has to match
// "2025-taxes.pdf" — and costs the single-binary install. Case-insensitivity is
// SQLite's, which is ASCII-only: "Ä" does not match "ä". Fix by storing a
// folded copy of the name if anyone ever needs it.
func Search(db *index.DB, userID int64, term string, limit int) (entries []Entry, more bool, err error) {
	term = strings.TrimSpace(term)
	if term == "" {
		return nil, false, fmt.Errorf("%w: no search term", ErrInvalid)
	}
	if limit <= 0 || limit > MaxPageSize {
		limit = DefaultPageSize
	}

	rs, err := db.Query(`SELECT `+columns+` FROM files
	                     WHERE user_id = ? AND state = 'present' AND name LIKE ? ESCAPE '\'
	                     ORDER BY dir, name LIMIT ?`,
		userID, "%"+escapeLike(term)+"%", limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("searching for %q: %w", term, err)
	}
	defer rs.Close()

	entries = []Entry{}
	for rs.Next() {
		e, err := scanEntry(rs)
		if err != nil {
			return nil, false, fmt.Errorf("searching for %q: %w", term, err)
		}
		entries = append(entries, e)
	}
	if err := rs.Err(); err != nil {
		return nil, false, fmt.Errorf("searching for %q: %w", term, err)
	}
	if len(entries) > limit {
		return entries[:limit], true, nil
	}
	return entries, false, nil
}

// escapeLike neutralises the wildcards in a search term. Without it, searching
// for "50%" matches every file the user has.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func escapeLike(term string) string { return likeEscaper.Replace(term) }
