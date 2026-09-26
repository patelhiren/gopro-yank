package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func selectionItem(id, captured, kind string) MediaItem {
	return MediaItem{ID: id, CapturedAt: captured, MediaType: kind}
}

func selectedIDs(t *testing.T, selection Selection, items []MediaItem) string {
	t.Helper()
	selected, err := selection.Filter(items)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(selected))
	for _, item := range selected {
		ids = append(ids, item.ID)
	}
	return strings.Join(ids, ",")
}

func TestSelectionDateBoundsIncludeTheWrittenPrecision(t *testing.T) {
	items := []MediaItem{
		selectionItem("before", "2026-09-11T23:59:59Z", "Video"),
		selectionItem("start", "2026-09-12T00:00:00Z", "Video"),
		selectionItem("last-day", "2026-09-14T23:59:59Z", "Video"),
		selectionItem("after", "2026-09-15T00:00:00Z", "Video"),
	}
	selection, err := ParseSelection("2026-09-12", "2026-09-14", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := selectedIDs(t, selection, items); got != "start,last-day" {
		t.Fatalf("date-only range selected %q", got)
	}

	items = []MediaItem{
		selectionItem("early", "2026-09-12T07:59:59Z", "Video"),
		selectionItem("morning", "2026-09-12T08:00:00Z", "Video"),
		selectionItem("same-minute", "2026-09-12T22:00:59Z", "Video"),
		selectionItem("late", "2026-09-12T22:01:00Z", "Video"),
	}
	selection, err = ParseSelection("2026-09-12T08:00", "2026-09-12 22:00", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := selectedIDs(t, selection, items); got != "morning,same-minute" {
		t.Fatalf("minute range selected %q", got)
	}
}

func TestSelectionCameraClockIgnoresZoneLabels(t *testing.T) {
	items := []MediaItem{
		selectionItem("zoned", "2026-09-12T08:30:00-07:00", "Video"),
		selectionItem("utc", "2026-09-12T08:30:00Z", "Video"),
		selectionItem("naive", "2026-09-12T08:30:00", "Video"),
		selectionItem("fraction", "2026-09-12T08:30:00.250Z", "Video"),
		selectionItem("created-only", "", "Video"),
		selectionItem("undated", "", "Video"),
	}
	items[4].CreatedAt = "2026-09-12T08:45:00Z"
	selection, err := ParseSelection("2026-09-12T08:00", "2026-09-12T09:00", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := selectedIDs(t, selection, items); got != "zoned,utc,naive,fraction,created-only" {
		t.Fatalf("wall-clock range selected %q", got)
	}
}

func TestSelectionMatchesGoProWebsiteDates(t *testing.T) {
	// GoPro labels the camera clock as UTC; gopro.com shows it in the browser's zone.
	items := []MediaItem{
		selectionItem("GX011187", "2026-08-28T15:15:28Z", "Video"),
		selectionItem("GX011194", "2026-08-29T00:38:44Z", "Video"),
		selectionItem("GX011197", "2026-08-29T04:00:00Z", "Video"),
		selectionItem("naive", "2026-08-29T03:59:59", "Video"),
	}
	website, err := ParseSelection("2026-08-28", "2026-08-28", "", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if got := selectedIDs(t, website, items); got != "GX011187,GX011194,naive" {
		t.Fatalf("gopro.com dates in New York selected %q", got)
	}
	camera, _ := ParseSelection("2026-08-28", "2026-08-28", "", "")
	if got := selectedIDs(t, camera, items); got != "GX011187" {
		t.Fatalf("camera clock selected %q", got)
	}

	// Standard time applies in winter: 2026-01-10T04:30Z is 23:30 on Jan 9 in New York.
	winter := []MediaItem{selectionItem("winter", "2026-01-10T04:30:00Z", "Video")}
	january9, _ := ParseSelection("2026-01-09T23:00", "2026-01-09T23:59", "", "America/New_York")
	if got := selectedIDs(t, january9, winter); got != "winter" {
		t.Fatalf("winter time selected %q", got)
	}
}

func TestSelectionOpenEndedRanges(t *testing.T) {
	items := []MediaItem{
		selectionItem("old", "2025-01-01T00:00:00Z", "Video"),
		selectionItem("new", "2026-09-20T00:00:00Z", "Video"),
	}
	from, _ := ParseSelection("2026-01-01", "", "", "")
	to, _ := ParseSelection("", "2025-12-31", "", "")
	if got := selectedIDs(t, from, items); got != "new" {
		t.Fatalf("--from only selected %q", got)
	}
	if got := selectedIDs(t, to, items); got != "old" {
		t.Fatalf("--to only selected %q", got)
	}
}

func TestSelectionTypesAreCaseInsensitive(t *testing.T) {
	items := []MediaItem{
		selectionItem("video", "2026-09-12T08:00:00Z", "Video"),
		selectionItem("photo", "2026-09-12T08:00:00Z", "Photo"),
		selectionItem("lapse", "2026-09-12T08:00:00Z", "TimeLapseVideo"),
		selectionItem("undated-video", "", "Video"),
	}
	selection, err := ParseSelection("", "", " video, timelapsevideo ,VIDEO", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(selection.Types) != 2 {
		t.Fatalf("types were not trimmed and deduplicated: %q", selection.Types)
	}
	if got := selectedIDs(t, selection, items); got != "video,lapse,undated-video" {
		t.Fatalf("type filter selected %q", got)
	}
	selection, _ = ParseSelection("2026-09-12", "2026-09-12", "Video", "")
	if got := selectedIDs(t, selection, items); got != "video" {
		t.Fatalf("combined filter selected %q", got)
	}
}

func TestParseSelectionRejectsBadInput(t *testing.T) {
	for _, values := range [][2]string{
		{"yesterday", ""},
		{"", "2026-13-01"},
		{"2026-09-12T25:00", ""},
		{"2026-09-14", "2026-09-12"},
		{"2026-09-12T10:00", "2026-09-12T09:59"},
	} {
		if _, err := ParseSelection(values[0], values[1], "", ""); err == nil {
			t.Errorf("accepted --from %q --to %q", values[0], values[1])
		}
	}
	if _, err := ParseSelection("2026-09-12", "2026-09-12", "", ""); err != nil {
		t.Errorf("rejected a single-day range: %v", err)
	}
	if _, err := ParseSelection("2026-09-12", "", "", "Mars/Olympus_Mons"); err == nil {
		t.Error("accepted an unknown time zone")
	}
}

func TestSelectionFromFlags(t *testing.T) {
	if selection, err := selectionFromFlags("", "", "", "UTC", false, false); err != nil || selection != nil {
		t.Fatalf("no selection flags should keep the saved selection: %+v, %v", selection, err)
	}
	if selection, err := selectionFromFlags("", "", "", "", false, true); err != nil || selection == nil || !selection.IsEmpty() {
		t.Fatalf("--all should select the whole library: %+v, %v", selection, err)
	}
	if _, err := selectionFromFlags("2026-09-12", "", "", "", false, true); err == nil {
		t.Fatal("--all was combined with --from")
	}
	if _, err := selectionFromFlags("2026-09-12", "", "", "UTC", true, false); err == nil {
		t.Fatal("--camera-clock was combined with --tz")
	}
	if selection, err := selectionFromFlags("2026-09-12", "", "", "", false, false); err != nil || selection.Zone == "" || selection.Zone != localZoneName() {
		t.Fatalf("dates should default to this computer's zone: %+v, %v", selection, err)
	}
	if selection, err := selectionFromFlags("2026-09-12", "", "", "America/Los_Angeles", false, false); err != nil || selection.Zone != "America/Los_Angeles" {
		t.Fatalf("--tz was not kept: %+v, %v", selection, err)
	}
	if selection, err := selectionFromFlags("2026-09-12", "", "", "", true, false); err != nil || selection.Zone != "" {
		t.Fatalf("--camera-clock kept a zone: %+v, %v", selection, err)
	}
	if selection, err := selectionFromFlags("", "", "Video", "", false, false); err != nil || selection.Zone != "" {
		t.Fatalf("a type-only selection saved a zone: %+v, %v", selection, err)
	}
}

func TestLocalZoneNameIsLoadable(t *testing.T) {
	name := localZoneName()
	if _, err := time.LoadLocation(name); err != nil {
		t.Fatalf("local zone %q cannot be loaded: %v", name, err)
	}
}

// fakeGoPro is a local GoPro API serving a fixed library and recording downloads.
type fakeGoPro struct {
	server    *httptest.Server
	mu        sync.Mutex
	details   []string
	downloads []string
}

func newFakeGoPro(t *testing.T, media []map[string]any) *fakeGoPro {
	t.Helper()
	fake := &fakeGoPro{}
	byID := map[string]map[string]any{}
	for _, item := range media {
		byID[item["id"].(string)] = item
	}
	fake.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.URL.Path == "/media/user":
			_ = json.NewEncoder(writer).Encode(map[string]any{"id": "user"})
		case request.URL.Path == "/media/search":
			_ = json.NewEncoder(writer).Encode(map[string]any{"_embedded": map[string]any{"media": media}, "_pages": map[string]any{"total_pages": 1}})
		case request.URL.Path == "/media/x/zip/source":
			id := request.URL.Query().Get("ids")
			fake.mu.Lock()
			fake.downloads = append(fake.downloads, id)
			fake.mu.Unlock()
			members := map[string][]byte{byID[id]["filename"].(string): []byte("payload-" + id)}
			if chapters, ok := byID[id]["chapters"].([]string); ok {
				for _, chapter := range chapters {
					members[chapter] = []byte("chapter-" + chapter + "-" + id)
				}
			}
			_, _ = writer.Write(zipBytes(t, members))
		case strings.HasPrefix(request.URL.Path, "/media/"):
			id := strings.TrimPrefix(request.URL.Path, "/media/")
			fake.mu.Lock()
			fake.details = append(fake.details, id)
			fake.mu.Unlock()
			if item, ok := byID[id]; ok {
				_ = json.NewEncoder(writer).Encode(item)
				return
			}
			http.NotFound(writer, request)
		default:
			http.NotFound(writer, request)
		}
	}))
	previousBase := apiBaseURL
	apiBaseURL = fake.server.URL
	t.Cleanup(func() {
		apiBaseURL = previousBase
		fake.server.Close()
	})
	t.Setenv("AUTH_TOKEN", "")
	t.Setenv("USER_ID", "")
	return fake
}

func (f *fakeGoPro) takeDownloads() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := append([]string(nil), f.downloads...)
	f.downloads = nil
	return strings.Join(sortedStrings(ids), ",")
}

func sortedStrings(values []string) []string {
	slices.Sort(values)
	return values
}

func tripLibrary() []map[string]any {
	return []map[string]any{
		{"id": "before-trip", "filename": "GX010001.MP4", "file_size": 15, "captured_at": "2026-09-10T09:00:00Z", "type": "Video"},
		{"id": "trip-video", "filename": "GX010002.MP4", "file_size": 14, "captured_at": "2026-09-12T09:00:00Z", "type": "Video"},
		{"id": "trip-photo", "filename": "GOPR0003.JPG", "file_size": 14, "captured_at": "2026-09-13T09:00:00Z", "type": "Photo"},
		{"id": "trip-lapse", "filename": "GX010004.MP4", "file_size": 14, "captured_at": "2026-09-14T21:00:00Z", "type": "TimeLapseVideo"},
		{"id": "after-trip", "filename": "GX010005.MP4", "file_size": 14, "captured_at": "2026-09-20T09:00:00Z", "type": "Video"},
	}
}

func writeTestEnv(t *testing.T) string {
	t.Helper()
	envPath := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envPath, []byte("AUTH_TOKEN=token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return envPath
}

func archiveArgs(root, envPath string, extra ...string) []string {
	return append([]string{"-out", root, "-env-file", envPath, "-state-dir", filepath.Join(root, "no-legacy"), "-parallel", "2", "-tz", "UTC"}, extra...)
}

func TestArchiveDownloadsOnlyTheSelection(t *testing.T) {
	fake := newFakeGoPro(t, tripLibrary())
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog-trip")

	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-from", "2026-09-12", "-to", "2026-09-14", "-type", "Video,TimeLapseVideo")); err != nil {
		t.Fatalf("selected archive did not complete: %v", err)
	}
	if got := fake.takeDownloads(); got != "trip-lapse,trip-video" {
		t.Fatalf("downloaded %q", got)
	}
	fake.mu.Lock()
	details := strings.Join(sortedStrings(append([]string(nil), fake.details...)), ",")
	fake.mu.Unlock()
	if details != "trip-lapse,trip-video" {
		t.Fatalf("fetched details for unselected media: %q", details)
	}
	archive, err := NewArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	if archive.Data.Selection == nil || archive.Data.Selection.From != "2026-09-12" || archive.Data.Selection.To != "2026-09-14" || archive.Data.Selection.Zone != "UTC" {
		t.Fatalf("selection was not saved: %+v", archive.Data.Selection)
	}
	if len(archive.Data.Items) != 2 {
		t.Fatalf("manifest tracks unselected media: %d items", len(archive.Data.Items))
	}
	if summary := archive.Summary(); summary.Archived != 2 || summary.Blockers != 0 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if !archive.Verify("").OK() {
		t.Fatal("selected archive failed verification")
	}

	// A rerun without flags keeps the saved selection instead of pulling the whole library.
	if err := archiveCommand(context.Background(), archiveArgs(root, envPath)); err != nil {
		t.Fatalf("rerun failed: %v", err)
	}
	if got := fake.takeDownloads(); got != "" {
		t.Fatalf("rerun downloaded %q", got)
	}

	// verify --source compares against the saved selection too.
	if err := verifyCommand(context.Background(), []string{"-out", root, "-env-file", envPath, "-source"}); err != nil {
		t.Fatalf("verify --source reported unselected media as missing: %v", err)
	}
}

func TestArchiveSelectionCanChangeWithoutLosingFiles(t *testing.T) {
	fake := newFakeGoPro(t, tripLibrary())
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")

	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-from", "2026-09-12", "-to", "2026-09-12")); err != nil {
		t.Fatal(err)
	}
	if got := fake.takeDownloads(); got != "trip-video" {
		t.Fatalf("first selection downloaded %q", got)
	}
	archive, _ := NewArchive(root)
	firstFile := filepath.Join(root, filepath.FromSlash(archive.Item("trip-video").Files[0].Path))

	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-from", "2026-09-20")); err != nil {
		t.Fatal(err)
	}
	if got := fake.takeDownloads(); got != "after-trip" {
		t.Fatalf("second selection downloaded %q", got)
	}
	if _, err := os.Stat(firstFile); err != nil {
		t.Fatalf("changing the selection removed an archived file: %v", err)
	}
	archive, _ = NewArchive(root)
	if summary := archive.Summary(); summary.Archived != 2 || summary.Blockers != 0 {
		t.Fatalf("unexpected summary after changing selection: %+v", summary)
	}

	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-all")); err != nil {
		t.Fatal(err)
	}
	if got := fake.takeDownloads(); got != "before-trip,trip-lapse,trip-photo" {
		t.Fatalf("--all downloaded %q", got)
	}
	archive, _ = NewArchive(root)
	if archive.Data.Selection != nil {
		t.Fatalf("--all kept a selection: %+v", archive.Data.Selection)
	}
}

func TestArchiveUsesTheSavedTimeZone(t *testing.T) {
	fake := newFakeGoPro(t, []map[string]any{
		{"id": "evening", "filename": "GX011187.MP4", "file_size": 7, "captured_at": "2026-08-28T15:15:28Z", "type": "Video"},
		{"id": "after-midnight", "filename": "GX011194.MP4", "file_size": 7, "captured_at": "2026-08-29T00:38:44Z", "type": "Video"},
		{"id": "next-day", "filename": "GX011197.MP4", "file_size": 7, "captured_at": "2026-08-29T12:00:00Z", "type": "Video"},
	})
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")
	args := []string{"-out", root, "-env-file", envPath, "-state-dir", filepath.Join(root, "no-legacy"), "-parallel", "1"}

	if err := archiveCommand(context.Background(), append(args, "-from", "2026-08-28", "-to", "2026-08-28", "-tz", "America/New_York")); err != nil {
		t.Fatal(err)
	}
	if got := fake.takeDownloads(); got != "after-midnight,evening" {
		t.Fatalf("gopro.com Aug 28 in New York downloaded %q", got)
	}
	archive, _ := NewArchive(root)
	if archive.Data.Selection == nil || archive.Data.Selection.Zone != "America/New_York" {
		t.Fatalf("time zone was not saved: %+v", archive.Data.Selection)
	}
	inspection, err := InspectLibrary(context.Background(), root, envPath, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Total != 2 || inspection.Earliest != "2026-08-28" || inspection.Latest != "2026-08-28" {
		t.Fatalf("saved zone was not applied on inspection: %+v", inspection)
	}

	if err := archiveCommand(context.Background(), append(args, "-from", "2026-08-28", "-to", "2026-08-28", "-camera-clock")); err != nil {
		t.Fatal(err)
	}
	if got := fake.takeDownloads(); got != "" {
		t.Fatalf("camera clock selection downloaded %q", got)
	}
	archive, _ = NewArchive(root)
	if archive.Data.Selection.Zone != "" || archive.Data.Items["after-midnight"] == nil || !archive.IsArchived("after-midnight") {
		t.Fatalf("camera clock selection lost the zone change or an archived file: %+v", archive.Data.Selection)
	}
}

func TestArchiveRejectsAnEmptySelection(t *testing.T) {
	fake := newFakeGoPro(t, tripLibrary())
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")

	err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-from", "2027-01-01"))
	if err == nil || !strings.Contains(err.Error(), "no GoPro media matches") {
		t.Fatalf("empty selection was accepted: %v", err)
	}
	if got := fake.takeDownloads(); got != "" {
		t.Fatalf("empty selection downloaded %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, controlName, "manifest.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty selection wrote a manifest: %v", err)
	}
}

func TestArchiveRejectsInvalidSelectionFlags(t *testing.T) {
	root := filepath.Join(t.TempDir(), "vlog")
	err := archiveCommand(context.Background(), archiveArgs(root, "unused", "-from", "2026-09-14", "-to", "2026-09-12"))
	var exit exitError
	if !errors.As(err, &exit) || exit.code != 2 {
		t.Fatalf("expected a usage error, got %v", err)
	}
	if _, statErr := os.Stat(root); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid flags created the archive folder: %v", statErr)
	}
}

func TestInspectLibraryAppliesSelectionReadOnly(t *testing.T) {
	newFakeGoPro(t, tripLibrary())
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")
	selection, err := ParseSelection("2026-09-12", "2026-09-14", "", "")
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := InspectLibrary(context.Background(), root, envPath, 100, &selection)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Total != 3 || inspection.Unselected != 2 || inspection.Remaining != 3 || inspection.TotalBytes != 42 {
		t.Fatalf("unexpected inspection: %+v", inspection)
	}
	if inspection.Earliest != "2026-09-12" || inspection.Latest != "2026-09-14" {
		t.Fatalf("unexpected date range: %s to %s", inspection.Earliest, inspection.Latest)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inspection changed the archive folder: %v", err)
	}
}

func TestDeleteCommandRequiresConfirmation(t *testing.T) {
	newFakeGoPro(t, tripLibrary())
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")
	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-from", "2026-09-12", "-to", "2026-09-12")); err != nil {
		t.Fatal(err)
	}
	archive, _ := NewArchive(root)
	saved := filepath.Join(root, filepath.FromSlash(archive.Item("trip-video").Files[0].Path))
	unrelated := filepath.Join(root, "edit-project.fcpbundle")
	if err := os.WriteFile(unrelated, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := deleteCommand(context.Background(), []string{}, strings.NewReader("DELETE\n")); err == nil {
		t.Fatal("delete ran without --out")
	}
	if err := deleteCommand(context.Background(), []string{"-out", root}, strings.NewReader("delete\n")); err == nil {
		t.Fatal("delete accepted the wrong confirmation")
	}
	if _, err := os.Stat(saved); err != nil {
		t.Fatalf("unconfirmed delete removed media: %v", err)
	}

	if err := deleteCommand(context.Background(), []string{"-out", root}, strings.NewReader("DELETE\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(saved); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("confirmed delete kept media: %v", err)
	}
	if body, err := os.ReadFile(unrelated); err != nil || string(body) != "mine" {
		t.Fatalf("delete changed an unrelated file: %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(root, controlName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("delete kept archive records: %v", err)
	}
}

func TestDeleteCommandYesSkipsPrompt(t *testing.T) {
	newFakeGoPro(t, tripLibrary())
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")
	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-type", "Photo")); err != nil {
		t.Fatal(err)
	}
	if err := deleteCommand(context.Background(), []string{"-out", root, "-yes"}, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanLocalArchive(root); err == nil {
		t.Fatal("archive still exists after delete --yes")
	}
}
