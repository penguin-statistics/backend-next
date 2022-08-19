package jetstream

import (
	"strconv"

	"github.com/nats-io/nats.go"
)

func MessageID(pair nats.SequencePair) string {
	return "seq:" + strconv.FormatUint(pair.Consumer, 10)
}
