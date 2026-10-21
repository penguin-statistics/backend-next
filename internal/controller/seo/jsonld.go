package seo

import (
	"time"

	"exusiai.dev/backend-next/internal/service"
)

// The structured data of the pages (https://schema.org), marshaled by html/template into a JSON-LD script.

type ldGraph struct {
	Context string `json:"@context"`
	Graph   []any  `json:"@graph"`
}

type ldOrganization struct {
	Type string `json:"@type"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type ldDataCatalog struct {
	Type string `json:"@type"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type ldPropertyValue struct {
	Type        string `json:"@type"`
	Name        string `json:"name"`
	UnitText    string `json:"unitText,omitempty"`
	Description string `json:"description"`
}

type ldDataDownload struct {
	Type           string `json:"@type"`
	EncodingFormat string `json:"encodingFormat"`
	ContentURL     string `json:"contentUrl"`
}

type ldDataset struct {
	Type                  string            `json:"@type"`
	ID                    string            `json:"@id"`
	Name                  string            `json:"name"`
	Description           string            `json:"description"`
	URL                   string            `json:"url"`
	InLanguage            string            `json:"inLanguage"`
	License               string            `json:"license"`
	IsAccessibleForFree   bool              `json:"isAccessibleForFree"`
	Creator               ldOrganization    `json:"creator"`
	Publisher             ldOrganization    `json:"publisher"`
	IncludedInDataCatalog ldDataCatalog     `json:"includedInDataCatalog"`
	DateModified          string            `json:"dateModified,omitempty"`
	TemporalCoverage      string            `json:"temporalCoverage,omitempty"`
	MeasurementTechnique  string            `json:"measurementTechnique"`
	VariableMeasured      []ldPropertyValue `json:"variableMeasured"`
	Distribution          []ldDataDownload  `json:"distribution"`
}

type ldBreadcrumbList struct {
	Type            string       `json:"@type"`
	ItemListElement []ldListItem `json:"itemListElement"`
}

type ldListItem struct {
	Type     string `json:"@type"`
	Position int    `json:"position"`
	Name     string `json:"name"`
	Item     string `json:"item,omitempty"`
}

func (b *builder) organization() ldOrganization {
	// the name the site is known by in every language
	return ldOrganization{Type: "Organization", Name: "Penguin Statistics", URL: b.site + "/"}
}

func breadcrumbList(crumbs []Link) ldBreadcrumbList {
	l := ldBreadcrumbList{Type: "BreadcrumbList"}
	for i, c := range crumbs {
		l.ItemListElement = append(l.ItemListElement, ldListItem{Type: "ListItem", Position: i + 1, Name: c.Name, Item: c.Href})
	}
	return l
}

func (b *builder) breadcrumbLD(crumbs []Link) *ldGraph {
	return &ldGraph{Context: "https://schema.org", Graph: []any{breadcrumbList(crumbs)}}
}

// jsonLD is the structured data of a page showing the rows of an item or a stage: a Dataset of them, and the
// breadcrumbs
func (b *builder) jsonLD(p *Page, name, filter, id string, rows []service.SEORow, crumbs []Link) *ldGraph {
	org := b.organization()
	ds := ldDataset{
		Type:                  "Dataset",
		ID:                    p.Head.Canonical + "#dataset",
		Name:                  name,
		Description:           p.Head.Description + " " + b.t.Definitions,
		URL:                   p.Head.Canonical,
		InLanguage:            b.t.Lang,
		License:               licenseURL,
		IsAccessibleForFree:   true,
		Creator:               org,
		Publisher:             org,
		IncludedInDataCatalog: ldDataCatalog{Type: "DataCatalog", Name: org.Name, URL: org.URL},
		TemporalCoverage:      temporalCoverage(rows),
		MeasurementTechnique:  b.t.MeasurementTechnique,
		VariableMeasured: []ldPropertyValue{
			{Type: "PropertyValue", Name: b.t.HTimes, Description: b.t.VarTimes},
			{Type: "PropertyValue", Name: b.t.HQuantity, Description: b.t.VarQuantity},
			{Type: "PropertyValue", Name: b.t.HPercentage, UnitText: "%", Description: b.t.VarRate},
		},
		Distribution: []ldDataDownload{{Type: "DataDownload", EncodingFormat: "application/json", ContentURL: b.dataURL(filter, id)}},
	}
	if b.matrix != nil && !b.matrix.LastModified.IsZero() {
		ds.DateModified = b.matrix.LastModified.UTC().Format(time.RFC3339)
	}
	return &ldGraph{Context: "https://schema.org", Graph: []any{ds, breadcrumbList(crumbs)}}
}

// temporalCoverage is the ISO 8601 interval of the time ranges of rows, open ("..") if any of them is
func temporalCoverage(rows []service.SEORow) string {
	if len(rows) == 0 {
		return ""
	}
	start, end, open := rows[0].StartTime, int64(0), false
	for _, row := range rows {
		if row.StartTime < start {
			start = row.StartTime
		}
		if !row.EndTime.Valid {
			open = true
		} else if row.EndTime.Int64 > end {
			end = row.EndTime.Int64
		}
	}
	s := time.UnixMilli(start).UTC().Format(time.RFC3339) + "/"
	if open {
		return s + ".."
	}
	return s + time.UnixMilli(end).UTC().Format(time.RFC3339)
}
