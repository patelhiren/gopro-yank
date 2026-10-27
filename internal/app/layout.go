package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Archive layouts. Nested keeps each original in its own folder under
// originals/YYYY/MM/DD/. Date puts every file straight into a YYYY-MM-DD folder.
const (
	layoutNested = "nested"
	layoutDate   = "date"
)

func parseLayout(value string) (string, error) {
	switch value {
	case "", layoutNested, layoutDate:
		return value, nil
	}
	return "", fmt.Errorf("unknown layout %q; use date or nested", value)
}

func (a *Archive) Layout() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.Data.Layout == "" {
		return layoutNested
	}
	return a.Data.Layout
}

func nestedDir(item MediaItem) string {
	return "originals/" + datePath(itemCaptureDate(item)) + "/" + mediaSegment(item.ID)
}

// dateDir names an item's date folder, using the same dates as the archive's selection.
func (a *Archive) dateDir(item MediaItem) string {
	a.mu.RLock()
	var location *time.Location
	if a.Data.Selection != nil {
		location, _ = a.Data.Selection.location()
	}
	a.mu.RUnlock()
	if captured, ok := captureClock(itemCaptureDate(item), location); ok {
		return captured.Format("2006-01-02")
	}
	return "undated"
}

// chooseLayout settles the layout before downloading. A folder that already
// holds files keeps its layout; a new one uses requested, or the date layout
// when it holds a selection.
func (a *Archive) chooseLayout(requested string, selection Selection) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	hasFiles := false
	for _, record := range a.Data.Items {
		if len(record.Files) > 0 {
			hasFiles = true
			break
		}
	}
	if hasFiles {
		current := a.Data.Layout
		if current == "" {
			current = layoutNested
		}
		if requested != "" && requested != current {
			return fmt.Errorf("this folder uses the %s layout; run gopro-yank reorganize --out %s --layout %s first", current, a.Root, requested)
		}
		return nil
	}
	if requested == "" {
		requested = layoutNested
		if !selection.IsEmpty() {
			requested = layoutDate
		}
	}
	a.Data.Layout = requested
	return nil
}

// freePath returns dir/name, or dir/stem-N.ext when that name is already on
// disk or recorded for another item. current is the file's own path, which
// counts as free. Callers hold placeMu so two items cannot claim one name.
func (a *Archive) freePath(dir, name, owner, current string) (string, error) {
	claimed := map[string]bool{}
	a.mu.RLock()
	for id, record := range a.Data.Items {
		if id == owner {
			continue
		}
		for _, file := range record.Files {
			claimed[collisionKey(file.Path)] = true
		}
	}
	a.mu.RUnlock()
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	for number := 1; ; number++ {
		candidate := dir + "/" + name
		if number > 1 {
			candidate = fmt.Sprintf("%s/%s-%d%s", dir, stem, number, extension)
		}
		if candidate == current {
			return candidate, nil
		}
		if claimed[collisionKey(candidate)] {
			continue
		}
		target, err := secureJoin(a.Root, candidate)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}

func (a *Archive) recordedFiles(id string) []FileRecord {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if record := a.Data.Items[id]; record != nil {
		return append([]FileRecord(nil), record.Files...)
	}
	return nil
}

// sameFilesOnDisk reports whether previous already holds exactly the staged files.
func (a *Archive) sameFilesOnDisk(previous, staged []FileRecord) bool {
	if len(previous) == 0 || len(previous) != len(staged) {
		return false
	}
	want := map[string]int{}
	for _, file := range staged {
		want[file.SHA256]++
	}
	for _, file := range previous {
		target, err := secureJoin(a.Root, file.Path)
		if err != nil {
			return false
		}
		digest, size, err := sha256File(target)
		if err != nil || size != file.Size || digest != file.SHA256 || want[digest] == 0 {
			return false
		}
		want[digest]--
	}
	return true
}

// placeByDate moves staged files into the item's date folder. Staged record
// paths are bare file names. Earlier files for the item are kept in recovery.
func (a *Archive) placeByDate(item MediaItem, staged string, records []FileRecord) ([]FileRecord, string, error) {
	a.placeMu.Lock()
	defer a.placeMu.Unlock()
	previous := a.recordedFiles(item.ID)
	if a.sameFilesOnDisk(previous, records) {
		if err := os.RemoveAll(staged); err != nil {
			return nil, "", err
		}
		return previous, "", nil
	}

	recovered := ""
	for index, file := range previous {
		source, err := secureJoin(a.Root, file.Path)
		if err != nil {
			return nil, "", err
		}
		if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, "", err
		}
		if recovered == "" {
			recovered = filepath.ToSlash(filepath.Join(controlName, "recovery", fmt.Sprintf("%s-%d", mediaSegment(item.ID), time.Now().UnixNano())))
		}
		kept, err := secureJoin(a.Root, recovered+"/"+fmt.Sprintf("%d-%s", index+1, path.Base(file.Path)))
		if err != nil {
			return nil, "", err
		}
		if err := os.MkdirAll(filepath.Dir(kept), 0o755); err != nil {
			return nil, "", err
		}
		if err := os.Rename(source, kept); err != nil {
			return nil, "", err
		}
	}

	dir := a.dateDir(item)
	placed := []string{}
	undo := func() {
		for _, target := range placed {
			_ = os.Remove(target)
		}
	}
	for index := range records {
		relative, err := a.freePath(dir, records[index].Path, item.ID, "")
		if err != nil {
			undo()
			return nil, "", err
		}
		target, err := secureJoin(a.Root, relative)
		if err != nil {
			undo()
			return nil, "", err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			undo()
			return nil, "", err
		}
		if err := os.Rename(filepath.Join(staged, records[index].Path), target); err != nil {
			undo()
			return nil, "", err
		}
		placed = append(placed, target)
		records[index].Path = relative
	}
	_ = os.Remove(staged)
	return records, recovered, nil
}

type ReorganizeResult struct {
	Layout string
	Files  int
	Moved  int
}

// ErrDownloadActive means the archive's staging folder shows a download in progress.
var ErrDownloadActive = errors.New("a download appears to be running in this folder; wait for it to finish or stop it first")

// ReorganizeArchive moves every recorded file into layout and updates the
// records as it goes, so an interrupted run can simply be repeated.
func ReorganizeArchive(ctx context.Context, root, layout string) (ReorganizeResult, error) {
	if layout == "" {
		layout = layoutDate
	}
	if _, err := parseLayout(layout); err != nil {
		return ReorganizeResult{}, err
	}
	archive, err := requireArchive(root)
	if err != nil {
		return ReorganizeResult{}, err
	}
	if archive.downloadActive(10 * time.Minute) {
		return ReorganizeResult{}, ErrDownloadActive
	}
	return archive.reorganize(ctx, layout)
}

// downloadActive reports a partial download written to within window.
func (a *Archive) downloadActive(window time.Duration) bool {
	entries, err := os.ReadDir(a.StagingDir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".zip.part") {
			continue
		}
		if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) < window {
			return true
		}
	}
	return false
}

func (a *Archive) reorganize(ctx context.Context, layout string) (ReorganizeResult, error) {
	a.placeMu.Lock()
	defer a.placeMu.Unlock()
	result := ReorganizeResult{Layout: layout}

	a.mu.RLock()
	ids := make([]string, 0, len(a.Data.Items))
	for id, record := range a.Data.Items {
		if len(record.Files) > 0 {
			ids = append(ids, id)
		}
	}
	a.mu.RUnlock()
	sort.Strings(ids)

	emptied := map[string]bool{}
	for _, id := range ids {
		a.mu.RLock()
		record := a.Data.Items[id]
		item := MediaItem{ID: id, CapturedAt: record.CapturedAt, CreatedAt: record.CreatedAt}
		files := append([]FileRecord(nil), record.Files...)
		a.mu.RUnlock()

		dir := nestedDir(item)
		if layout == layoutDate {
			dir = a.dateDir(item)
		}
		for index, file := range files {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			result.Files++
			relative, err := a.freePath(dir, path.Base(file.Path), id, file.Path)
			if err != nil {
				return result, err
			}
			if relative == file.Path {
				continue
			}
			source, err := secureJoin(a.Root, file.Path)
			if err != nil {
				return result, fmt.Errorf("refusing to move an unsafe path: %w", err)
			}
			target, err := secureJoin(a.Root, relative)
			if err != nil {
				return result, fmt.Errorf("refusing to move to an unsafe path: %w", err)
			}
			if _, err := os.Lstat(source); err != nil {
				return result, fmt.Errorf("cannot move %s: %w; run gopro-yank verify --out %s", file.Path, err, a.Root)
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return result, err
			}
			if err := os.Rename(source, target); err != nil {
				return result, err
			}
			a.mu.Lock()
			a.Data.Items[id].Files[index].Path = relative
			err = a.saveLocked()
			a.mu.Unlock()
			if err != nil {
				return result, err
			}
			result.Moved++
			for parent := filepath.Dir(source); parent != a.Root && parent != filepath.Dir(parent); parent = filepath.Dir(parent) {
				emptied[parent] = true
			}
		}
	}

	a.mu.Lock()
	a.Data.Layout = layout
	err := a.saveLocked()
	a.mu.Unlock()
	if err != nil {
		return result, err
	}
	directories := make([]string, 0, len(emptied))
	for directory := range emptied {
		directories = append(directories, directory)
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.Count(directories[i], string(filepath.Separator)) > strings.Count(directories[j], string(filepath.Separator))
	})
	for _, directory := range directories {
		_ = os.Remove(directory)
	}
	if _, err := renderReport(a, nil); err != nil {
		return result, err
	}
	return result, nil
}
