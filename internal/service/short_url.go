package service

import (
	"net/url"

	"github.com/gofiber/fiber/v2"

	"github.com/penguin-statistics/backend-next/internal/constants"
	"github.com/penguin-statistics/backend-next/internal/utils"
)

type ShortURLService struct {
	ItemService  *ItemService
	StageService *StageService
	ZoneService  *ZoneService

	GeoIPService *GeoIPService
}

func NewShortURLService(itemService *ItemService, stageService *StageService, zoneService *ZoneService, geoIPService *GeoIPService) *ShortURLService {
	return &ShortURLService{
		ItemService:  itemService,
		StageService: stageService,
		ZoneService:  zoneService,
		GeoIPService: geoIPService,
	}
}

// siteURL returns the site URL with path appended from toPath.
// toPath is expected to always start with a slash.
func (s *ShortURLService) siteURL(ctx *fiber.Ctx, toPath string) string {
	ip := utils.ExtractIP(ctx)
	var host string
	if s.GeoIPService.InChinaMainland(ip) {
		host = constants.SiteChinaMainlandMirrorHost
	} else {
		host = constants.SiteGlobalMirrorHost
	}

	return "https://" + host + toPath
}

func (s *ShortURLService) ResolveShortURL(ctx *fiber.Ctx, path string) string {
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
	if resolved, err := s.resolveByItemName(ctx, path); err == nil {
		return resolved
	}
	if resolved, err := s.resolveByStageCode(ctx, path); err == nil {
		return resolved
	}
	if resolved, err := s.resolveByItemId(ctx, path); err == nil {
		return resolved
	}
	if resolved, err := s.resolveByStageId(ctx, path); err == nil {
		return resolved
	}

	return s.resolveUnknown(ctx, path)
}

func (s *ShortURLService) resolveByItemName(ctx *fiber.Ctx, path string) (string, error) {
	item, err := s.ItemService.SearchItemByName(ctx, path)
	if err != nil {
		return "", err
	}

	return s.siteURL(ctx, "/result/item/"+item.ArkItemID+"?utm_source=exusiai&utm_medium=item&utm_campaign=name"), nil
}

func (s *ShortURLService) resolveByStageCode(ctx *fiber.Ctx, path string) (string, error) {
	stage, err := s.StageService.SearchStageByCode(ctx, path)
	if err != nil {
		return "", err
	}

	zone, err := s.ZoneService.GetZoneById(ctx, stage.ZoneID)
	if err != nil {
		return "", err
	}

	return s.siteURL(ctx, "/result/stage/"+zone.ArkZoneID+"/"+stage.ArkStageID+"?utm_source=exusiai&utm_medium=stage&utm_campaign=code"), nil
}

func (s *ShortURLService) resolveByStageId(ctx *fiber.Ctx, path string) (string, error) {
	stage, err := s.StageService.GetStageByArkId(ctx, path)
	if err != nil {
		return "", err
	}

	zone, err := s.ZoneService.GetZoneById(ctx, stage.ZoneID)
	if err != nil {
		return "", err
	}

	return s.siteURL(ctx, "/result/stage/"+zone.ArkZoneID+"/"+stage.ArkStageID+"?utm_source=exusiai&utm_medium=stage&utm_campaign=id"), nil
}

func (s *ShortURLService) resolveByItemId(ctx *fiber.Ctx, path string) (string, error) {
	item, err := s.ItemService.GetItemByArkId(ctx, path)
	if err != nil {
		return "", err
	}

	return s.siteURL(ctx, "/result/item/"+item.ArkItemID+"?utm_source=exusiai&utm_medium=item&utm_campaign=id"), nil
}

func (s *ShortURLService) resolveUnknown(ctx *fiber.Ctx, path string) string {
	return s.siteURL(ctx, "/search?utm_source=exusiai&utm_medium=search&utm_campaign=fallback&q="+url.PathEscape(path))
}
