package service

import (
	"net/url"
	"slices"
	"strings"

	"exusiai.dev/gommon/constant"
)

// The rules of the stats of the frontend shared by its server-rendered pages and the MCP server, both made of the
// catalog and matrices of SEOData.

// StatsLangs are the languages of the stats routes of the frontend (src/router.js statsLanguages)
var StatsLangs = []string{"zh", "en", "ja", "ko"}

// StatsDefaultLang is the fallback locale of the frontend: the x-default of the hreflang links of the pages
const StatsDefaultLang = "en"

// The license of the drop data (meta.footer.copyright of the frontend)
const (
	DropDataLicenseURL  = "https://creativecommons.org/licenses/by-nc/4.0/"
	DropDataLicenseName = "CC BY-NC 4.0"
)

// ListedItemTypes are the types of the items the frontend lists (src/utils/getters.js), in the order its item list
// groups them
var ListedItemTypes = []string{"MATERIAL", "CARD_EXP", "CHIP", "FURN", "ACTIVITY_ITEM"}

// IsListedItemType tells whether the frontend lists the items of type t
func IsListedItemType(t string) bool {
	return slices.Contains(ListedItemTypes, t)
}

// IsRecruitStage tells whether the stage is the recruitment statistics, which the frontend shows in beta with
// measures of their own
func IsRecruitStage(stage *SEOStage) bool {
	return stage.ArkID == constant.RecruitStageID || (stage.Zone != nil && stage.Zone.Category == "RECRUIT")
}

// ItemOnServer tells whether the item has a stats page on server: an item the frontend lists that exists there or
// has drops there. matrix is the matrix of server, nil while it is not calculated.
func ItemOnServer(item *SEOItem, server string, matrix *SEOMatrix) bool {
	if item == nil || !IsListedItemType(item.Type) {
		return false
	}
	return item.Existence.In(server) || (matrix != nil && len(matrix.ByItem[item.ArkID]) > 0)
}

// StageOnServer tells whether the stage has a stats page on server: a stage in a zone that exists there or has
// drops there. matrix is the matrix of server, nil while it is not calculated.
func StageOnServer(stage *SEOStage, server string, matrix *SEOMatrix) bool {
	if stage == nil || stage.Zone == nil {
		return false
	}
	return stage.Existence.In(server) || (matrix != nil && len(matrix.ByStage[stage.ArkID]) > 0)
}

// DisplayName is the name of the item in lang (see SEOI18n.In), or its ID if it has none.
func (i *SEOItem) DisplayName(lang string) string {
	if n := i.Name.In(lang); n != "" {
		return n
	}
	return i.ArkID
}

// DisplayName is the name of the zone in lang (see SEOI18n.In), or its ID if it has none; "" for no zone.
func (z *SEOZone) DisplayName(lang string) string {
	if z == nil {
		return ""
	}
	if n := z.Name.In(lang); n != "" {
		return n
	}
	return z.ArkID
}

// DisplayCode is the code of the stage in lang (see SEOI18n.In), or its ID if it has none.
func (s *SEOStage) DisplayCode(lang string) string {
	if c := s.Code.In(lang); c != "" {
		return c
	}
	return s.ArkID
}

// DisplayName is the code of the stage and the name of its zone in lang: stages have no name of their own. A stage
// named like its zone (the recruitment statistics) is named once.
func (s *SEOStage) DisplayName(lang string) string {
	code, zone := s.DisplayCode(lang), s.Zone.DisplayName(lang)
	if zone == "" || zone == code {
		return code
	}
	return code + " " + zone
}

// The sections of the stats routes: the list of the items or stages, and the pages under it
const (
	StatsSectionItem  = "item"
	StatsSectionStage = "stage"
)

// StatsPagePath is the path of a stats page of the frontend: /{lang}/result/{server}, then the section, if any, and
// the IDs, escaped.
func StatsPagePath(lang, server, section string, ids ...string) string {
	p := "/" + lang + "/result/" + strings.ToLower(server)
	if section != "" {
		p += "/" + section
	}
	for _, id := range ids {
		p += "/" + url.PathEscape(id)
	}
	return p
}

// ItemPageURL is the URL of the stats page of an item on server in lang, at site (an origin). ok is false if there
// is no such page to link: a language or server without stats routes, or an ID that is routed as another path (see
// routablePage).
func ItemPageURL(site, lang, server, itemID string) (u string, ok bool) {
	if !routablePage(lang, server, itemID) {
		return "", false
	}
	return strings.TrimRight(site, "/") + StatsPagePath(lang, server, StatsSectionItem, itemID), true
}

// StagePageURL is the URL of the stats page of a stage on server in lang, at site (an origin), whose path holds the
// zone of the stage. ok is false if there is no such page to link: a stage without a zone, as ItemPageURL otherwise.
func StagePageURL(site, lang, server string, stage *SEOStage) (u string, ok bool) {
	if stage == nil || stage.Zone == nil || !routablePage(lang, server, stage.Zone.ArkID, stage.ArkID) {
		return "", false
	}
	return strings.TrimRight(site, "/") + StatsPagePath(lang, server, StatsSectionStage, stage.Zone.ArkID, stage.ArkID), true
}

// routablePage tells whether the page of the IDs in lang on server is served as that page: lang is one of the
// languages of the stats routes, mounted in lower case only, and server one of the servers, in any case; an ID ""
// would be parsed as another path and redirected, "." or ".." resolved to another path before routing, and a last ID
// ending in .md (but .md itself) served as the Markdown view of another page (the sitemaps check the same with the
// routes of the pages)
func routablePage(lang, server string, ids ...string) bool {
	if _, ok := constant.ServerMap[strings.ToUpper(server)]; !ok || !slices.Contains(StatsLangs, lang) {
		return false
	}
	for _, id := range ids {
		if id == "" || id == "." || id == ".." {
			return false
		}
	}
	if last := ids[len(ids)-1]; len(last) > len(".md") && strings.HasSuffix(last, ".md") {
		return false
	}
	return true
}

// SanityCost is the sanity a run of the stage costs. ok is false if it has none to compare drops by: none known (or
// the placeholder 99 the v2 API fills in for none), 0, or a gacha or the recruitment, whose rows count draws and
// recruitments rather than runs.
func (s *SEOStage) SanityCost() (sanity int64, ok bool) {
	if s == nil || !s.Sanity.Valid || s.Sanity.Int64 <= 0 || s.Sanity.Int64 == constant.DefaultNullSanity ||
		s.IsGacha || IsRecruitStage(s) {
		return 0, false
	}
	return s.Sanity.Int64, true
}

// SanityPerItem is the sanity required per item of a row of the stage with times and quantity: sanity × times ÷
// quantity, from the counts rather than the rounded rate the pages divide by (stats.headers.apPPR). ok is false if
// the stage has no sanity cost (see SanityCost) or the row no drops.
func (s *SEOStage) SanityPerItem(times, quantity int) (perItem float64, ok bool) {
	sanity, ok := s.SanityCost()
	if !ok || times <= 0 || quantity <= 0 {
		return 0, false
	}
	return float64(sanity) * float64(times) / float64(quantity), true
}
