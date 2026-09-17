package server

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fosslife/drive/internal/auth"
	"github.com/fosslife/drive/internal/thumb"
)

// picture is a w×h image, red on the left half and blue on the right, so a
// rotation is visible in a single pixel.
func picture(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			c := color.RGBA{R: 220, A: 255}
			if x >= w/2 {
				c = color.RGBA{B: 220, A: 255}
			}
			img.Set(x, y, c)
		}
	}
	return img
}

func encodeJPEG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, nil); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// withExif splices an APP1 segment carrying one orientation tag into a JPEG,
// which is what a phone writes when it takes a photograph sideways.
func withExif(t *testing.T, jpg []byte, turn uint16) []byte {
	t.Helper()
	tiff := new(bytes.Buffer)
	tiff.WriteString("Exif\x00\x00")
	tiff.WriteString("II")                              // little-endian
	binary.Write(tiff, binary.LittleEndian, uint16(42)) // the TIFF magic number
	binary.Write(tiff, binary.LittleEndian, uint32(8))  // IFD0 starts right after
	binary.Write(tiff, binary.LittleEndian, uint16(1))  // one entry
	binary.Write(tiff, binary.LittleEndian, uint16(0x0112))
	binary.Write(tiff, binary.LittleEndian, uint16(3)) // SHORT
	binary.Write(tiff, binary.LittleEndian, uint32(1))
	binary.Write(tiff, binary.LittleEndian, turn)
	binary.Write(tiff, binary.LittleEndian, uint16(0)) // padding of the value field
	binary.Write(tiff, binary.LittleEndian, uint32(0)) // no next IFD

	segment := new(bytes.Buffer)
	segment.Write([]byte{0xFF, 0xE1})
	binary.Write(segment, binary.BigEndian, uint16(tiff.Len()+2))
	segment.Write(tiff.Bytes())

	out := append([]byte{}, jpg[:2]...) // SOI
	out = append(out, segment.Bytes()...)
	return append(out, jpg[2:]...)
}

// A 400×200 lossless WebP, solid blue. Checked in as bytes because there is no
// WebP encoder in Go: golang.org/x/image decodes them and nothing writes them.
const blueWebP = "UklGRiYAAABXRUJQVlA4TBoAAAAvj8ExAAdQiioUuf8BAUnS//9hRP8z/vN/iA=="

func (h *harness) putBytes(u *auth.User, rel string, content []byte) {
	h.t.Helper()
	h.put(u, rel, string(content))
}

// thumbOf fetches a thumbnail and decodes it, failing on anything but 200.
func (h *harness) thumbOf(t *testing.T, secret, path string) image.Image {
	t.Helper()
	w := h.as(secret, "GET", "/api/thumb/"+path, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("thumbnail for %q: %d %s", path, w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("thumbnail Content-Type = %q", ct)
	}
	img, err := jpeg.Decode(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatalf("decoding the thumbnail for %q: %v", path, err)
	}
	return img
}

// 12.1: every format the spec names produces a reduced image that keeps its
// proportions, with no media toolchain installed to do it.
func TestThumbnailsForEverySupportedFormat(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)

	var pngBuf, gifBuf bytes.Buffer
	if err := png.Encode(&pngBuf, picture(800, 400)); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gifBuf, picture(800, 400), nil); err != nil {
		t.Fatal(err)
	}
	webp, err := base64.StdEncoding.DecodeString(blueWebP)
	if err != nil {
		t.Fatal(err)
	}

	h.putBytes(ada, "wide.jpg", encodeJPEG(t, picture(800, 400)))
	h.putBytes(ada, "wide.png", pngBuf.Bytes())
	h.putBytes(ada, "wide.gif", gifBuf.Bytes())
	h.putBytes(ada, "wide.webp", webp)
	h.putBytes(ada, "tall.jpg", encodeJPEG(t, picture(200, 600)))
	h.putBytes(ada, "small.jpg", encodeJPEG(t, picture(64, 32)))
	h.scan(ada)

	// 2:1 sources come back 2:1, bounded by the long edge.
	for _, name := range []string{"wide.jpg", "wide.png", "wide.gif", "wide.webp"} {
		b := h.thumbOf(t, secret, name).Bounds()
		if b.Dx() != thumb.Size || b.Dy() != thumb.Size/2 {
			t.Errorf("%s thumbnail is %dx%d, want %dx%d", name, b.Dx(), b.Dy(), thumb.Size, thumb.Size/2)
		}
	}
	// A portrait source stays portrait.
	if b := h.thumbOf(t, secret, "tall.jpg").Bounds(); b.Dy() != thumb.Size || b.Dx() != thumb.Size/3 {
		t.Errorf("tall.jpg thumbnail is %dx%d, want %dx%d", b.Dx(), b.Dy(), thumb.Size/3, thumb.Size)
	}
	// Already smaller than a thumbnail: left alone rather than blown up.
	if b := h.thumbOf(t, secret, "small.jpg").Bounds(); b.Dx() != 64 || b.Dy() != 32 {
		t.Errorf("small.jpg thumbnail is %dx%d, want it untouched at 64x32", b.Dx(), b.Dy())
	}
}

// 12.2: a photograph taken sideways is shown the right way up.
func TestThumbnailAppliesExifOrientation(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)

	jpg := encodeJPEG(t, picture(200, 100)) // red on the left, blue on the right
	h.putBytes(ada, "upright.jpg", jpg)
	// Orientation 6 is "rotate 90° clockwise to display": the left of the stored
	// image belongs at the top.
	h.putBytes(ada, "sideways.jpg", withExif(t, jpg, 6))
	h.scan(ada)

	upright := h.thumbOf(t, secret, "upright.jpg")
	if b := upright.Bounds(); b.Dx() != 200 || b.Dy() != 100 {
		t.Fatalf("the unrotated thumbnail is %v, want 200x100", b)
	}

	rotated := h.thumbOf(t, secret, "sideways.jpg")
	b := rotated.Bounds()
	if b.Dx() != 100 || b.Dy() != 200 {
		t.Fatalf("the rotated thumbnail is %dx%d, want the axes swapped to 100x200", b.Dx(), b.Dy())
	}
	// Red was the left half; after the rotation it is the top half.
	if !reddish(rotated.At(50, 20)) {
		t.Errorf("the top of the rotated thumbnail is %v, want the red that was on the left", rotated.At(50, 20))
	}
	if reddish(rotated.At(50, 180)) {
		t.Errorf("the bottom of the rotated thumbnail is %v, want the blue that was on the right", rotated.At(50, 180))
	}
}

func reddish(c color.Color) bool {
	r, _, b, _ := c.RGBA()
	return r > b*2
}

// 12.3: a file with no picture in it is reported as having none, and stays a
// perfectly good file otherwise.
func TestUnsupportedFilesReportNoThumbnailAndStayUsable(t *testing.T) {
	h, ada, secret := withFiles(t)
	h.put(ada, "clip.mp4", "not really a video, and certainly not an image")
	h.scan(ada)

	for _, name := range []string{"clip.mp4", "notes.txt"} {
		w := h.as(secret, "GET", "/api/thumb/"+name, nil)
		if w.Code != http.StatusUnsupportedMediaType {
			t.Errorf("thumbnail for %s: %d %s, want 415", name, w.Code, w.Body.String())
		}
	}
	// Listed, downloadable, shareable: the missing picture changes none of that.
	if !slicesContains(h.list(t, secret, "").Entries, "clip.mp4") {
		t.Error("a file with no thumbnail fell out of its listing")
	}
	if w := h.as(secret, "GET", "/api/download/clip.mp4", nil); w.Code != http.StatusOK {
		t.Errorf("downloading a file with no thumbnail: %d", w.Code)
	}
	if w := h.as(secret, "POST", "/api/shares", map[string]any{"path": "clip.mp4"}); w.Code != http.StatusCreated {
		t.Errorf("sharing a file with no thumbnail: %d %s", w.Code, w.Body.String())
	}
	// A folder is not an image either, and asking is not an error worth logging.
	if w := h.as(secret, "GET", "/api/thumb/docs", nil); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("thumbnail for a folder: %d, want 415", w.Code)
	}
}

// 12.4: a big folder of unthumbnailed images lists at once, because listing
// does not touch the thumbnail machinery at all.
func TestListingDoesNotWaitForThumbnails(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)

	jpg := encodeJPEG(t, picture(1200, 900))
	root, err := h.users.Root(ada)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 1000 {
		if _, err := root.Write(filepath.Join("photos", fmt.Sprintf("%04d.jpg", i)),
			bytes.NewReader(jpg), int64(len(jpg))); err != nil {
			t.Fatal(err)
		}
	}
	root.Close()
	h.scan(ada)

	start := time.Now()
	got := h.list(t, secret, "photos")
	took := time.Since(start)

	if len(got.Entries) == 0 {
		t.Fatal("the folder listed empty")
	}
	// Decoding even one of these takes milliseconds; a thousand would take
	// seconds. This bound fails long before anyone would call it slow.
	if took > 2*time.Second {
		t.Errorf("listing 1,000 unthumbnailed images took %s", took)
	}
	var made int
	h.db.QueryRow(`SELECT count(*) FROM thumbs`).Scan(&made)
	if made != 0 {
		t.Errorf("listing generated %d thumbnails; it must generate none", made)
	}
}

// 12.5: the second request does not redraw, a changed file does, and a corrupt
// one is not decoded again and again.
func TestThumbnailsAreCachedInvalidatedAndFailuresRemembered(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)
	h.putBytes(ada, "photo.jpg", encodeJPEG(t, picture(800, 400)))
	h.putBytes(ada, "broken.jpg", []byte("\xff\xd8\xff not a picture at all"))
	h.scan(ada)

	first := h.as(secret, "GET", "/api/thumb/photo.jpg", nil)
	if first.Code != http.StatusOK {
		t.Fatalf("the first thumbnail: %d %s", first.Code, first.Body.String())
	}
	id := entryNamed(t, h.list(t, secret, "").Entries, "photo.jpg").ID
	cached := filepath.Join(h.dataDir, "users", "ada", ".drive", "thumbs", fmt.Sprint(id)+".jpg")

	// Replace the cached picture with something unmistakable: a second request
	// that returns it is a request that did not regenerate.
	marker := encodeJPEG(t, picture(8, 8))
	if err := os.WriteFile(cached, marker, 0o600); err != nil {
		t.Fatal(err)
	}
	if second := h.as(secret, "GET", "/api/thumb/photo.jpg", nil); !bytes.Equal(second.Body.Bytes(), marker) {
		t.Error("the second request regenerated the thumbnail instead of serving the cached one")
	}

	// A new version of the file is a new thumbnail.
	h.putBytes(ada, "photo.jpg", encodeJPEG(t, picture(300, 600)))
	h.scan(ada)
	if b := h.thumbOf(t, secret, "photo.jpg").Bounds(); b.Dy() <= b.Dx() {
		t.Errorf("after the source changed the thumbnail is %v, want the new portrait shape", b)
	}

	// A corrupt image is recorded as such and not reprocessed.
	if w := h.as(secret, "GET", "/api/thumb/broken.jpg", nil); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("a corrupt image: %d %s, want 415", w.Code, w.Body.String())
	}
	var state string
	brokenID := entryNamed(t, h.list(t, secret, "").Entries, "broken.jpg").ID
	if err := h.db.QueryRow(`SELECT state FROM thumbs WHERE file_id = ?`, brokenID).Scan(&state); err != nil {
		t.Fatalf("no record of the failure: %v", err)
	}
	if state != "failed" {
		t.Errorf("the failure is recorded as %q, want failed", state)
	}
	// Proof that the retry does not touch the file: remove it and ask again.
	if err := os.Remove(filepath.Join(h.dataDir, "users", "ada", "broken.jpg")); err != nil {
		t.Fatal(err)
	}
	if w := h.as(secret, "GET", "/api/thumb/broken.jpg", nil); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("the repeat request for a failed thumbnail: %d, want the remembered 415", w.Code)
	}
}

// 12.6: the cache is derived data. Deleting all of it costs nothing but the
// work to make it again.
func TestThumbnailCacheIsDerivedData(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)
	original := encodeJPEG(t, picture(800, 400))
	h.putBytes(ada, "photo.jpg", original)
	h.scan(ada)

	if b := h.thumbOf(t, secret, "photo.jpg").Bounds(); b.Dx() != thumb.Size {
		t.Fatalf("the first thumbnail is %v", b)
	}

	cacheDir := filepath.Join(h.dataDir, "users", "ada", ".drive", "thumbs")
	if err := os.RemoveAll(cacheDir); err != nil {
		t.Fatal(err)
	}

	// No user file is touched by that, and the picture comes back on demand.
	if got, err := os.ReadFile(filepath.Join(h.dataDir, "users", "ada", "photo.jpg")); err != nil ||
		!bytes.Equal(got, original) {
		t.Errorf("the source file after deleting the cache: %d bytes, %v", len(got), err)
	}
	if b := h.thumbOf(t, secret, "photo.jpg").Bounds(); b.Dx() != thumb.Size {
		t.Errorf("the regenerated thumbnail is %v", b)
	}
	if _, err := os.Stat(filepath.Join(cacheDir)); err != nil {
		t.Errorf("the cache directory was not recreated: %v", err)
	}
}

// 12.6: a permanent delete takes the picture with it. "Permanently deleted"
// that leaves a recognisable thumbnail behind has not deleted anything.
func TestPurgingAFileDiscardsItsThumbnail(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)
	h.putBytes(ada, "album/photo.jpg", encodeJPEG(t, picture(800, 400)))
	h.scan(ada)

	id := entryNamed(t, h.list(t, secret, "album").Entries, "photo.jpg").ID
	h.thumbOf(t, secret, "album/photo.jpg")
	cached := filepath.Join(h.dataDir, "users", "ada", ".drive", "thumbs", fmt.Sprint(id)+".jpg")
	if _, err := os.Stat(cached); err != nil {
		t.Fatalf("no cached thumbnail to begin with: %v", err)
	}

	// Delete the folder, so the thumbnail being removed is a descendant's.
	folder := h.delete(t, secret, "album")
	if _, err := os.Stat(cached); err != nil {
		t.Errorf("trashing removed the thumbnail: %v; trash destroys nothing", err)
	}
	if w := h.as(secret, "DELETE", "/api/trash/"+itoa(folder.ID), nil); w.Code != http.StatusNoContent {
		t.Fatalf("purging: %d %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(cached); !os.IsNotExist(err) {
		t.Errorf("the thumbnail of a permanently deleted file is still there: %v", err)
	}
	var rows int
	h.db.QueryRow(`SELECT count(*) FROM thumbs WHERE file_id = ?`, id).Scan(&rows)
	if rows != 0 {
		t.Errorf("%d thumbnail rows survive the purge", rows)
	}
}

// 12.7: a thumbnail is readable exactly when the file behind it is.
func TestThumbnailAccessFollowsTheFile(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)
	h.putBytes(ada, "album/photo.jpg", encodeJPEG(t, picture(800, 400)))
	h.scan(ada)

	bob := h.account("bob", false)
	bobsToken := h.token(bob)
	if w := h.as(bobsToken, "GET", "/api/thumb/album/photo.jpg", nil); w.Code != http.StatusNotFound {
		t.Errorf("another user's thumbnail: %d, want 404", w.Code)
	}

	link := h.share(t, secret, map[string]any{"path": "album"})
	v := h.visitor(t)
	w := v.do("GET", "/api/shares/"+link.Token+"/thumb/photo.jpg", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("a thumbnail through a share link: %d %s", w.Code, w.Body.String())
	}
	if _, err := jpeg.Decode(bytes.NewReader(w.Body.Bytes())); err != nil {
		t.Errorf("the shared thumbnail does not decode: %v", err)
	}
	// Outside the shared folder, through the share: nothing.
	if w := v.do("GET", "/api/shares/"+link.Token+"/thumb/../secret.jpg", nil); w.Code == http.StatusOK {
		t.Error("a share link served a thumbnail from outside its folder")
	}

	if w := h.as(secret, "DELETE", "/api/shares/"+itoa(link.ID), nil); w.Code != http.StatusNoContent {
		t.Fatalf("revoking: %d %s", w.Code, w.Body.String())
	}
	if w := v.do("GET", "/api/shares/"+link.Token+"/thumb/photo.jpg", nil); w.Code != http.StatusNotFound {
		t.Errorf("a thumbnail after revocation: %d, want 404", w.Code)
	}
}

// 12.8: full-size viewing in place is the download endpoint's inline
// allowlist — an image renders, anything that could carry script does not.
func TestFullSizeImagesAreServedInlineAndNothingElseIs(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	secret := h.token(ada)
	full := encodeJPEG(t, picture(1200, 900))
	h.putBytes(ada, "photo.jpg", full)
	h.put(ada, "drawing.svg", `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)
	h.scan(ada)

	w := h.as(secret, "GET", "/api/download/photo.jpg", nil)
	if w.Header().Get("Content-Disposition") != "inline; filename=photo.jpg" {
		t.Errorf("a JPEG is served as %q, want inline", w.Header().Get("Content-Disposition"))
	}
	if w.Header().Get("Content-Type") != "image/jpeg" || !bytes.Equal(w.Body.Bytes(), full) {
		t.Errorf("the full-size image came back as %q, %d bytes", w.Header().Get("Content-Type"), w.Body.Len())
	}

	svg := h.as(secret, "GET", "/api/download/drawing.svg", nil)
	if svg.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("an SVG is served as %q", svg.Header().Get("Content-Type"))
	}
	if !bytes.Contains([]byte(svg.Header().Get("Content-Disposition")), []byte("attachment")) {
		t.Errorf("an SVG is served as %q, want an attachment", svg.Header().Get("Content-Disposition"))
	}
}
