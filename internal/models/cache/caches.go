package cache

import "github.com/penguin-statistics/backend-next/internal/utils/pcache"

var ItemFromId = pcache.New()
var ItemFromArkId = pcache.New()

var StageFromId = pcache.New()
var StageFromArkId = pcache.New()

var ZoneFromId = pcache.New()
var ZoneFromArkId = pcache.New()
