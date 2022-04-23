package constant

const (
	FurnitureArkItemID = "furni"

	ExtraProcessTypeGachaBox = "GACHABOX"

	DropTypeRegular         = "REGULAR"
	DropTypeSpecial         = "SPECIAL"
	DropTypeExtra           = "EXTRA"
	DropTypeRecognitionOnly = "RECOGNITION_ONLY"
	DropTypeFurniture       = "FURNITURE"
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
