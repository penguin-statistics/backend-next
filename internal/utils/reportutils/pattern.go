package reportutils

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cespare/xxhash/v2"

	"github.com/penguin-statistics/backend-next/internal/models/types"
)

func CalculateDropPatternHash(drops []types.Drop) string {
	segments := make([]string, 0, len(drops))

	for _, drop := range drops {
		segments = append(segments, fmt.Sprintf("%s:%d", drop.DropType, drop.Quantity))
	}

	sort.Strings(segments)

	hash := xxhash.Sum64String(strings.Join(segments, "|"))
	return strconv.FormatUint(hash, 16)
}
