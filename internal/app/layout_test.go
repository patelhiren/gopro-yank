package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

func recordedPaths(t *testing.T, root string) []string {
	t.Helper()
	archive, err := NewArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{}
	for _, record := range archive.Data.Items {
		for _, file := range record.Files {
			paths = append(paths, file.Path)
		}
	}
	sort.Strings(paths)
	return paths
}

func requireVerified(t *testing.T, root string) {
	t.Helper()
	archive, err := NewArchive(root)
	if err != nil {
		t.Fatal(err)
	}
	if result := archive.Verify(""); !result.OK() {
		t.Fatalf("archive failed verification: %+v", result.Issues)
	}
}

func TestSelectionArchiveUsesDateFolders(t *testing.T) {
	library := tripLibrary()
	library[1]["chapters"] = []string{"GX020002.MP4"}
	newFakeGoPro(t, library)
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")

	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-from", "2026-09-12", "-to", "2026-09-14")); err != nil {
		t.Fatal(err)
	}
	want := "2026-09-12/GX010002.MP4,2026-09-12/GX020002.MP4,2026-09-13/GOPR0003.JPG,2026-09-14/GX010004.MP4"
	if got := strings.Join(recordedPaths(t, root), ","); got != want {
		t.Fatalf("unexpected layout:\n got %s\nwant %s", got, want)
	}
	archive, _ := NewArchive(root)
	if archive.Layout() != layoutDate {
		t.Fatalf("layout not saved: %q", archive.Data.Layout)
	}
	if _, err := os.Stat(filepath.Join(root, "originals")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("date layout created originals/: %v", err)
	}
	requireVerified(t, root)
}

func TestWholeLibraryKeepsNestedLayout(t *testing.T) {
	newFakeGoPro(t, tripLibrary()[:1])
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "GoPro")
	if err := archiveCommand(context.Background(), archiveArgs(root, envPath)); err != nil {
		t.Fatal(err)
	}
	want := "originals/2026/09/10/" + mediaSegment("before-trip") + "/GX010001.MP4"
	if got := strings.Join(recordedPaths(t, root), ","); got != want {
		t.Fatalf("whole library layout changed: %s", got)
	}
}

func TestDateFoldersFollowTheSelectionTimeZone(t *testing.T) {
	newFakeGoPro(t, []map[string]any{
		{"id": "after-midnight", "filename": "GX011194.MP4", "file_size": 7, "captured_at": "2026-08-29T00:38:44Z", "type": "Video"},
	})
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")
	args := []string{"-out", root, "-env-file", envPath, "-state-dir", filepath.Join(root, "no-legacy"), "-parallel", "1", "-from", "2026-08-28", "-to", "2026-08-28", "-tz", "America/New_York"}
	if err := archiveCommand(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(recordedPaths(t, root), ","); got != "2026-08-28/GX011194.MP4" {
		t.Fatalf("date folder ignored the selection zone: %s", got)
	}
}

func TestDateFoldersKeepSameNamedFilesApart(t *testing.T) {
	library := []map[string]any{}
	for _, id := range []string{"camera-a", "camera-b", "camera-c"} {
		library = append(library, map[string]any{"id": id, "filename": "GX010001.MP4", "file_size": 7, "captured_at": "2026-09-12T09:00:00Z", "type": "Video"})
	}
	newFakeGoPro(t, library)
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")
	if err := archiveCommand(context.Background(), append(archiveArgs(root, envPath, "-from", "2026-09-12"), "-parallel", "3")); err != nil {
		t.Fatal(err)
	}
	want := "2026-09-12/GX010001-2.MP4,2026-09-12/GX010001-3.MP4,2026-09-12/GX010001.MP4"
	if got := strings.Join(recordedPaths(t, root), ","); got != want {
		t.Fatalf("same-named files collided: %s", got)
	}
	requireVerified(t, root)
}

func TestLayoutFlagOverridesAndCannotMixLayouts(t *testing.T) {
	newFakeGoPro(t, tripLibrary())
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")
	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-from", "2026-09-12", "-to", "2026-09-12", "-layout", "nested")); err != nil {
		t.Fatal(err)
	}
	if paths := recordedPaths(t, root); len(paths) != 1 || !strings.HasPrefix(paths[0], "originals/") {
		t.Fatalf("--layout nested was ignored: %v", paths)
	}
	err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-layout", "date"))
	if err == nil || !strings.Contains(err.Error(), "gopro-yank reorganize") {
		t.Fatalf("mixing layouts was allowed: %v", err)
	}
	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-layout", "sideways")); err == nil {
		t.Fatal("unknown layout was accepted")
	}
}

func TestReorganizeMovesAnExistingArchive(t *testing.T) {
	library := tripLibrary()
	library[1]["chapters"] = []string{"GX020002.MP4"}
	fake := newFakeGoPro(t, library)
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")
	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-from", "2026-09-12", "-to", "2026-09-13", "-layout", "nested")); err != nil {
		t.Fatal(err)
	}
	fake.takeDownloads()
	unrelated := filepath.Join(root, "originals", "notes.txt")
	if err := os.WriteFile(unrelated, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := reorganizeCommand(context.Background(), []string{"-out", root}); err != nil {
		t.Fatal(err)
	}
	want := "2026-09-12/GX010002.MP4,2026-09-12/GX020002.MP4,2026-09-13/GOPR0003.JPG"
	if got := strings.Join(recordedPaths(t, root), ","); got != want {
		t.Fatalf("reorganize produced %s", got)
	}
	requireVerified(t, root)
	if body, err := os.ReadFile(unrelated); err != nil || string(body) != "mine" {
		t.Fatalf("reorganize touched an unrelated file: %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(root, "originals", "2026")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty nested folders remain: %v", err)
	}
	checksums, _ := os.ReadFile(filepath.Join(root, controlName, "checksums.sha256"))
	if !strings.Contains(string(checksums), "2026-09-12/GX010002.MP4") || strings.Contains(string(checksums), "originals/") {
		t.Fatalf("checksums were not updated:\n%s", checksums)
	}

	// Repeating it is harmless, and later downloads use the new layout.
	result, err := ReorganizeArchive(context.Background(), root, layoutDate)
	if err != nil || result.Moved != 0 {
		t.Fatalf("second reorganize moved %d files: %v", result.Moved, err)
	}
	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-from", "2026-09-12", "-to", "2026-09-14")); err != nil {
		t.Fatal(err)
	}
	if got := fake.takeDownloads(); got != "trip-lapse" {
		t.Fatalf("resumed archive downloaded %q", got)
	}
	if paths := recordedPaths(t, root); !strings.Contains(strings.Join(paths, ","), "2026-09-14/GX010004.MP4") {
		t.Fatalf("new download did not use date folders: %v", paths)
	}

	// And back again.
	if err := reorganizeCommand(context.Background(), []string{"-out", root, "-layout", "nested"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range recordedPaths(t, root) {
		if !strings.HasPrefix(path, "originals/") {
			t.Fatalf("nested reorganize left %s", path)
		}
	}
	requireVerified(t, root)
}

func TestReorganizeRefusesDuringADownload(t *testing.T) {
	root := t.TempDir()
	archive, _ := NewArchive(root)
	item := testMedia("busy")
	if _, err := archive.RecordSnapshot([]MediaItem{item}, "user", nil); err != nil {
		t.Fatal(err)
	}
	addArchived(t, archive, item, "originals/busy/GX010001.MP4", []byte("source"))
	if err := os.MkdirAll(archive.StagingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(archive.StagingDir, mediaSegment("other")+".zip.part")
	if err := os.WriteFile(partial, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReorganizeArchive(context.Background(), root, layoutDate); !errors.Is(err, ErrDownloadActive) {
		t.Fatalf("reorganize ran during a download: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "originals", "busy", "GX010001.MP4")); err != nil {
		t.Fatalf("refused reorganize moved a file: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(partial, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := ReorganizeArchive(context.Background(), root, layoutDate); err != nil {
		t.Fatalf("a stale partial download blocked reorganize: %v", err)
	}
}

func TestDateLayoutRepairKeepsTheDamagedFile(t *testing.T) {
	fake := newFakeGoPro(t, tripLibrary())
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")
	args := archiveArgs(root, envPath, "-from", "2026-09-12", "-to", "2026-09-12")
	if err := archiveCommand(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	fake.takeDownloads()
	saved := filepath.Join(root, "2026-09-12", "GX010002.MP4")
	if err := os.WriteFile(saved, []byte("damaged-bytes!!!"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := archiveCommand(context.Background(), args); err != nil {
		t.Fatalf("repair run failed: %v", err)
	}
	if got := fake.takeDownloads(); got != "trip-video" {
		t.Fatalf("damaged file was not downloaded again: %q", got)
	}
	if got := strings.Join(recordedPaths(t, root), ","); got != "2026-09-12/GX010002.MP4" {
		t.Fatalf("repaired file moved: %s", got)
	}
	requireVerified(t, root)
	archive, _ := NewArchive(root)
	replaced := archive.Item("trip-video").ReplacedArtifacts
	if len(replaced) != 1 {
		t.Fatalf("damaged file was not kept: %+v", replaced)
	}
	entries, _ := os.ReadDir(filepath.Join(root, filepath.FromSlash(replaced[0].Path)))
	if len(entries) != 1 {
		t.Fatalf("recovery folder holds %d files", len(entries))
	}
	if body, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(replaced[0].Path), entries[0].Name())); string(body) != "damaged-bytes!!!" {
		t.Fatalf("recovery holds %q", body)
	}
}

func TestDeleteClearsDateFolders(t *testing.T) {
	newFakeGoPro(t, tripLibrary())
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")
	if err := archiveCommand(context.Background(), archiveArgs(root, envPath, "-from", "2026-09-12", "-to", "2026-09-14")); err != nil {
		t.Fatal(err)
	}
	if err := deleteCommand(context.Background(), []string{"-out", root, "-yes"}, strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("delete left %d entries: %v", len(entries), err)
	}
}
