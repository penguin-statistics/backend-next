package service

import (
	"context"
	"net/url"

	"github.com/gofiber/fiber/v2"

	"exusiai.dev/backend-next/internal/util"
	"exusiai.dev/gommon/constant"
)

type ShortURL struct {
	ItemService  *Item
	StageService *Stage
	ZoneService  *Zone
	GeoIPService *GeoIP
}

func NewShortURL(itemService *Item, stageService *Stage, zoneService *Zone, geoIPService *GeoIP) *ShortURL {
	return &ShortURL{
		ItemService:  itemService,
		StageService: stageService,
		ZoneService:  zoneService,
		GeoIPService: geoIPService,
	}
}

// siteURL returns the site URL with path appended from toPath.
// toPath is expected to always start with a slash.
func (s *ShortURL) siteURL(ctx *fiber.Ctx, toPath string) string {
	ip := util.ExtractIP(ctx)
	var host string
	if s.GeoIPService.InChinaMainland(ip) {
		host = constant.SiteChinaMainlandMirrorHost
	} else {
		host = constant.SiteGlobalMirrorHost
	}

	return "https://" + host + toPath
}

func (s *ShortURL) Resolve(ctx *fiber.Ctx, path string) string {
	defaultPath := "/?utm_source=exusiai&utm_medium=root&utm_campaign=root"
	if path == "" || len(path) > 128 {
		return s.siteURL(ctx, defaultPath)
	}

	escapedPath, err := url.PathUnescape(path)
	if err != nil {
		return s.siteURL(ctx, defaultPath)
	}
	path = escapedPath

	// Simple Keyword Matching
	if path == "item" {
		return s.siteURL(ctx, "/result/item")
	}
	if path == "stage" {
		return s.siteURL(ctx, "/result/stage")
	}
	if path == "planner" {
		return s.siteURL(ctx, "/planner")
	}

	// Item Name Matching
	if resolved, err := s.resolveByItemName(ctx.UserContext(), path); err == nil {
		return s.siteURL(ctx, resolved)
	}
	if resolved, err := s.resolveByStageCode(ctx.UserContext(), path); err == nil {
		return s.siteURL(ctx, resolved)
	}
	if resolved, err := s.resolveByItemId(ctx.UserContext(), path); err == nil {
		return s.siteURL(ctx, resolved)
	}
	if resolved, err := s.resolveByStageId(ctx.UserContext(), path); err == nil {
		return s.siteURL(ctx, resolved)
	}

	resolved := s.resolveUnknown(path)
	return s.siteURL(ctx, resolved)
}

func (s *ShortURL) resolveByItemName(ctx context.Context, path string) (string, error) {
	item, err := s.ItemService.SearchItemByName(ctx, path)
	if err != nil {
		return "", err
	}

	return "/result/item/" + item.ArkItemID + "?utm_source=exusiai&utm_medium=item&utm_campaign=name", nil
}

func (s *ShortURL) resolveByStageCode(ctx context.Context, path string) (string, error) {
	stage, err := s.StageService.SearchStageByCode(ctx, path)
	if err != nil {
		return "", err
	}

	zone, err := s.ZoneService.GetZoneById(ctx, stage.ZoneID)
	if err != nil {
		return "", err
	}

	return "/result/stage/" + zone.ArkZoneID + "/" + stage.ArkStageID + "?utm_source=exusiai&utm_medium=stage&utm_campaign=code", nil
}

func (s *ShortURL) resolveByStageId(ctx context.Context, path string) (string, error) {
	stage, err := s.StageService.GetStageByArkId(ctx, path)
	if err != nil {
		return "", err
	}

	zone, err := s.ZoneService.GetZoneById(ctx, stage.ZoneID)
	if err != nil {
		return "", err
	}

	return "/result/stage/" + zone.ArkZoneID + "/" + stage.ArkStageID + "?utm_source=exusiai&utm_medium=stage&utm_campaign=id", nil
}

func (s *ShortURL) resolveByItemId(ctx context.Context, path string) (string, error) {
	item, err := s.ItemService.GetItemByArkId(ctx, path)
	if err != nil {
		return "", err
	}

	return "/result/item/" + item.ArkItemID + "?utm_source=exusiai&utm_medium=item&utm_campaign=id", nil
}

func (s *ShortURL) resolveUnknown(path string) string {
	return "/search?utm_source=exusiai&utm_medium=search&utm_campaign=fallback&q=" + url.QueryEscape(path)
}
