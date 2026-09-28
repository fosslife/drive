package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// setQuota gives an account an allowance, the way an administrator would.
func setQuota(t *testing.T, h *harness, username string, bytes int64) {
	t.Helper()
	if err := h.users.SetQuota(username, bytes); err != nil {
		t.Fatal(err)
	}
}

// tempFiles counts what is waiting in a root's upload area.
func tempFiles(t *testing.T, h *harness, username string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.dataDir, "users", username, ".drive", "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// 4.1: refused before a byte is stored, and the message says what the limit is.
func TestUploadBeyondTheQuotaIsRefusedBeforeAnyByteLands(t *testing.T) {
	h := newHarness(t)
	ada, bob := h.account("ada", false), h.account("bob", false)
	setQuota(t, h, "ada", 100)

	w := newUploader(t, h, ada).tryCreate("", "big.bin", 200, false)
	if w.Code != http.StatusInsufficientStorage {
		t.Fatalf("an upload past the quota: %d %s, want 507", w.Code, w.Body.String())
	}
	for _, want := range []string{"quota", "100", "200"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("the refusal %s does not mention %q", w.Body.String(), want)
		}
	}
	if n := tempFiles(t, h, "ada"); n != 0 {
		t.Errorf("a refused upload left %d files in .drive/tmp", n)
	}
	var reserved int
	if err := h.db.QueryRow(`SELECT COUNT(*) FROM uploads WHERE user_id = ?`, ada.ID).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved != 0 {
		t.Errorf("a refused upload left %d rows in uploads", reserved)
	}

	// What fits is still accepted, and an account with no quota at all is
	// exactly as it was before any of this existed.
	newUploader(t, h, ada).create("", "small.bin", 100, false)
	newUploader(t, h, bob).create("", "big.bin", 1<<20, false)
}

// 4.2: two limits, two reasons, two messages. Sending someone to delete their
// files when the volume is full, or to the operator when it is their own quota,
// is the failure this prevents.
func TestQuotaAndVolumeReserveAreToldApart(t *testing.T) {
	quota := newHarness(t)
	ada := quota.account("ada", false)
	setQuota(t, quota, "ada", 10)
	refusedByQuota := newUploader(t, quota, ada).tryCreate("", "big.bin", 1000, false).Body.String()

	// A reserve larger than any disk: every write eats into it.
	volume := newHarnessReserving(t, 1<<62)
	bob := volume.account("bob", false)
	refusedByVolume := newUploader(t, volume, bob).tryCreate("", "big.bin", 1000, false).Body.String()

	if !strings.Contains(refusedByQuota, "quota") {
		t.Errorf("the quota refusal does not name the quota: %s", refusedByQuota)
	}
	if strings.Contains(refusedByQuota, "free space") {
		t.Errorf("the quota refusal blames the volume: %s", refusedByQuota)
	}
	if !strings.Contains(refusedByVolume, "free space") {
		t.Errorf("the volume refusal does not name free space: %s", refusedByVolume)
	}
	if strings.Contains(refusedByVolume, "quota") {
		t.Errorf("the volume refusal blames the account's quota: %s", refusedByVolume)
	}
}

// 4.3: a full account is not a locked account. Everything except writing keeps
// working, which is what makes the way out of it available.
func TestAFullAccountCanStillReadAndEmptyItself(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)

	h.put(ada, "notes.txt", "some notes worth keeping")
	h.put(ada, "photo.jpg", string(encodeJPEG(t, picture(40, 20))))
	h.scan(ada)
	setQuota(t, h, "ada", 1) // below what is already there

	secret := h.token(ada)
	if w := newUploader(t, h, ada).tryCreate("", "more.bin", 10, false); w.Code != http.StatusInsufficientStorage {
		t.Fatalf("an upload by a full account: %d %s, want 507", w.Code, w.Body.String())
	}

	for _, read := range []struct{ name, path string }{
		{"list", "/api/list?path="},
		{"search", "/api/search?q=notes"},
		{"download", "/api/download/notes.txt"},
		{"thumbnail", "/api/thumb/photo.jpg"},
	} {
		if w := h.as(secret, "GET", read.path, nil); w.Code != http.StatusOK {
			t.Errorf("%s while over quota: %d %s, want 200", read.name, w.Code, w.Body.String())
		}
	}

	// Trash, restore, and permanent delete: the whole path back under the line.
	notes := h.delete(t, secret, "notes.txt").ID
	if w := h.as(secret, "POST", "/api/trash/"+strconv.FormatInt(notes, 10)+"/restore", nil); w.Code != http.StatusOK {
		t.Fatalf("restoring while over quota: %d %s", w.Code, w.Body.String())
	}
	notes = h.delete(t, secret, "notes.txt").ID

	// Still counted while in the trash: those bytes are still on the volume.
	usage, err := h.users.Usage(ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if usage.Files != 2 {
		t.Errorf("usage after trashing = %+v, want both files still counted", usage)
	}

	if w := h.as(secret, "DELETE", "/api/trash/"+strconv.FormatInt(notes, 10), nil); w.Code != http.StatusNoContent {
		t.Fatalf("permanently deleting while over quota: %d %s", w.Code, w.Body.String())
	}
	after, err := h.users.Usage(ada.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Bytes >= usage.Bytes {
		t.Errorf("usage after a permanent delete = %d, was %d; freeing bytes must lower it", after.Bytes, usage.Bytes)
	}

	// And the room it freed is usable: raise the quota to just above what is
	// left and the next upload goes through.
	setQuota(t, h, "ada", after.Bytes+10)
	newUploader(t, h, ada).create("", "more.bin", 10, false)
}

// 4.4: the check and the reservation are one transaction, so uploads started
// together cannot each find the same room.
func TestConcurrentUploadsCannotRacePastTheQuota(t *testing.T) {
	h := newHarness(t)
	ada := h.account("ada", false)
	setQuota(t, h, "ada", 100)

	const racers = 8
	c := newUploader(t, h, ada)
	codes := make([]int, racers)
	var start sync.WaitGroup
	var done sync.WaitGroup
	start.Add(1)
	for i := range racers {
		done.Add(1)
		go func() {
			defer done.Done()
			start.Wait()
			codes[i] = c.tryCreate("", "racer-"+strconv.Itoa(i)+".bin", 60, false).Code
		}()
	}
	start.Done()
	done.Wait()

	accepted := 0
	for _, code := range codes {
		if code == http.StatusCreated {
			accepted++
		}
	}
	if accepted != 1 {
		t.Errorf("%d of %d concurrent 60-byte uploads were accepted against a 100-byte quota, want exactly 1", accepted, racers)
	}
	var reserved int64
	if err := h.db.QueryRow(`SELECT COALESCE(SUM(size), 0) FROM uploads WHERE user_id = ?`, ada.ID).Scan(&reserved); err != nil {
		t.Fatal(err)
	}
	if reserved > 100 {
		t.Errorf("uploads reserve %d bytes against a quota of 100", reserved)
	}
}
