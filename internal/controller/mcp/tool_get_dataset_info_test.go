package mcp

import (
	"encoding/json"
	"strings"
	"testing"

	"exusiai.dev/backend-next/internal/model"
	"exusiai.dev/backend-next/internal/service"
)

func TestGetDatasetInfo(t *testing.T) {
	// CN is calculated, the other servers are not yet
	_, app := testServer(&fakeData{matrices: testMatrices()})
	res := callTool(t, app, "get_dataset_info", `{}`)
	if res.IsError {
		t.Fatalf("error: %s", res.text(t))
	}
	var out datasetInfo
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil {
		t.Fatal(err)
	}
	if out.Name != "Penguin Statistics" || out.Website != site || out.Docs != "https://developer.penguin-stats.io" ||
		out.OpenAPI != "https://penguin-stats.io/swagger/doc.json" || out.License != "CC BY-NC 4.0" ||
		out.LicenseURL != "https://creativecommons.org/licenses/by-nc/4.0/" || out.Source.URL != site {
		t.Errorf("info: %+v", out)
	}
	want := []serverData{
		{Server: "CN", Timezone: "UTC+8", MatrixAvailable: true, DataAsOf: "2026-10-22T16:32:01Z"},
		{Server: "US", Timezone: "UTC-7"},
		{Server: "JP", Timezone: "UTC+9"},
		{Server: "KR", Timezone: "UTC+9"},
	}
	if len(out.Servers) != len(want) {
		t.Fatalf("servers: %+v", out.Servers)
	}
	for i := range want {
		if out.Servers[i] != want[i] {
			t.Errorf("server %d = %+v, want %+v", i, out.Servers[i], want[i])
		}
	}
	if out.Definitions != dropDefinitions || len(out.Caveats) == 0 {
		t.Errorf("definitions %+v, caveats %q", out.Definitions, out.Caveats)
	}

	// the text alone is enough to answer: the servers, their freshness, the definitions and the credit
	text := res.text(t)
	for _, s := range []string{
		"| CN | UTC+8 | loaded | 2026-10-22T16:32:01Z | - |",
		"| US | UTC-7 | loading (retry in about 2 minutes) | - | - |",
		"- dropRate: quantity ÷ times",
		"- sanityPerItem: ",
		"Crowdsourced",
		"https://penguin-stats.io/swagger/doc.json",
		"Data: Penguin Statistics (https://penguin-stats.io), CC BY-NC 4.0 (https://creativecommons.org/licenses/by-nc/4.0/). Source: https://penguin-stats.io",
	} {
		if !strings.Contains(text, s) {
			t.Errorf("text lacks %q:\n%s", s, text)
		}
	}
	// not the structured content a second time
	if strings.Contains(text, `"servers"`) {
		t.Errorf("text holds the JSON:\n%s", text)
	}

	// far below the budget of a result (about 25k tokens)
	if n := len(res.StructuredContent) + len(text); n > 8<<10 {
		t.Errorf("result of %d bytes", n)
	}
}

// The info answers whatever is loaded: it tells what is not.
func TestGetDatasetInfoWhileCold(t *testing.T) {
	_, app := testServer(&fakeData{})
	res := callTool(t, app, "get_dataset_info", `{}`)
	var out datasetInfo
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil {
		t.Fatal(err)
	}
	if res.IsError || len(out.Servers) != 4 {
		t.Fatalf("result: %s", res.StructuredContent)
	}
	for _, s := range out.Servers {
		if s.MatrixAvailable || s.DataAsOf != "" {
			t.Errorf("server %+v", s)
		}
	}
	if strings.Contains(string(res.StructuredContent), "dataAsOf") {
		t.Errorf("dataAsOf without a matrix: %s", res.StructuredContent)
	}
}

// The catalog may lag the game on the global servers: the info tells the newest event it lists on each server.
func TestGetDatasetInfoLatestEvent(t *testing.T) {
	_, app := testServer(&fakeData{catalog: service.NewSEOCatalog(nil, nil, []*model.Zone{
		{ArkZoneID: "act52side_zone1", ZoneID: 1, Category: "ACTIVITY", Existence: []byte(`{"CN":{"exist":true,"openTime":1791518400000}}`)},
		{ArkZoneID: "act45side_zone1", ZoneID: 2, Category: "ACTIVITY",
			Existence: []byte(`{"CN":{"exist":true,"openTime":1770706800000},"US":{"exist":true,"openTime":1770735600000}}`)},
		// not an event
		{ArkZoneID: "main_17", ZoneID: 3, Category: "MAINLINE", Existence: []byte(`{"US":{"exist":true,"openTime":1792483200000}}`)},
	})})
	res := callTool(t, app, "get_dataset_info", `{}`)
	var out datasetInfo
	if err := json.Unmarshal(res.StructuredContent, &out); err != nil || res.IsError || len(out.Servers) != 4 {
		t.Fatalf("result %s %v", res.StructuredContent, err)
	}
	for i, want := range []string{"2026-10-09T04:00:00Z", "2026-02-10T15:00:00Z", "", ""} {
		if got := out.Servers[i].LatestEventOpenTime; got != want {
			t.Errorf("%s: latestEventOpenTime %q, want %q", out.Servers[i].Server, got, want)
		}
	}
	if text := res.text(t); !strings.Contains(text, "| CN | UTC+8 | loading (retry in about 2 minutes) | - | 2026-10-09T04:00:00Z |") {
		t.Errorf("text:\n%s", text)
	}
}
