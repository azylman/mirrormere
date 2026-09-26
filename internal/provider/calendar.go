package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	ical "github.com/arran4/golang-ical"
	"github.com/teambition/rrule-go"
)

const (
	defaultCalendarTimeout = 10 * time.Second
	maxCalendarFeedBytes   = 2 * 1024 * 1024 // 2MB Slowloris defense ceiling
)

// CalendarEvent represents a single normalized calendar event per SPEC-007 §1.
type CalendarEvent struct {
	ID           string `json:"id"`
	CalendarName string `json:"calendar_name"`
	Color        string `json:"color"`
	Title        string `json:"title"`
	Start        string `json:"start"`
	End          string `json:"end"`
	AllDay       bool   `json:"all_day"`
	Location     string `json:"location"`
	Description  string `json:"description"`
}

// CalendarSnapshot represents the normalized calendar provider payload per SPEC-007 §1.
type CalendarSnapshot struct {
	LastSync   string          `json:"last_sync"`
	SyncStatus string          `json:"sync_status"`
	Events     []CalendarEvent `json:"events"`
}

// CalendarSource defines configuration for a single upstream calendar feed.
type CalendarSource struct {
	Name        string `json:"name" yaml:"name"`
	Type        string `json:"type,omitempty" yaml:"type,omitempty"` // "ical" (default) or "caldav"
	URL         string `json:"url,omitempty" yaml:"url,omitempty"`
	URLEnv      string `json:"url_env,omitempty" yaml:"url_env,omitempty"`
	Color       string `json:"color,omitempty" yaml:"color,omitempty"`
	Enabled     bool   `json:"enabled" yaml:"enabled"`
	Username    string `json:"username,omitempty" yaml:"username,omitempty"`
	UsernameEnv string `json:"username_env,omitempty" yaml:"username_env,omitempty"`
	Password    string `json:"password,omitempty" yaml:"password,omitempty"`
	PasswordEnv string `json:"password_env,omitempty" yaml:"password_env,omitempty"`
	Token       string `json:"token,omitempty" yaml:"token,omitempty"`
	TokenEnv    string `json:"token_env,omitempty" yaml:"token_env,omitempty"`
}

// CalendarConfig defines the full instance configuration for calendar-agenda.
type CalendarConfig struct {
	WindowDaysPast   int              `json:"window_days_past" yaml:"window_days_past"`
	WindowDaysFuture int              `json:"window_days_future" yaml:"window_days_future"`
	View             string           `json:"view" yaml:"view"`
	Calendars        []CalendarSource `json:"calendars" yaml:"calendars"`
}

// CalendarProvider fetches, parses, expands recurrence rules, and normalizes iCal feeds per SPEC-007 §1.
type CalendarProvider struct {
	client       *http.Client
	config       *CalendarConfig
	nowFunc      func() time.Time
	logger       *slog.Logger
	caldavClient *CalDAVClient
}

// NewCalendarProvider constructs a standard CalendarProvider.
func NewCalendarProvider() Provider {
	return NewCalendarProviderWithClient(nil, nil)
}

// NewCalendarProviderWithClient constructs a CalendarProvider with custom HTTP client and clock.
func NewCalendarProviderWithClient(client *http.Client, nowFunc func() time.Time) *CalendarProvider {
	if client == nil {
		client = &http.Client{Timeout: defaultCalendarTimeout}
	}
	if nowFunc == nil {
		nowFunc = time.Now
	}
	logger := slog.Default()
	return &CalendarProvider{
		client:       client,
		nowFunc:      nowFunc,
		logger:       logger,
		caldavClient: NewCalDAVClient(client, logger),
	}
}

// Init initializes the calendar provider from configuration.
func (p *CalendarProvider) Init(ctx context.Context, rawConfig map[string]any, opts InitOptions) error {
	cfg, err := parseCalendarConfig(rawConfig, opts)
	if err != nil {
		return fmt.Errorf("calendar provider init failed: %w", err)
	}
	p.config = cfg
	return nil
}

func parseCalendarConfig(raw map[string]any, opts InitOptions) (*CalendarConfig, error) {
	cfg := &CalendarConfig{
		WindowDaysPast:   1,
		WindowDaysFuture: 14,
		View:             "agenda",
	}

	if v, ok := raw["window_days_past"].(int); ok && v >= 0 {
		cfg.WindowDaysPast = v
	} else if v, ok := raw["window_days_past"].(float64); ok && v >= 0 {
		cfg.WindowDaysPast = int(v)
	}

	if v, ok := raw["window_days_future"].(int); ok && v > 0 {
		cfg.WindowDaysFuture = v
	} else if v, ok := raw["window_days_future"].(float64); ok && v > 0 {
		cfg.WindowDaysFuture = int(v)
	}

	if v, ok := raw["view"].(string); ok && strings.TrimSpace(v) != "" {
		cfg.View = strings.TrimSpace(v)
	}

	var rawSources []map[string]any
	switch list := raw["calendars"].(type) {
	case []any:
		for idx, item := range list {
			m, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("calendar source at index %d is not a map", idx)
			}
			rawSources = append(rawSources, m)
		}
	case []map[string]any:
		rawSources = list
	}

	if len(rawSources) == 0 {
		return nil, errors.New("calendar provider requires at least one calendar in 'calendars'")
	}

	for idx, m := range rawSources {
		name, ok := m["name"].(string)
		if !ok || strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("calendar at index %d requires non-empty 'name'", idx)
		}
		name = strings.TrimSpace(name)

		var rawType string
		if t, ok := m["type"].(string); ok {
			rawType = t
		}
		sourceType := strings.ToLower(strings.TrimSpace(rawType))
		if sourceType == "" {
			sourceType = "ical"
		}
		if sourceType != "ical" && sourceType != "caldav" {
			return nil, fmt.Errorf("calendar %q: invalid type %q (must be 'ical' or 'caldav')", name, sourceType)
		}

		var rawURL string
		if u, ok := m["url"].(string); ok {
			rawURL = strings.TrimSpace(u)
		}

		var urlEnv string
		if ue, ok := m["url_env"].(string); ok {
			urlEnv = strings.TrimSpace(ue)
		}

		if rawURL == "" && urlEnv == "" {
			return nil, fmt.Errorf("calendar %q requires either 'url' or 'url_env'", name)
		}
		if rawURL != "" && urlEnv != "" {
			return nil, fmt.Errorf("calendar %q cannot specify both 'url' and 'url_env'", name)
		}

		resolvedURL := rawURL
		if urlEnv != "" {
			resolved := opts.GetSecret(fmt.Sprintf("calendars[%d].url", idx))
			if resolved == "" {
				resolved = opts.GetSecret(urlEnv)
			}
			if resolved == "" {
				resolved = os.Getenv(urlEnv)
			}
			if resolved == "" {
				return nil, fmt.Errorf("calendar %q: environment variable %q is not set or empty", name, urlEnv)
			}
			resolvedURL = resolved
		}

		var rawUsername string
		if u, ok := m["username"].(string); ok {
			rawUsername = strings.TrimSpace(u)
		}
		var usernameEnv string
		if ue, ok := m["username_env"].(string); ok {
			usernameEnv = strings.TrimSpace(ue)
		}
		if rawUsername != "" && usernameEnv != "" {
			return nil, fmt.Errorf("calendar %q cannot specify both 'username' and 'username_env'", name)
		}
		resolvedUsername := rawUsername
		if usernameEnv != "" {
			resolved := opts.GetSecret(fmt.Sprintf("calendars[%d].username", idx))
			if resolved == "" {
				resolved = opts.GetSecret(usernameEnv)
			}
			if resolved == "" {
				resolved = os.Getenv(usernameEnv)
			}
			if resolved == "" {
				return nil, fmt.Errorf("calendar %q: environment variable %q is not set or empty", name, usernameEnv)
			}
			resolvedUsername = resolved
		}

		var rawPassword string
		if p, ok := m["password"].(string); ok {
			rawPassword = strings.TrimSpace(p)
		}
		var passwordEnv string
		if pe, ok := m["password_env"].(string); ok {
			passwordEnv = strings.TrimSpace(pe)
		}
		if rawPassword != "" && passwordEnv != "" {
			return nil, fmt.Errorf("calendar %q cannot specify both 'password' and 'password_env'", name)
		}
		resolvedPassword := rawPassword
		if passwordEnv != "" {
			resolved := opts.GetSecret(fmt.Sprintf("calendars[%d].password", idx))
			if resolved == "" {
				resolved = opts.GetSecret(passwordEnv)
			}
			if resolved == "" {
				resolved = os.Getenv(passwordEnv)
			}
			if resolved == "" {
				return nil, fmt.Errorf("calendar %q: environment variable %q is not set or empty", name, passwordEnv)
			}
			resolvedPassword = resolved
		}

		var rawToken string
		if t, ok := m["token"].(string); ok {
			rawToken = strings.TrimSpace(t)
		}
		var tokenEnv string
		if te, ok := m["token_env"].(string); ok {
			tokenEnv = strings.TrimSpace(te)
		}
		if rawToken != "" && tokenEnv != "" {
			return nil, fmt.Errorf("calendar %q cannot specify both 'token' and 'token_env'", name)
		}
		resolvedToken := rawToken
		if tokenEnv != "" {
			resolved := opts.GetSecret(fmt.Sprintf("calendars[%d].token", idx))
			if resolved == "" {
				resolved = opts.GetSecret(tokenEnv)
			}
			if resolved == "" {
				resolved = os.Getenv(tokenEnv)
			}
			if resolved == "" {
				return nil, fmt.Errorf("calendar %q: environment variable %q is not set or empty", name, tokenEnv)
			}
			resolvedToken = resolved
		}

		// Auth exclusivity: Cannot specify both Basic Auth and Bearer Token
		if (resolvedUsername != "" || resolvedPassword != "") && resolvedToken != "" {
			return nil, fmt.Errorf("calendar %q cannot specify both Basic auth (username/password) and Bearer auth (token)", name)
		}

		// Basic auth completeness
		if resolvedUsername != "" && resolvedPassword == "" {
			return nil, fmt.Errorf("calendar %q: username provided without password", name)
		}
		if resolvedPassword != "" && resolvedUsername == "" {
			return nil, fmt.Errorf("calendar %q: password provided without username", name)
		}

		color := "#3b82f6"
		if c, ok := m["color"].(string); ok && strings.TrimSpace(c) != "" {
			color = strings.TrimSpace(c)
		}

		enabled := true
		if e, ok := m["enabled"].(bool); ok {
			enabled = e
		}

		cfg.Calendars = append(cfg.Calendars, CalendarSource{
			Name:        name,
			Type:        sourceType,
			URL:         resolvedURL,
			URLEnv:      urlEnv,
			Color:       color,
			Enabled:     enabled,
			Username:    resolvedUsername,
			UsernameEnv: usernameEnv,
			Password:    resolvedPassword,
			PasswordEnv: passwordEnv,
			Token:       resolvedToken,
			TokenEnv:    tokenEnv,
		})
	}

	return cfg, nil
}

// Fetch retrieves and parses iCal feeds across all configured calendars in parallel.
func (p *CalendarProvider) Fetch(ctx context.Context) (any, error) {
	if p.config == nil {
		return nil, errors.New("calendar provider not initialized")
	}

	now := p.nowFunc()
	windowStart := now.AddDate(0, 0, -p.config.WindowDaysPast)
	windowEnd := now.AddDate(0, 0, p.config.WindowDaysFuture)

	var enabledSources []CalendarSource
	for _, source := range p.config.Calendars {
		if source.Enabled {
			enabledSources = append(enabledSources, source)
		}
	}

	type fetchResult struct {
		source CalendarSource
		events []CalendarEvent
		err    error
	}

	results := make([]fetchResult, len(enabledSources))
	var wg sync.WaitGroup
	for i, src := range enabledSources {
		wg.Add(1)
		go func(idx int, s CalendarSource) {
			defer wg.Done()
			events, err := p.fetchCalendar(ctx, s, windowStart, windowEnd)
			results[idx] = fetchResult{source: s, events: events, err: err}
		}(i, src)
	}
	wg.Wait()

	var allEvents []CalendarEvent
	hasSuccess := false
	var fetchErrors []string

	for _, r := range results {
		if r.err != nil {
			p.logger.Warn("calendar feed fetch failed", "calendar", r.source.Name, "error", r.err)
			fetchErrors = append(fetchErrors, fmt.Sprintf("%s: %v", r.source.Name, r.err))
			continue
		}
		hasSuccess = true
		allEvents = append(allEvents, r.events...)
	}

	if !hasSuccess && len(fetchErrors) > 0 {
		return nil, fmt.Errorf("all calendar feeds failed: %s", strings.Join(fetchErrors, "; "))
	}

	// Sort chronologically ascending
	sortCalendarEvents(allEvents)

	syncStatus := "ok"
	if len(fetchErrors) > 0 {
		syncStatus = "partial"
	}

	return CalendarSnapshot{
		LastSync:   now.UTC().Format(time.RFC3339),
		SyncStatus: syncStatus,
		Events:     allEvents,
	}, nil
}

func (p *CalendarProvider) fetchCalendar(ctx context.Context, source CalendarSource, windowStart, windowEnd time.Time) ([]CalendarEvent, error) {
	if source.Type == "caldav" {
		return p.caldavClient.FetchCalendarEvents(ctx, source, windowStart, windowEnd)
	}

	targetURL := source.URL
	if strings.HasPrefix(targetURL, "webcal://") {
		targetURL = "https://" + strings.TrimPrefix(targetURL, "webcal://")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid request URL: %w", err)
	}
	req.Header.Set("User-Agent", "Mirrormere-Calendar/1.0")
	req.Header.Set("Accept", "text/calendar, application/ics, text/plain")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCalendarFeedBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading response body: %w", err)
	}
	if len(body) > maxCalendarFeedBytes {
		return nil, fmt.Errorf("calendar feed exceeds size limit of %d bytes", maxCalendarFeedBytes)
	}

	cal, err := ical.ParseCalendar(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("parsing ics calendar: %w", err)
	}

	return parseCalendarEvents(cal, source, windowStart, windowEnd), nil
}

func parseCalendarEvents(cal *ical.Calendar, source CalendarSource, windowStart, windowEnd time.Time) []CalendarEvent {
	var events []CalendarEvent
	recurrenceOverrides := make(map[string]map[string]bool) // uid -> set of recurrence-id timestamps

	// Pass 1: Discover modified/overridden occurrences (RECURRENCE-ID)
	for _, e := range cal.Events() {
		uidProp := e.GetProperty(ical.ComponentPropertyUniqueId)
		recIDProp := e.GetProperty(ical.ComponentPropertyRecurrenceId)
		if uidProp != nil && recIDProp != nil {
			uid := uidProp.Value
			if recurrenceOverrides[uid] == nil {
				recurrenceOverrides[uid] = make(map[string]bool)
			}
			recurrenceOverrides[uid][recIDProp.Value] = true
		}
	}

	// Pass 2: Parse events and expand recurrences
	for _, e := range cal.Events() {
		uid := ""
		if p := e.GetProperty(ical.ComponentPropertyUniqueId); p != nil {
			uid = p.Value
		}

		title := "Untitled Event"
		if p := e.GetProperty(ical.ComponentPropertySummary); p != nil && strings.TrimSpace(p.Value) != "" {
			title = strings.TrimSpace(p.Value)
		}

		location := ""
		if p := e.GetProperty(ical.ComponentPropertyLocation); p != nil {
			location = strings.TrimSpace(p.Value)
		}

		description := ""
		if p := e.GetProperty(ical.ComponentPropertyDescription); p != nil {
			description = strings.TrimSpace(p.Value)
		}

		dtStartProp := e.GetProperty(ical.ComponentPropertyDtStart)
		if dtStartProp == nil {
			continue
		}

		isAllDay := false
		if valParam, ok := dtStartProp.ICalParameters["VALUE"]; ok {
			for _, v := range valParam {
				if strings.ToUpper(v) == "DATE" {
					isAllDay = true
					break
				}
			}
		}
		if !isAllDay && len(dtStartProp.Value) == 8 {
			isAllDay = true
		}

		rruleProp := e.GetProperty(ical.ComponentPropertyRrule)
		recIDProp := e.GetProperty(ical.ComponentPropertyRecurrenceId)

		if rruleProp != nil && recIDProp == nil {
			// Recurring master event
			expanded := expandRecurrence(e, source, uid, title, location, description, isAllDay, windowStart, windowEnd, recurrenceOverrides[uid])
			events = append(events, expanded...)
		} else {
			// Single event or modified occurrence
			ev, ok := parseSingleEvent(e, source, uid, title, location, description, isAllDay, windowStart, windowEnd)
			if ok {
				events = append(events, ev)
			}
		}
	}

	return events
}

func parseSingleEvent(e *ical.VEvent, source CalendarSource, uid, title, location, description string, isAllDay bool, windowStart, windowEnd time.Time) (CalendarEvent, bool) {
	dtStartProp := e.GetProperty(ical.ComponentPropertyDtStart)
	dtEndProp := e.GetProperty(ical.ComponentPropertyDtEnd)

	startTime, err := parseICalDateTime(dtStartProp, isAllDay)
	if err != nil {
		return CalendarEvent{}, false
	}

	var endTime time.Time
	if dtEndProp != nil {
		if parsedEnd, err := parseICalDateTime(dtEndProp, isAllDay); err == nil {
			endTime = parsedEnd
		}
	} else if p := e.GetProperty(ical.ComponentPropertyDuration); p != nil {
		dur := parseICalDuration(p.Value)
		if dur > 0 {
			endTime = startTime.Add(dur)
		}
	}

	if endTime.IsZero() || endTime.Before(startTime) {
		if isAllDay {
			endTime = startTime
		} else {
			endTime = startTime.Add(time.Hour)
		}
	}

	// Check window intersection: [start, end] intersects [windowStart, windowEnd]
	if endTime.Before(windowStart) || startTime.After(windowEnd) {
		return CalendarEvent{}, false
	}

	startStr, endStr := formatEventRange(startTime, endTime, isAllDay)
	eventID := fmt.Sprintf("evt_%s_%d", sanitizeID(uid), startTime.Unix())

	return CalendarEvent{
		ID:           eventID,
		CalendarName: source.Name,
		Color:        source.Color,
		Title:        title,
		Start:        startStr,
		End:          endStr,
		AllDay:       isAllDay,
		Location:     location,
		Description:  description,
	}, true
}

func expandRecurrence(e *ical.VEvent, source CalendarSource, uid, title, location, description string, isAllDay bool, windowStart, windowEnd time.Time, overrides map[string]bool) []CalendarEvent {
	dtStartProp := e.GetProperty(ical.ComponentPropertyDtStart)
	rruleProp := e.GetProperty(ical.ComponentPropertyRrule)

	startTime, err := parseICalDateTime(dtStartProp, isAllDay)
	if err != nil {
		return nil
	}

	var duration time.Duration
	if dtEndProp := e.GetProperty(ical.ComponentPropertyDtEnd); dtEndProp != nil {
		endTime, err := parseICalDateTime(dtEndProp, isAllDay)
		if err == nil && endTime.After(startTime) {
			duration = endTime.Sub(startTime)
		}
	} else if p := e.GetProperty(ical.ComponentPropertyDuration); p != nil {
		dur := parseICalDuration(p.Value)
		if dur > 0 {
			duration = dur
		}
	}
	if duration <= 0 {
		if isAllDay {
			duration = 24 * time.Hour
		} else {
			duration = time.Hour
		}
	}

	// Collect EXDATE exclusions
	exdates := make(map[string]bool)
	for _, prop := range e.Properties {
		if prop.IANAToken == string(ical.ComponentPropertyExdate) {
			vals := strings.Split(prop.Value, ",")
			for _, v := range vals {
				v = strings.TrimSpace(v)
				if v != "" {
					exdates[v] = true
				}
			}
		}
	}

	ruleStr := rruleProp.Value
	upperRule := strings.ToUpper(ruleStr)
	if strings.Contains(upperRule, "FREQ=SECONDLY") || strings.Contains(upperRule, "FREQ=MINUTELY") {
		return nil
	}

	rule, err := rrule.StrToRRule(ruleStr)
	if err != nil {
		return nil
	}

	rule.DTStart(startTime)
	occurrences := rule.Between(windowStart.Add(-duration), windowEnd, true)
	const maxOccurrences = 500
	if len(occurrences) > maxOccurrences {
		occurrences = occurrences[:maxOccurrences]
	}

	var expanded []CalendarEvent
	for _, occ := range occurrences {
		// Check exclusions
		occStr := occ.Format("20060102T150405Z")
		occDateStr := occ.Format("20060102")
		if exdates[occStr] || exdates[occDateStr] {
			continue
		}

		// Check if overridden by a RECURRENCE-ID event
		if overrides != nil && (overrides[occStr] || overrides[occDateStr]) {
			continue
		}

		occEnd := occ.Add(duration)
		if occEnd.Before(windowStart) || occ.After(windowEnd) {
			continue
		}

		startStr, endStr := formatEventRange(occ, occEnd, isAllDay)
		eventID := fmt.Sprintf("evt_%s_%d", sanitizeID(uid), occ.Unix())

		expanded = append(expanded, CalendarEvent{
			ID:           eventID,
			CalendarName: source.Name,
			Color:        source.Color,
			Title:        title,
			Start:        startStr,
			End:          endStr,
			AllDay:       isAllDay,
			Location:     location,
			Description:  description,
		})
	}

	return expanded
}

func parseICalDateTime(prop *ical.IANAProperty, isAllDay bool) (time.Time, error) {
	if prop == nil {
		return time.Time{}, errors.New("nil property")
	}
	val := strings.TrimSpace(prop.Value)

	if isAllDay || len(val) == 8 {
		t, err := time.Parse("20060102", val)
		if err == nil {
			return t, nil
		}
	}

	// Check TZID parameter
	var loc *time.Location = time.UTC
	if tzid, ok := prop.ICalParameters["TZID"]; ok && len(tzid) > 0 {
		if l, err := time.LoadLocation(tzid[0]); err == nil {
			loc = l
		}
	}

	// Try common iCal formats
	layouts := []string{
		"20060102T150405Z",
		"20060102T150405",
		time.RFC3339,
	}

	for _, layout := range layouts {
		if strings.HasSuffix(layout, "Z") && !strings.HasSuffix(val, "Z") {
			continue
		}
		if t, err := time.ParseInLocation(layout, val, loc); err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse ical datetime %q", val)
}

func parseICalDuration(val string) time.Duration {
	// Simple ISO 8601 duration parser for P1D, PT1H, PT30M, etc.
	val = strings.TrimPrefix(val, "P")
	var dur time.Duration
	timePart := false

	var num int
	for i := 0; i < len(val); i++ {
		ch := val[i]
		if ch == 'T' {
			timePart = true
			continue
		}
		if ch >= '0' && ch <= '9' {
			num = num*10 + int(ch-'0')
			continue
		}
		switch ch {
		case 'D':
			dur += time.Duration(num) * 24 * time.Hour
		case 'H':
			dur += time.Duration(num) * time.Hour
		case 'M':
			if timePart {
				dur += time.Duration(num) * time.Minute
			} else {
				// Months approximation if any
				dur += time.Duration(num) * 30 * 24 * time.Hour
			}
		case 'S':
			dur += time.Duration(num) * time.Second
		}
		num = 0
	}
	return dur
}

func formatEventRange(start, end time.Time, isAllDay bool) (string, string) {
	if isAllDay {
		startStr := start.Format("2006-01-02")
		// In RFC 5545, DTEND for all-day events is non-inclusive.
		// e.g. DTSTART:20260925, DTEND:20260926 is 1 day.
		// If end is greater than start, normalize display end to inclusive date.
		endInclusive := end
		if end.After(start) && end.Sub(start) >= 24*time.Hour {
			endInclusive = end.Add(-24 * time.Hour)
		}
		endStr := endInclusive.Format("2006-01-02")
		if endStr < startStr {
			endStr = startStr
		}
		return startStr, endStr
	}
	return start.Format(time.RFC3339), end.Format(time.RFC3339)
}

func sortCalendarEvents(events []CalendarEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		a, b := events[i], events[j]
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		if a.AllDay != b.AllDay {
			return a.AllDay // all-day events first
		}
		return a.Title < b.Title
	})
}

func sanitizeID(uid string) string {
	s := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, uid)
	if len(s) > 32 {
		return s[:32]
	}
	if s == "" {
		return "event"
	}
	return s
}

// Subscribe satisfies Provider interface (no-op for pull-based calendar feeds).
func (p *CalendarProvider) Subscribe(ctx context.Context, eventSink chan<- WidgetPayload) error {
	return nil
}

// Shutdown gracefully satisfies Provider interface.
func (p *CalendarProvider) Shutdown(ctx context.Context) error {
	return nil
}
