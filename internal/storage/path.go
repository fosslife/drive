package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// Internal is the per-root directory holding in-flight uploads, trash, and the
// thumbnail cache. It is never user-visible and never user-writable.
const Internal = ".drive"

var ErrInvalidPath = errors.New("invalid path")

// relPath validates a user-supplied path and returns it in canonical form.
//
// os.Root already confines every operation to the root at the syscall level,
// including symlinks that point outside it. This rejects malformed input
// earlier, with an error that says what was wrong, and keeps the reserved
// .drive directory out of reach of the ordinary file API.
//
// Input must already be URL-decoded, which is what net/http hands us: an
// encoded separator such as %2e%2e%2f arrives here as ../ and is rejected as
// the traversal it is.
func relPath(p string) (string, error) {
	if strings.ContainsRune(p, 0) {
		return "", fmt.Errorf("%w: path contains a NUL byte", ErrInvalidPath)
	}
	// fs.ValidPath is exactly the rule wanted: unrooted, slash-separated, no
	// empty, "." or ".." elements, no leading or trailing slash.
	if !fs.ValidPath(p) {
		return "", fmt.Errorf("%w: %q must be a relative slash-separated path with no empty, . or .. elements", ErrInvalidPath, p)
	}
	if first, _, _ := strings.Cut(p, "/"); first == Internal {
		return "", fmt.Errorf("%w: %q is reserved", ErrInvalidPath, Internal)
	}
	return p, nil
}
