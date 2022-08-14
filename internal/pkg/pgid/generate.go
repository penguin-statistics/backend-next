package pgid

import (
	"fmt"
	"math/rand"
	"time"
)

// Before v3.3.7, PenguinIDs are generated as 8 digits number string.
// After v3.3.7 (approximately released at 2022-06-05 01:00), newly generated PenguinIDs will be a 9 digits number string.
// PenguinID can start with 0, with generated number padded to the corresponding length with 0.
func New() string {
	rand.Seed(time.Now().UnixNano())
	return fmt.Sprintf("%09d", rand.Intn(1e9))
}
