package server

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/fosslife/drive/internal/auth"
	"github.com/fosslife/drive/internal/files"
)

// results is the shape of a /api/search response.
type results struct {
	Query     string        `json:"query"`
	Entries   []files.Entry `json:"entries"`
	Truncated bool          `json:"truncated"`
}

func (h *harness) search(t *testing.T, secret, term string, limit int) results {
	t.Helper()
	path := "/api/search?q=" + url.QueryEscape(term)
	if limit > 0 {
		path += fmt.Sprintf("&limit=%d", limit)
	}
	w := h.as(secret, "GET", path, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("searching %q: %d %s", term, w.Code, w.Body.String())
	}
	return decodeBody[results](t, w)
}

func paths(entries []files.Entry) []string {
	found := make([]string, 0, len(entries))
	for _, e := range entries {
		found = append(found, e.Path)
	}
	return found
}

func hasPath(entries []files.Entry, want string) bool {
	for _, e := range entries {
		if e.Path == want {
			return true
		}
	}
	return false
}

// withSearchable gives ada a tree worth searching and bob one file that must
// never turn up in her results.
func withSearchable(t *testing.T) (h *harness, ada *auth.User, adasToken, bobsToken string) {
	t.Helper()
	h = newHarness(t)
	ada = h.account("ada", false)
	for _, name := range []string{"taxes.pdf", "docs/tax-2025.xlsx", "docs/TAXI-receipt.png", "notes.txt"} {
		h.put(ada, name, "content of "+name)
	}
	h.scan(ada)

	bob := h.account("bob", false)
	h.put(bob, "taxes.pdf", "bob's taxes")
	h.scan(bob)
	return h, ada, h.token(ada), h.token(bob)
}

// 10.1: a partial term matches anywhere in the name, whatever the case, and the
// results say where each file is.
func TestSearchMatchesPartialNamesAndReturnsFullPaths(t *testing.T) {
	h, _, secret, _ := withSearchable(t)

	got := h.search(t, secret, "tax", 0)
	for _, want := range []string{"taxes.pdf", "docs/tax-2025.xlsx", "docs/TAXI-receipt.png"} {
		if !hasPath(got.Entries, want) {
			t.Errorf("searching tax found %v, missing %s", paths(got.Entries), want)
		}
	}
	if hasPath(got.Entries, "notes.txt") {
		t.Errorf("searching tax found %v, which includes a file that does not match", paths(got.Entries))
	}
	if got.Truncated {
		t.Error("three results out of a default page are reported as truncated")
	}

	// The match is a substring, not a prefix, and folders are findable too.
	if got := h.search(t, secret, "receipt", 0); !hasPath(got.Entries, "docs/TAXI-receipt.png") {
		t.Errorf("searching a term from the middle of a name found %v", paths(got.Entries))
	}
	if got := h.search(t, secret, "doc", 0); !hasPath(got.Entries, "docs") {
		t.Errorf("searching for a folder found %v", paths(got.Entries))
	}

	if w := h.as(secret, "GET", "/api/search?q=", nil); w.Code != http.StatusBadRequest {
		t.Errorf("an empty search: %d, want 400 rather than every file", w.Code)
	}
}

// 10.1: search is one user's own files, and only the ones they can still see.
func TestSearchExcludesOtherUsersAndTrashedItems(t *testing.T) {
	h, _, secret, bobsToken := withSearchable(t)

	// Ada and bob each have a taxes.pdf; each sees exactly their own.
	if got := h.search(t, secret, "taxes", 0); len(got.Entries) != 1 {
		t.Errorf("ada's search for taxes found %v, want only her own", paths(got.Entries))
	}
	if got := h.search(t, bobsToken, "tax", 0); len(got.Entries) != 1 || got.Entries[0].Path != "taxes.pdf" {
		t.Errorf("bob's search found %v, want only his own taxes.pdf", paths(got.Entries))
	}
	if got := h.search(t, bobsToken, "2025", 0); len(got.Entries) != 0 {
		t.Errorf("bob's search found ada's files: %v", paths(got.Entries))
	}

	h.delete(t, secret, "docs/tax-2025.xlsx")
	got := h.search(t, secret, "tax", 0)
	if hasPath(got.Entries, "docs/tax-2025.xlsx") {
		t.Errorf("searching found a trashed file: %v", paths(got.Entries))
	}
	if !hasPath(got.Entries, "taxes.pdf") {
		t.Errorf("deleting one match removed the others: %v", paths(got.Entries))
	}
}

// 10.2: the response is one bounded page and says when there is more.
func TestSearchIsBoundedAndSignalsTruncation(t *testing.T) {
	h, ada, secret, _ := withSearchable(t)
	for i := range 40 {
		h.put(ada, fmt.Sprintf("bulk/tax-note-%02d.txt", i), "more taxes")
	}
	h.scan(ada)

	got := h.search(t, secret, "tax", 10)
	if len(got.Entries) != 10 {
		t.Errorf("a limit of 10 returned %d results", len(got.Entries))
	}
	if !got.Truncated {
		t.Error("a capped result set does not signal that more matches exist")
	}

	// Asking for more than there is says so, which is how a client knows it has
	// seen everything.
	if got := h.search(t, secret, "tax", 500); got.Truncated || len(got.Entries) != 43 {
		t.Errorf("%d results, truncated %v, want all 43 and no truncation", len(got.Entries), got.Truncated)
	}
	// A limit beyond the ceiling is capped rather than honoured: the page is
	// what bounds the response, not the caller.
	if got := h.search(t, secret, "tax", 1_000_000); len(got.Entries) > files.MaxPageSize {
		t.Errorf("a limit of a million returned %d results", len(got.Entries))
	}
}

// 10.1: a search term is a term, not a pattern. "%" matching everything would
// be a way to enumerate a root one character at a time.
func TestSearchTreatsWildcardsAsLiteralText(t *testing.T) {
	h, ada, secret, _ := withSearchable(t)
	h.put(ada, "100% done.txt", "finished")
	h.put(ada, "a_b.txt", "underscored")
	h.scan(ada)

	if got := h.search(t, secret, "%", 0); len(got.Entries) != 1 || got.Entries[0].Path != "100% done.txt" {
		t.Errorf("searching %% found %v, want only the file with a %% in its name", paths(got.Entries))
	}
	// "_" is LIKE's single-character wildcard: unescaped it matches every name.
	if got := h.search(t, secret, "a_b", 0); len(got.Entries) != 1 || got.Entries[0].Path != "a_b.txt" {
		t.Errorf("searching a_b found %v, want only a_b.txt", paths(got.Entries))
	}
}
