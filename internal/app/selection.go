package app

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// Selection narrows an archive to part of the GoPro library.
//
// GoPro stores the camera's clock in captured_at but labels it UTC, and
// gopro.com converts that label to the browser's time zone. With Zone set,
// dates are matched the way gopro.com shows them in that zone; with Zone empty,
// they are matched against the camera clock.
type Selection struct {
	From  string   `json:"from,omitempty"`
	To    string   `json:"to,omitempty"`
	Types []string `json:"types,omitempty"`
	Zone  string   `json:"zone,omitempty"`
}

type selectionLayout struct {
	layout    string
	precision time.Duration
}

var selectionLayouts = []selectionLayout{
	{"2006-01-02", 24 * time.Hour},
	{"2006-01-02T15:04", time.Minute},
	{"2006-01-02 15:04", time.Minute},
	{"2006-01-02T15:04:05", time.Second},
	{"2006-01-02 15:04:05", time.Second},
}

// parseSelectionTime returns the wall-clock time and the precision it was
// written with, so a --to bound can include the whole day, minute or second.
func parseSelectionTime(value string) (time.Time, time.Duration, error) {
	for _, candidate := range selectionLayouts {
		if parsed, err := time.Parse(candidate.layout, value); err == nil {
			return parsed, candidate.precision, nil
		}
	}
	return time.Time{}, 0, fmt.Errorf("unrecognized date %q; use YYYY-MM-DD or YYYY-MM-DDTHH:MM", value)
}

// ParseSelection validates command-line values. An empty zone matches the
// camera clock. A selection without from, to or types selects the whole library.
func ParseSelection(from, to, types, zone string) (Selection, error) {
	selection := Selection{From: strings.TrimSpace(from), To: strings.TrimSpace(to)}
	for _, kind := range strings.Split(types, ",") {
		if kind = strings.TrimSpace(kind); kind != "" && !slices.ContainsFunc(selection.Types, func(existing string) bool { return strings.EqualFold(existing, kind) }) {
			selection.Types = append(selection.Types, kind)
		}
	}
	if selection.From != "" || selection.To != "" {
		selection.Zone = strings.TrimSpace(zone)
	}
	if _, err := selection.location(); err != nil {
		return Selection{}, err
	}
	start, end, err := selection.bounds()
	if err != nil {
		return Selection{}, err
	}
	if !start.IsZero() && !end.IsZero() && !start.Before(end) {
		return Selection{}, errors.New("--from must be before --to")
	}
	return selection, nil
}

// bounds returns an inclusive start and exclusive end; zero means unbounded.
func (s Selection) bounds() (time.Time, time.Time, error) {
	var start, end time.Time
	if s.From != "" {
		parsed, _, err := parseSelectionTime(s.From)
		if err != nil {
			return start, end, fmt.Errorf("--from: %w", err)
		}
		start = parsed
	}
	if s.To != "" {
		parsed, precision, err := parseSelectionTime(s.To)
		if err != nil {
			return start, end, fmt.Errorf("--to: %w", err)
		}
		end = parsed.Add(precision)
	}
	return start, end, nil
}

// location returns the zone dates are shown in, or nil for the camera clock.
func (s Selection) location() (*time.Location, error) {
	if s.Zone == "" {
		return nil, nil
	}
	location, err := time.LoadLocation(s.Zone)
	if err != nil {
		return nil, fmt.Errorf("unknown time zone %q; use a name like America/Los_Angeles", s.Zone)
	}
	return location, nil
}

func (s Selection) IsEmpty() bool { return s.From == "" && s.To == "" && len(s.Types) == 0 }

func (s Selection) Equal(other Selection) bool {
	return s.From == other.From && s.To == other.To && s.Zone == other.Zone && slices.Equal(s.Types, other.Types)
}

func (s Selection) String() string {
	if s.IsEmpty() {
		return "whole library"
	}
	parts := []string{}
	switch {
	case s.From != "" && s.To != "":
		parts = append(parts, s.From+" to "+s.To)
	case s.From != "":
		parts = append(parts, "from "+s.From)
	case s.To != "":
		parts = append(parts, "through "+s.To)
	}
	if s.From != "" || s.To != "" {
		if s.Zone == "" {
			parts = append(parts, "camera clock")
		} else {
			parts = append(parts, "gopro.com dates in "+s.Zone)
		}
	}
	if len(s.Types) > 0 {
		parts = append(parts, strings.Join(s.Types, ", "))
	}
	return strings.Join(parts, " · ")
}

// captureClock reads a GoPro timestamp as the wall-clock time to match: the
// camera clock when location is nil, otherwise the time gopro.com shows there.
// Timestamps without a zone are treated as UTC, as gopro.com does.
func captureClock(value string, location *time.Location) (time.Time, bool) {
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			if location != nil {
				parsed = parsed.In(location)
			}
			return time.Date(parsed.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), parsed.Nanosecond(), time.UTC), true
		}
	}
	return time.Time{}, false
}

func itemCaptureDate(item MediaItem) string {
	if item.CapturedAt != "" {
		return item.CapturedAt
	}
	return item.CreatedAt
}

func (s Selection) matches(item MediaItem, start, end time.Time, location *time.Location) bool {
	if len(s.Types) > 0 && !slices.ContainsFunc(s.Types, func(kind string) bool { return strings.EqualFold(kind, item.MediaType) }) {
		return false
	}
	if start.IsZero() && end.IsZero() {
		return true
	}
	captured, ok := captureClock(itemCaptureDate(item), location)
	if !ok {
		return false
	}
	return (start.IsZero() || !captured.Before(start)) && (end.IsZero() || captured.Before(end))
}

// Filter returns the selected items in their original order.
func (s Selection) Filter(items []MediaItem) ([]MediaItem, error) {
	if s.IsEmpty() {
		return items, nil
	}
	start, end, err := s.bounds()
	if err != nil {
		return nil, err
	}
	location, err := s.location()
	if err != nil {
		return nil, err
	}
	selected := make([]MediaItem, 0, len(items))
	for _, item := range items {
		if s.matches(item, start, end, location) {
			selected = append(selected, item)
		}
	}
	return selected, nil
}

// localZoneName returns this computer's IANA time zone, which gopro.com uses in
// its browser, so a saved selection keeps its meaning on another computer.
func localZoneName() string {
	candidates := []string{strings.TrimPrefix(os.Getenv("TZ"), ":")}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if index := strings.Index(target, "zoneinfo/"); index >= 0 {
			candidates = append(candidates, target[index+len("zoneinfo/"):])
		}
	}
	for _, name := range candidates {
		if name != "" && name != "Local" {
			if _, err := time.LoadLocation(name); err == nil {
				return name
			}
		}
	}
	return "Local"
}

// selectionFromFlags returns the selection requested on the command line: nil keeps
// the archive's saved selection, and --all explicitly selects the whole library.
// Dates follow gopro.com in zone, this computer's zone when empty, unless cameraClock.
func selectionFromFlags(from, to, types, zone string, cameraClock, all bool) (*Selection, error) {
	if cameraClock && zone != "" {
		return nil, errors.New("--camera-clock cannot be combined with --tz")
	}
	if all {
		if from != "" || to != "" || types != "" {
			return nil, errors.New("--all cannot be combined with --from, --to or --type")
		}
		return &Selection{}, nil
	}
	if from == "" && to == "" && types == "" {
		return nil, nil
	}
	if cameraClock {
		zone = ""
	} else if zone == "" {
		zone = localZoneName()
	}
	selection, err := ParseSelection(from, to, types, zone)
	if err != nil {
		return nil, err
	}
	return &selection, nil
}

// resolveSelection picks the requested selection, or the one saved with the archive.
func resolveSelection(archive *Archive, requested *Selection) Selection {
	if requested != nil {
		return *requested
	}
	archive.mu.RLock()
	defer archive.mu.RUnlock()
	if archive.Data.Selection != nil {
		return *archive.Data.Selection
	}
	return Selection{}
}

// SetSelection records the archive's selection. When it changes, placeholder
// records with no saved files are dropped so media outside the new selection
// is not reported as pending. Archived files and their records are kept.
func (a *Archive) SetSelection(selection Selection) {
	a.mu.Lock()
	defer a.mu.Unlock()
	previous := Selection{}
	if a.Data.Selection != nil {
		previous = *a.Data.Selection
	}
	if !previous.Equal(selection) {
		for id, record := range a.Data.Items {
			if len(record.Files) == 0 && record.Status != "archived" && record.Status != "manual" {
				delete(a.Data.Items, id)
			}
		}
	}
	if selection.IsEmpty() {
		a.Data.Selection = nil
		return
	}
	copy := selection
	a.Data.Selection = &copy
}
