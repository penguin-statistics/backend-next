package mcp

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"exusiai.dev/gommon/constant"
	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/guregu/null.v3"

	"exusiai.dev/backend-next/internal/service"
)

// The status of an event on a server at a time
const (
	eventOpen     = "open"
	eventUpcoming = "upcoming"
	eventClosed   = "closed"
)

const (
	eventsDefaultLimit = 20
	eventsMaxLimit     = 50
	// latestEventsShown is how many of the latest events list_events shows when none is open or upcoming, so that it
	// still answers when the last one was
	latestEventsShown = 3
)

type listEventsIn struct {
	Server string `json:"server"`
	// a pointer, as it is true by default: the SDK fills in the default of the schema, nil is the default to a
	// handler called otherwise
	OpenOnly *bool  `json:"open_only,omitempty" jsonschema:"true (default): the events open or upcoming on the server, with their stages; if there are none, the latest 3. false: every event of the server, latest first, without their stages."`
	Lang     string `json:"lang,omitempty"`
	Limit    int    `json:"limit,omitempty" jsonschema:"Most events to return, 1 to 50. Default 20."`
}

type eventList struct {
	Server    string       `json:"server"`
	Now       string       `json:"now" jsonschema:"The time of the statuses, RFC 3339 in UTC"`
	OpenOnly  bool         `json:"openOnly"`
	Truncated bool         `json:"truncated" jsonschema:"Whether limit left out older events"`
	Events    []eventEntry `json:"events"`
	Note      string       `json:"note,omitempty"`
	Source    source       `json:"source"`
}

type eventEntry struct {
	ZoneID    string  `json:"zoneId"`
	Name      string  `json:"name"`
	Status    string  `json:"status" jsonschema:"open, upcoming or closed on the server at now"`
	OpenTime  *string `json:"openTime" jsonschema:"When the event opens on the server, RFC 3339 in UTC"`
	CloseTime *string `json:"closeTime" jsonschema:"When the event closes on the server, RFC 3339 in UTC"`
	IsRerun   bool    `json:"isRerun" jsonschema:"Whether the event is the rerun (复刻) of an earlier one"`
	// its stages but the gachas
	StageCount int          `json:"stageCount"`
	Stages     []eventStage `json:"stages,omitempty" jsonschema:"The stages of the event, with open_only only"`
}

type eventStage struct {
	StageID string `json:"stageId"`
	Code    string `json:"code"`
}

func listEventsTool() *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:  "list_events",
		Title: "List events",
		Description: "Lists the events (limited-time activities) of a game server: status now (open, upcoming or " +
			"closed), open and close times, whether a rerun. By default the open and upcoming events with their " +
			"stage IDs (for get_stage_drops), or the latest 3 if there are none; open_only=false lists every event, " +
			"latest first, with stage counts only. The main story and the permanent side stories are not events.",
		InputSchema: inputSchema[listEventsIn](map[string]property{
			"server": serverProperty,
			"open_only": func(p *jsonschema.Schema) {
				// a boolean, not the null or boolean inferred from the pointer
				p.Type, p.Types = "boolean", nil
				p.Default = json.RawMessage("true")
			},
			"lang":  langProperty,
			"limit": intProperty(eventsDefaultLimit, 1, eventsMaxLimit),
		}),
	}
}

func (t *tools) listEvents(_ context.Context, _ *sdkmcp.CallToolRequest, in listEventsIn) (*sdkmcp.CallToolResult, eventList, error) {
	c, err := t.catalog()
	if err != nil {
		return nil, eventList{}, err
	}
	server, lang := serverOf(in.Server), langOf(in.Lang)
	limit := limitOf(in.Limit, eventsDefaultLimit, eventsMaxLimit)
	out := t.eventsResult(c, server, lang, in.OpenOnly == nil || *in.OpenOnly, limit, t.now())
	return textResult(eventLines(out)...), out, nil
}

// eventsResult is the result of list_events on server at now, the names in lang.
func (t *tools) eventsResult(c *service.SEOCatalog, server, lang string, openOnly bool, limit int, now time.Time) eventList {
	out := eventList{
		Server:   server,
		Now:      now.UTC().Format(time.RFC3339),
		OpenOnly: openOnly,
		Events:   []eventEntry{},
		// no page per event: the stage list, where the events are listed latest first
		Source: t.source(t.site + service.StatsPagePath(lang, server, service.StatsSectionStage)),
	}
	all := eventsOf(c, server, lang, now, openOnly)
	events := all
	if len(all) == 0 {
		out.Note = "No event on " + server + "."
	} else if openOnly {
		events = nil
		for _, e := range all {
			if e.Status != eventClosed {
				events = append(events, e)
			}
		}
		if len(events) == 0 {
			events = all[:min(latestEventsShown, len(all))]
			out.Note = "No event is open or upcoming on " + server + " now: these are the latest " +
				strconv.Itoa(len(events)) + ", closed."
			if server != "CN" {
				if open := events[0].OpenTime; open != nil {
					day, _, _ := strings.Cut(*open, "T")
					out.Note += " The newest event Penguin Statistics lists for " + server + " opened on " + day +
						": it may not list the newest events of the global servers yet, so one may be running in the game."
				} else {
					out.Note += " Penguin Statistics may not list the newest events of the global servers yet, so one may be " +
						"running in the game."
				}
			}
		}
	}
	if len(events) > limit {
		events, out.Truncated = events[:limit], true
	}
	out.Events = append(out.Events, events...)
	return out
}

// eventsOf returns the events of server at now, the names in lang, latest first like the stage list of the pages:
// the zones of the category ACTIVITY that exist on server (side stories that stay are ACTIVITY_PERMANENT). Their
// stages are those that exist on server but the gachas, listed if withStages.
func eventsOf(c *service.SEOCatalog, server, lang string, now time.Time, withStages bool) []eventEntry {
	stages := map[*service.SEOZone][]*service.SEOStage{}
	for _, s := range c.StageList {
		if s.Zone != nil && s.Zone.Category == constant.ZoneCategoryActivity && s.Existence.In(server) &&
			!s.IsGacha && !service.IsRecruitStage(s) {
			stages[s.Zone] = append(stages[s.Zone], s)
		}
	}
	var zones []*service.SEOZone
	for _, z := range c.Zones {
		if z.Category == constant.ZoneCategoryActivity && z.Existence.In(server) {
			zones = append(zones, z)
		}
	}
	sort.Slice(zones, func(i, j int) bool {
		a, b := zones[i], zones[j]
		if oa, ob := a.Existence[server].OpenTime.Int64, b.Existence[server].OpenTime.Int64; oa != ob {
			return oa > ob
		}
		if a.Index != b.Index {
			return a.Index < b.Index
		}
		return a.ArkID < b.ArkID
	})

	events := make([]eventEntry, 0, len(zones))
	for _, z := range zones {
		e := eventEntry{
			ZoneID:     z.ArkID,
			Name:       z.DisplayName(lang),
			Status:     eventStatus(z.Existence, server, now),
			OpenTime:   eventTime(z.Existence[server].OpenTime),
			CloseTime:  eventTime(z.Existence[server].CloseTime),
			IsRerun:    eventIsRerun(z, stages[z]),
			StageCount: len(stages[z]),
		}
		if withStages {
			e.Stages = make([]eventStage, 0, len(stages[z]))
			for _, s := range stages[z] {
				e.Stages = append(e.Stages, eventStage{StageID: s.ArkID, Code: s.DisplayCode(lang)})
			}
		}
		events = append(events, e)
	}
	return events
}

// eventStatus is the status on server at now of an event that exists there: open (see service.SEOExistence.OpenAt),
// upcoming if it opens later, else closed.
func eventStatus(e service.SEOExistence, server string, now time.Time) string {
	switch open := e[server].OpenTime; {
	case e.OpenAt(server, now):
		return eventOpen
	case open.Valid && open.Int64 > now.UnixMilli():
		return eventUpcoming
	default:
		return eventClosed
	}
}

// eventTime is an open or close time of an event, RFC 3339 in UTC; nil (null) if none: unset, 0, or the close time
// of what never closes.
func eventTime(ms null.Int) *string {
	if !ms.Valid || ms.Int64 == 0 || ms.Int64 == constant.FakeEndTimeMilli {
		return nil
	}
	s := utcTime(ms.Int64)
	return &s
}

// eventIsRerun tells whether the event of zone is a rerun: its zone ID says so (act11sre_zone1) or its stages do
// (act13d2_01_rep), as the first reruns have zone IDs like those of other events (act13d2_zone1, 骑兵与猎人・复刻).
func eventIsRerun(zone *service.SEOZone, stages []*service.SEOStage) bool {
	if strings.Contains(zone.ArkID, "_rep") || strings.Contains(zone.ArkID, "sre") {
		return true
	}
	for _, s := range stages {
		if variantOf(s) == variantRerun {
			return true
		}
	}
	return false
}

// eventLines are the lines of the text content of out, a result of list_events.
func eventLines(out eventList) []string {
	heading := "Events on " + out.Server + " at " + out.Now + ", latest first (times in UTC):"
	if out.OpenOnly && out.Note == "" {
		heading = "Events open or upcoming on " + out.Server + " at " + out.Now + ", latest first (times in UTC):"
	}
	lines := []string{heading}
	if out.Note != "" {
		lines = append(lines, out.Note)
	}
	if len(out.Events) > 0 {
		rows := make([][]string, 0, len(out.Events))
		for _, e := range out.Events {
			rows = append(rows, []string{e.Name, e.ZoneID, e.Status, eventTimeText(e.OpenTime), eventTimeText(e.CloseTime), yesNo(e.IsRerun),
				strconv.Itoa(e.StageCount)})
		}
		lines = append(lines, "", table([]string{"event", "zoneId", "status", "opens", "closes", "rerun", "stages"}, rows))
	}
	// below the table after a blank line, not to be read as rows of it
	var below []string
	for _, e := range out.Events {
		if len(e.Stages) == 0 {
			continue
		}
		stages := make([]string, len(e.Stages))
		for i, s := range e.Stages {
			stages[i] = s.Code + " (" + s.StageID + ")"
		}
		below = append(below, "Stages of "+e.ZoneID+": "+strings.Join(stages, ", "))
	}
	if out.Truncated {
		more := "raise limit (at most " + strconv.Itoa(eventsMaxLimit) + ") for older ones"
		if len(out.Events) >= eventsMaxLimit {
			more = "older ones are left out"
		}
		below = append(below, "Showing the latest "+strconv.Itoa(len(out.Events))+" events: "+more+".")
	}
	lines = append(lines, "")
	if len(below) > 0 {
		lines = append(append(lines, below...), "")
	}
	return append(lines, out.Source.line())
}

// eventTimeText is t in the text content, "" (a "-" cell) for none.
func eventTimeText(t *string) string {
	if t == nil {
		return ""
	}
	return *t
}
