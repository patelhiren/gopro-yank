package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestDemoStartsWithReadOnlyLibrary(t *testing.T) {
	model := newTUIModel(context.Background(), "test", true)
	view := model.View().Content
	for _, expected := range []string{"GOPRO LIBRARY", "847", "Nothing was downloaded", "choose media"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("demo view does not contain %q:\n%s", expected, view)
		}
	}
}

func TestArchiveNeedsConfirmation(t *testing.T) {
	model := newTUIModel(context.Background(), "test", true)
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(tuiModel)
	if model.screen != screenConfirm {
		t.Fatalf("enter started an archive without confirmation: %v", model.screen)
	}
	view := model.View().Content
	for _, expected := range []string{"START ARCHIVING?", "never deletes cloud or archived media", "start archiving"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("confirmation does not contain %q:\n%s", expected, view)
		}
	}
}

func TestNormalizeArchivePathExpandsHome(t *testing.T) {
	root, err := normalizeArchivePath("~/GoPro Test")
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if root != filepath.Join(home, "GoPro Test") {
		t.Fatalf("unexpected path: %s", root)
	}
}

func TestStoppedArchiveReturnsHomeWithoutClaimingCompletion(t *testing.T) {
	model := newTUIModel(context.Background(), "test", true)
	model.screen = screenProgress
	model.busy = true
	updated, _ := model.Update(archiveMessage{err: context.Canceled})
	model = updated.(tuiModel)
	if model.screen != screenHome || model.busy || !strings.Contains(model.status, "Stopped safely") {
		t.Fatalf("unexpected stopped state: screen=%v busy=%v status=%q", model.screen, model.busy, model.status)
	}
	if strings.Contains(model.View().Content, "EXPORT COMPLETE") {
		t.Fatal("stopped archive claimed completion")
	}
}

func TestDeleteLocalArchiveRequiresExactConfirmation(t *testing.T) {
	root := t.TempDir()
	archive, _ := NewArchive(root)
	item := testMedia("delete-confirm")
	if _, err := archive.RecordSnapshot([]MediaItem{item}, "user", nil); err != nil {
		t.Fatal(err)
	}
	addArchived(t, archive, item, "originals/delete-confirm.MP4", []byte("source"))

	model := newTUIModel(context.Background(), "test", false)
	model.archiveRoot = root
	model.archive = archive
	actions := model.homeActions()
	for index, action := range actions {
		if action.id == "delete" {
			model.cursor = index
			break
		}
	}
	updated, _ := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(tuiModel)
	if model.screen != screenDeleteConfirm {
		t.Fatalf("delete action did not open confirmation: %v", model.screen)
	}
	view := model.View().Content
	for _, expected := range []string{"DELETE LOCAL ARCHIVE?", "Recorded files", "Nothing will be deleted from GoPro", "folder itself will stay"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("delete confirmation does not contain %q:\n%s", expected, view)
		}
	}

	model.deleteInput.SetValue("delete")
	updated, command := model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(tuiModel)
	if command != nil || model.busy || model.deleteInput.Err == nil {
		t.Fatal("lowercase confirmation started deletion")
	}
	if _, err := os.Stat(filepath.Join(root, "originals", "delete-confirm.MP4")); err != nil {
		t.Fatalf("failed confirmation deleted media: %v", err)
	}

	model.deleteInput.SetValue("DELETE")
	updated, command = model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	model = updated.(tuiModel)
	if command == nil || !model.busy {
		t.Fatal("exact confirmation did not start deletion")
	}
}

func TestDemoNeverLoadsARealArchive(t *testing.T) {
	model := newTUIModel(context.Background(), "test", true)
	model.archiveRoot = t.TempDir()
	archive, _ := NewArchive(model.archiveRoot)
	if err := archive.Save(); err != nil {
		t.Fatal(err)
	}
	model.reloadArchive()
	if model.archive.Exists {
		t.Fatal("demo loaded a real archive")
	}
}

func pressKeys(t *testing.T, model tuiModel, keys ...tea.KeyPressMsg) tuiModel {
	t.Helper()
	for _, key := range keys {
		updated, _ := model.Update(key)
		model = updated.(tuiModel)
	}
	return model
}

func typeText(t *testing.T, model tuiModel, text string) tuiModel {
	t.Helper()
	for _, character := range text {
		model = pressKeys(t, model, tea.KeyPressMsg(tea.Key{Code: character, Text: string(character)}))
	}
	return model
}

var (
	keyEnter = tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter})
	keyTab   = tea.KeyPressMsg(tea.Key{Code: tea.KeyTab})
	keyEsc   = tea.KeyPressMsg(tea.Key{Code: tea.KeyEscape})
)

// tripLibraryModel is a TUI showing the trip library as read from a local GoPro API.
func tripLibraryModel(t *testing.T) tuiModel {
	t.Helper()
	newFakeGoPro(t, tripLibrary())
	envPath := writeTestEnv(t)
	root := filepath.Join(t.TempDir(), "vlog")
	inspection, err := InspectLibrary(context.Background(), root, envPath, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	model := newTUIModel(context.Background(), "test", false)
	model.archiveRoot, model.envPath = root, envPath
	model.reloadArchive()
	model.inspection = &inspection
	model.screen = screenLibrary
	return model
}

func TestSelectionScreenFiltersLibraryWithoutDownloading(t *testing.T) {
	model := tripLibraryModel(t)
	if model.inspection.Total != 5 {
		t.Fatalf("unexpected library: %+v", model.inspection)
	}
	model = pressKeys(t, model, tea.KeyPressMsg(tea.Key{Code: 's', Text: "s"}))
	if model.screen != screenSelection {
		t.Fatalf("s did not open the selection screen: %v", model.screen)
	}
	view := model.View().Content
	for _, expected := range []string{"CHOOSE MEDIA", "gopro.com", "Types in your library: Photo, TimeLapseVideo, Video"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("selection screen does not contain %q:\n%s", expected, view)
		}
	}

	model = typeText(t, model, "2026-09-12")
	model = pressKeys(t, model, keyTab)
	model = typeText(t, model, "2026-09-14")
	model = pressKeys(t, model, keyTab)
	model = typeText(t, model, "video")
	model = pressKeys(t, model, keyEnter)
	if model.screen != screenLibrary || model.selectErr != nil {
		t.Fatalf("selection was not applied: screen=%v err=%v", model.screen, model.selectErr)
	}
	if model.selection == nil || model.selection.From != "2026-09-12" || model.selection.Zone == "" {
		t.Fatalf("unexpected selection: %+v", model.selection)
	}
	if model.inspection.Total != 1 || model.inspection.Unselected != 4 || model.inspection.Remaining != 1 {
		t.Fatalf("library was not filtered: %+v", model.inspection)
	}
	if view := model.View().Content; !strings.Contains(view, "Selected: 2026-09-12 to 2026-09-14") || !strings.Contains(view, "4 others not selected") {
		t.Fatalf("library view does not show the selection:\n%s", view)
	}
	if _, err := os.Stat(model.archiveRoot); !os.IsNotExist(err) {
		t.Fatalf("choosing media changed the archive folder: %v", err)
	}

	// Clearing every field returns to the whole library.
	model = pressKeys(t, model, tea.KeyPressMsg(tea.Key{Code: 's', Text: "s"}))
	for index := range model.selectInputs {
		model.selectInputs[index].SetValue("")
	}
	model = pressKeys(t, model, keyEnter)
	if model.inspection.Total != 5 || model.selection == nil || !model.selection.IsEmpty() {
		t.Fatalf("clearing did not select the whole library: %+v %+v", model.inspection, model.selection)
	}
}

func TestSelectionScreenKeepsBadInputOnScreen(t *testing.T) {
	model := tripLibraryModel(t)
	model = pressKeys(t, model, tea.KeyPressMsg(tea.Key{Code: 's', Text: "s"}))
	model = typeText(t, model, "2026-09-14")
	model = pressKeys(t, model, keyTab)
	model = typeText(t, model, "2026-09-12")
	model = pressKeys(t, model, keyEnter)
	if model.screen != screenSelection || model.selectErr == nil || !strings.Contains(model.View().Content, "--from must be before --to") {
		t.Fatalf("reversed range was accepted: screen=%v err=%v", model.screen, model.selectErr)
	}

	model.selectInputs[selectFrom].SetValue("2027-01-01")
	model.selectInputs[selectTo].SetValue("")
	model = pressKeys(t, model, keyEnter)
	if model.screen != screenSelection || model.selectErr == nil || !strings.Contains(model.selectErr.Error(), "no GoPro media matches") {
		t.Fatalf("empty selection was accepted: screen=%v err=%v", model.screen, model.selectErr)
	}

	model = pressKeys(t, model, keyEsc)
	if model.screen != screenLibrary || model.selection != nil || model.inspection.Total != 5 {
		t.Fatalf("esc changed the selection: screen=%v selection=%+v", model.screen, model.selection)
	}
}

func TestConfirmWarnsWhenFolderSelectionChanges(t *testing.T) {
	model := tripLibraryModel(t)
	if err := archiveCommand(context.Background(), archiveArgs(model.archiveRoot, model.envPath, "-type", "Photo")); err != nil {
		t.Fatal(err)
	}
	model.reloadArchive()
	inspection, err := SelectLibrary(model.archiveRoot, *model.inspection, nil)
	if err != nil {
		t.Fatal(err)
	}
	model.inspection = &inspection
	if model.inspection.Total != 1 || model.inspection.Archived != 1 {
		t.Fatalf("saved folder selection was not applied: %+v", model.inspection)
	}

	model = pressKeys(t, model, tea.KeyPressMsg(tea.Key{Code: 's', Text: "s"}))
	model.selectInputs[selectTypes].SetValue("Video")
	model = pressKeys(t, model, keyEnter, keyEnter)
	if model.screen != screenConfirm {
		t.Fatalf("expected confirmation, got %v", model.screen)
	}
	view := model.View().Content
	if !strings.Contains(view, "changes from Photo to Video") {
		t.Fatalf("confirmation does not warn about the selection change:\n%s", view)
	}
}

func TestChangingFolderUsesItsSavedSelection(t *testing.T) {
	model := tripLibraryModel(t)
	other := filepath.Join(t.TempDir(), "photos")
	if err := archiveCommand(context.Background(), archiveArgs(other, model.envPath, "-type", "Photo")); err != nil {
		t.Fatal(err)
	}
	model = pressKeys(t, model, tea.KeyPressMsg(tea.Key{Code: 'e', Text: "e"}))
	model.pathInput.SetValue(other)
	model = pressKeys(t, model, keyEnter)
	if model.screen != screenLibrary || model.inspection.Total != 1 || len(model.inspection.Selection.Types) != 1 {
		t.Fatalf("folder change ignored its saved selection: %+v", model.inspection)
	}
}
