package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Fetcher fetches and parses an iCal URL.
type Fetcher struct {
	client *http.Client
}

// NewFetcher creates a Fetcher with sensible defaults.
func NewFetcher() *Fetcher {
	return &Fetcher{
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// Fetch retrieves the iCal data from url and returns parsed Events.
func (f *Fetcher) Fetch(ctx context.Context, url string) ([]*Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("User-Agent", "ical-to-caldav/1.0")
	req.Header.Set("Accept", "text/calendar,application/calendar+xml")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("fetch %s: HTTP %d: %s", url, resp.StatusCode, string(body))
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return Parse(data)
}

// Event represents a calendar event extracted from iCal.
type Event struct {
	ID          string
	UID         string
	Summary     string
	Description string
	Location    string
	Start       time.Time
	End         time.Time
	AllDay      bool
	Recurrence  string
	Modified    time.Time
}

// Parse extracts Events from raw iCal (ICS) bytes.
func Parse(data []byte) ([]*Event, error) {
	var events []*Event
	var current *Event
	var inEvent bool
	var foldedLine string

	for _, line := range strings.Split(string(data), "\n") {
		// Skip empty continuation lines
		if line == "" {
			continue
		}

		// Unfold: continuation lines start with space or tab (RFC 5545 §3.1)
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			foldedLine += line[1:]
			continue
		}

		// Process the accumulated line
		procLine := foldedLine
		foldedLine = line

		if procLine == "" {
			continue
		}

		if strings.HasPrefix(procLine, "BEGIN:VEVENT") {
			inEvent = true
			current = &Event{}
			continue
		}
		if strings.HasPrefix(procLine, "END:VEVENT") {
			if current != nil && current.UID != "" {
				events = append(events, current)
			}
			current = nil
			inEvent = false
			continue
		}
		if !inEvent || current == nil {
			continue
		}

		colon := strings.IndexByte(procLine, ':')
		if colon < 0 {
			continue
		}
		key := strings.ToUpper(procLine[:colon])
		value := procLine[colon+1:]

		// Strip PARAMs from key (e.g. DTSTART;VALUE=DATE:20240101 → DTSTART)
		if semi := strings.IndexByte(key, ';'); semi >= 0 {
			key = key[:semi]
		}

		switch key {
		case "UID":
			current.UID = value
			current.ID = value
		case "SUMMARY":
			current.Summary = unescape(value)
		case "DESCRIPTION":
			current.Description = unescape(value)
		case "LOCATION":
			current.Location = unescape(value)
		case "DTSTART":
			if current.Start, current.AllDay = parseDT(value); current.AllDay {
				current.Start = time.Date(current.Start.Year(), current.Start.Month(), current.Start.Day(), 0, 0, 0, 0, time.UTC)
			}
		case "DTEND":
			current.End, _ = parseDT(value)
		case "RRULE":
			current.Recurrence = value
		case "LAST-MODIFIED":
			current.Modified, _ = parseDT(value)
		}
	}

	return events, nil
}

// parseDT parses an iCal DT value (YYYYMMDD or YYYYMMDDTHHMMSS or with TZ).
func parseDT(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	// DATE-only format: YYYYMMDD
	if len(value) == 8 {
		t, err := time.Parse("20060102", value)
		if err != nil {
			return time.Time{}, false
		}
		return t, true
	}
	// DATETIME format: YYYYMMDDTHHMMSSZ or YYYYMMDDTHHMMSS
	normalized := strings.ReplaceAll(strings.ReplaceAll(value, "-", ""), ":", "")
	normalized = strings.TrimSuffix(normalized, "Z")
	for _, fmt := range []string{"20060102T150405", "20060102T150405Z"} {
		if t, err := time.Parse(fmt, normalized); err == nil {
			return t, false
		}
	}
	return time.Time{}, false
}

// unescape unescapes iCal text values (RFC 5545 §3.3.3).
func unescape(s string) string {
	s = strings.ReplaceAll(s, `\N`, "\n")
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\,`, ",")
	s = strings.ReplaceAll(s, `\;`, ";")
	s = strings.ReplaceAll(s, `\\`, "\\")
	return s
}
