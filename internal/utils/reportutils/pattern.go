package reportutils

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/zeebo/xxh3"

	"github.com/penguin-statistics/backend-next/internal/models/types"
)

func CalculateDropPatternHash(drops []*types.Drop) string {
	segments := make([]string, len(drops))

	for i, drop := range drops {
		segments[i] = fmt.Sprintf("%d:%d", drop.ItemID, drop.Quantity)
	}

	sort.Strings(segments)

	hash := xxh3.HashStringSeed(strings.Join(segments, "|"), 0)
	return strconv.FormatUint(hash, 16)
}
