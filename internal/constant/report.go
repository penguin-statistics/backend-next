package constant

import "time"

const (
	FurnitureArkItemID = "furni"

	ExtraProcessTypeGachaBox = "GACHABOX"

	ReportRedisPrefix = "report:"

	DropTypeRegular         = "REGULAR"
	DropTypeSpecial         = "SPECIAL"
	DropTypeExtra           = "EXTRA"
	DropTypeRecognitionOnly = "RECOGNITION_ONLY"
	DropTypeFurniture       = "FURNITURE"

	ViolationReliabilityUser                 = 1 << 2
	ViolationReliabilityMD5                  = 1<<2 + 1
	ViolationReliabilityDrop                 = 1<<2 + 2
	ViolationReliabilityRejectRuleUnexpected = 1<<2 + 3

	ViolationReliabilityRejectRuleRangeLeast = 1 << 8
	ViolationReliabilityRejectRuleRangeMost  = 1 << 10

	ReportIdempotencyLifetime     = time.Hour * 24
	ReportIdempotencyRedisHashKey = "report-idempotency"

	RecruitStageID  = "recruit"
	RecruitItemType = "RECRUIT_TAG"
)

// DropTypeMap maps an API drop type to a database drop type.
// The map must not be modified.
var DropTypeMap = map[string]string{
	"REGULAR_DROP": "REGULAR",
	"NORMAL_DROP":  "REGULAR",
	"SPECIAL_DROP": "SPECIAL",
	"EXTRA_DROP":   "EXTRA",
	"FURNITURE":    "FURNITURE",
}

var DropTypeReversedMap = map[string]string{
	"REGULAR":   "NORMAL_DROP",
	"SPECIAL":   "SPECIAL_DROP",
	"EXTRA":     "EXTRA_DROP",
	"FURNITURE": "FURNITURE",
}

var DropTypeMapKeys = []string{
	"NORMAL_DROP",
	"SPECIAL_DROP",
	"EXTRA_DROP",
}
