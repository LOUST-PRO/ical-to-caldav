package caldav

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LOUST-PRO/ical-to-caldav/internal/config"
	"github.com/LOUST-PRO/ical-to-caldav/internal/source"
	"github.com/LOUST-PRO/ical-to-caldav/internal/store"
)

// Server implements a minimal CalDAV HTTP server using chi.
type Server struct {
	store  *store.BadgerStore
	sources []config.Source
}

// NewServer creates a CalDAV server backed by a BadgerStore.
func NewServer(s *store.BadgerStore, sources []config.Source) *Server {
	return &Server{store: s, sources: sources}
}

// Handler returns an http.Handler that routes CalDAV requests.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.handleRoot)
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("/.well-known/carddav", s.handleCardDAVRedirect)
	// CalDAV: principal, calendar-home-set, calendar multicollection
	mux.HandleFunc("OPTIONS /caldav/", s.handleOptions)
	mux.HandleFunc("PROPFIND /caldav/", s.handlePropfind)
	mux.HandleFunc("REPORT /caldav/", s.handleReport)
	return mux
}

// --- HTTP handlers ---

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	io.WriteString(w, "ical-to-caldav alive\n")
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"status":"ok","time":"%s"}`, time.Now().UTC().Format(time.RFC3339))
}

func (s *Server) handleCardDAVRedirect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Location", "/caldav/")
	w.WriteHeader(http.StatusMovedPermanently)
}

func (s *Server) handleOptions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Allow", "OPTIONS, PROPFIND, REPORT, GET, HEAD")
	w.Header().Set("DAV", "1, 2, 3, calendar-access, calendar-schedule, calendar-query")
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusNoContent)
}

// handlePropfind handles CalDAV PROPFIND (RFC 4791 §5.1)
func (s *Server) handlePropfind(w http.ResponseWriter, r *http.Request) {
	if r.Method != "PROPFIND" {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	depth := r.Header.Get("Depth")
	if depth == "" {
		depth = "1"
	}

	// Parse the propfind XML to know what properties are requested
	propfind, _ := parsePropfind(body)
	propnames := make(map[string]bool)
	for _, p := range propfind.Props {
		propnames[p.XMLName.Local] = true
	}

	// Determine what to return based on the request URL path
	urlPath := strings.TrimPrefix(r.URL.Path, "/caldav/")
	urlPath = strings.TrimPrefix(urlPath, "/")

	var responses []ResponseXML

	if urlPath == "" || urlPath == "/" {
		// Root: return the calendar-home-set
		responses = append(responses, s.buildCalendarHomeSetResponse(depth, propnames)...)
	} else if strings.HasPrefix(urlPath, "sources/") {
		// Individual source
		sourceID := strings.TrimPrefix(urlPath, "sources/")
		responses = append(responses, s.buildSourceResponse(sourceID, depth, propnames)...)
	} else {
		// Calendar object resource
		parts := strings.SplitN(urlPath, "/", 2)
		sourceID := parts[0]
		if len(parts) == 2 && parts[1] != "" {
			responses = append(responses, s.buildEventResponse(sourceID, parts[1], propnames)...)
		}
	}

	// Marshal response
	resp := MultiStatusXML{
		XMLName:   xml.Name{Space: "DAV:", Local: "multistatus"},
		Responses: responses,
	}
	data, err := xml.MarshalIndent(resp, "", "  ")
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	w.Write([]byte(xml.Header))
	w.Write(data)
}

func (s *Server) handleReport(w http.ResponseWriter, r *http.Request) {
	if r.Method != "REPORT" {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	urlPath := strings.TrimPrefix(r.URL.Path, "/caldav/")
	sourceID := strings.TrimPrefix(strings.TrimSuffix(urlPath, "/"), "sources/")

	// Parse calendar-query REPORT (RFC 4791 §7.2)
	query := parseCalendarQuery(body)
	events, err := s.store.ListEvents(sourceID, query.From, query.To)
	if err != nil {
		events = nil
	}

	// Filter by time range if specified
	var filtered []*source.Event
	for _, ev := range events {
		if query.From != nil && ev.End.Before(*query.From) {
			continue
		}
		if query.To != nil && ev.Start.After(*query.To) {
			continue
		}
		filtered = append(filtered, ev)
	}

	responses := make([]ResponseXML, 0, len(filtered))
	for _, ev := range filtered {
		responses = append(responses, s.buildEventXML(sourceID, ev))
	}

	resp := MultiStatusXML{
		XMLName:   xml.Name{Space: "DAV:", Local: "multistatus"},
		Responses: responses,
	}
	data, _ := xml.MarshalIndent(resp, "", "  ")
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(http.StatusMultiStatus)
	w.Write([]byte(xml.Header))
	w.Write(data)
}

// --- Response builders ---

func (s *Server) buildCalendarHomeSetResponse(depth string, props map[string]bool) []ResponseXML {
	responses := []ResponseXML{}

	if depth == "0" {
		return responses
	}

	// Return the calendar-home-set collection
	homeSet := ResponseXML{
		Href:     "/caldav/",
		Status:   "HTTP/1.1 200 OK",
		Props: []PropXML{
			{XMLName: xml.Name{Space: "DAV:", Local: "resourcetype"}, Value: "collection"},
			{XMLName: xml.Name{Space: "DAV:", Local: "displayname"}, Value: "iCal Subscriptions"},
		},
	}
	if hasProp(props, "calendar-home-set") {
		homeSet.Props = append(homeSet.Props, PropXML{
			XMLName: xml.Name{Space: "urn:ietf:params:xml:ns:caldav", Local: "calendar-home-set"},
			Href:    "/caldav/sources/",
		})
	}
	responses = append(responses, homeSet)

	// Return each source as a calendar collection
	for _, src := range s.sources {
		href := fmt.Sprintf("/caldav/sources/%s/", src.ID)
		r := ResponseXML{
			Href:   href,
			Status: "HTTP/1.1 200 OK",
			Props: []PropXML{
				{XMLName: xml.Name{Space: "DAV:", Local: "resourcetype"}, Value: "calendar-collection"},
				{XMLName: xml.Name{Space: "DAV:", Local: "displayname"}, Value: src.Name},
				{XMLName: xml.Name{Space: "DAV:", Local: "getcontenttype"}, Value: "text/calendar"},
				{XMLName: xml.Name{Space: "urn:ietf:params:xml:ns:caldav", Local: "supported-calendar-component-set"}, Value: "VEVENT"},
				{XMLName: xml.Name{Space: "http://apple.com/ns/ical/", Local: "calendar-color"}, Value: src.Color},
			},
		}
		if lastFetch, err := s.store.LastFetch(src.ID); err == nil {
			r.Props = append(r.Props, PropXML{
				XMLName: xml.Name{Space: "DAV:", Local: "getlastmodified"},
				Value:   lastFetch.Format(time.RFC1123Z),
			})
		}
		responses = append(responses, r)
	}

	return responses
}

func (s *Server) buildSourceResponse(sourceID, depth string, props map[string]bool) []ResponseXML {
	var src *config.Source
	for _, s := range s.sources {
		if s.ID == sourceID {
			src = &s
			break
		}
	}
	if src == nil {
		return nil
	}

	responses := []ResponseXML{
		{
			Href:   fmt.Sprintf("/caldav/sources/%s/", sourceID),
			Status: "HTTP/1.1 200 OK",
			Props: []PropXML{
				{XMLName: xml.Name{Space: "DAV:", Local: "resourcetype"}, Value: "calendar-collection"},
				{XMLName: xml.Name{Space: "DAV:", Local: "displayname"}, Value: src.Name},
				{XMLName: xml.Name{Space: "DAV:", Local: "getcontenttype"}, Value: "text/calendar"},
			},
		},
	}

	if depth == "1" {
		events, _ := s.store.ListEvents(sourceID, nil, nil)
		for _, ev := range events {
			responses = append(responses, s.buildEventXML(sourceID, ev))
		}
	}

	return responses
}

func (s *Server) buildEventResponse(sourceID, eventUID string, props map[string]bool) []ResponseXML {
	events, _ := s.store.ListEvents(sourceID, nil, nil)
	for _, ev := range events {
		if ev.UID == eventUID || ev.ID == eventUID {
			return []ResponseXML{s.buildEventXML(sourceID, ev)}
		}
	}
	return nil
}

func (s *Server) buildEventXML(sourceID string, ev *source.Event) ResponseXML {
	href := fmt.Sprintf("/caldav/sources/%s/%s.ics", sourceID, ev.UID)
	return ResponseXML{
		Href:   href,
		Status: "HTTP/1.1 200 OK",
		Props: []PropXML{
			{XMLName: xml.Name{Space: "DAV:", Local: "resourcetype"}, Value: ""},
			{XMLName: xml.Name{Space: "DAV:", Local: "getcontenttype"}, Value: "text/calendar"},
			{XMLName: xml.Name{Space: "DAV:", Local: "getlastmodified"}, Value: ev.Modified.Format(time.RFC1123Z)},
			{XMLName: xml.Name{Space: "DAV:", Local: "getcontentlength"}, Value: "0"},
		},
	}
}

// --- XML types ---

// ResponseXML represents a DAV:response element.
type ResponseXML struct {
	XMLName   xml.Name     `xml:"DAV: response"`
	Href     string       `xml:"DAV: href"`
	Status   string       `xml:"DAV: status,omitempty"`
	Props    []PropXML    `xml:"DAV: propstat>DAV: prop"`
	Responses []ResponseXML `xml:"DAV: response,omitempty"`
}

// MultiStatusXML is the root element for DAV:multistatus.
type MultiStatusXML struct {
	XMLName   xml.Name     `xml:"DAV: multistatus"`
	Responses []ResponseXML `xml:"DAV: response"`
}

// PropXML carries a single DAV property.
type PropXML struct {
	XMLName xml.Name
	Value   string `xml:",chardata"`
	Href    string `xml:"DAV: href,omitempty"`
}

// PropfindXML is a parsed PROPFIND request body.
type PropfindXML struct {
	Props []PropXML `xml:"DAV: prop"`
}

// CalendarQueryXML is a parsed calendar-query REPORT.
type CalendarQueryXML struct {
	From *time.Time
	To   *time.Time
}

// --- Parsers ---

func parsePropfind(data []byte) (*PropfindXML, error) {
	if len(data) == 0 {
		return &PropfindXML{}, nil
	}
	var p PropfindXML
	err := xml.Unmarshal(data, &p)
	return &p, err
}

func parseCalendarQuery(data []byte) *CalendarQueryXML {
	if len(data) == 0 {
		return &CalendarQueryXML{}
	}
	var q struct {
		Filter struct {
			CompFilter struct {
				Start struct {
					Time string `xml:"time-range,attr"`
				} `xml:"time-range"`
			} `xml:"comp-filter"`
		} `xml:"urn:ietf:params:xml:ns:caldav:filter"`
	}
	xml.Unmarshal(data, &q)
	// Note: full XPath parsing omitted for brevity; extend as needed.
	return &CalendarQueryXML{}
}

// hasProp returns true if the prop name was requested.
func hasProp(props map[string]bool, name string) bool {
	if len(props) == 0 {
		return true // all props
	}
	return props[name]
}
