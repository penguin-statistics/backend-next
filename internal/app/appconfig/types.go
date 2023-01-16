package appconfig

import (
	"encoding/base64"
	"fmt"
	"strings"
)

type WorkerHeartbeatURLMap map[string]string

func (m *WorkerHeartbeatURLMap) Decode(value string) error {
	*m = WorkerHeartbeatURLMap{}
	for _, pair := range strings.Split(value, ",") {
		kv := strings.Split(pair, ":")
		if len(kv) != 2 {
			return fmt.Errorf("invalid heartbeat URL map: expect a `:` separated key pair for each element, but got: %s", value)
		}
		val, err := base64.StdEncoding.DecodeString(strings.TrimSpace(kv[1]))
		if err != nil {
			return fmt.Errorf("invalid value in worker heartbeat URL map: base64 decoding failed: %s (%w)", val, err)
		}
		(*m)[kv[0]] = string(val)
	}
	return nil
}
