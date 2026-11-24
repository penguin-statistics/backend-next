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

// GlobalMatrixIncrementalMode is the mode of ConfigSpec.GlobalMatrixIncremental: off, verify or on.
type GlobalMatrixIncrementalMode string

const (
	GlobalMatrixIncrementalOff    GlobalMatrixIncrementalMode = "off"
	GlobalMatrixIncrementalVerify GlobalMatrixIncrementalMode = "verify"
	GlobalMatrixIncrementalOn     GlobalMatrixIncrementalMode = "on"
)

// Decode accepts off, verify and on in any case; empty is off.
func (m *GlobalMatrixIncrementalMode) Decode(value string) error {
	switch mode := GlobalMatrixIncrementalMode(strings.ToLower(strings.TrimSpace(value))); mode {
	case "":
		*m = GlobalMatrixIncrementalOff
		return nil
	case GlobalMatrixIncrementalOff, GlobalMatrixIncrementalVerify, GlobalMatrixIncrementalOn:
		*m = mode
		return nil
	default:
		return fmt.Errorf("invalid global matrix incremental mode %q: expect off, verify or on", value)
	}
}

// Incremental reports whether the global matrices are calculated from bases (verify or on). The zero value, as in
// configurations built in code, is off.
func (m GlobalMatrixIncrementalMode) Incremental() bool {
	return m == GlobalMatrixIncrementalVerify || m == GlobalMatrixIncrementalOn
}
