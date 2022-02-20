package reportutils

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cespare/xxhash/v2"

	"github.com/penguin-statistics/backend-next/internal/models/types"
)

func CalculateDropPatternHash(drops []*types.Drop) string {
	segments := make([]string, len(drops))

	for i, drop := range drops {
		segments[i] = fmt.Sprintf("%d:%d", drop.ItemID, drop.Quantity)
	}

	sort.Strings(segments)

	hash := xxhash.Sum64String(strings.Join(segments, "|"))
	return strconv.FormatUint(hash, 16)
}
