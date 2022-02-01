package reportutils

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/cespare/xxhash/v2"

	"github.com/penguin-statistics/backend-next/internal/models/types"
)

func CalculateDropPatternHash(drops []types.Drop) string {
	var sb strings.Builder

	for _, drop := range drops {
		sb.WriteString(fmt.Sprintf("%s:%d", drop.DropType, drop.Quantity))
		// add separator | if not last one
		if drop != drops[len(drops)-1] {
			sb.WriteString("|")
		}
	}

	hash := xxhash.Sum64String(sb.String())
	return strconv.FormatUint(hash, 16)
}
