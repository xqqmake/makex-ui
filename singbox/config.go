package singbox

import (
	"bytes"
	"encoding/json"

	"x-ui/util/json_util"
)

// Config is the sing-box core configuration. Inbounds are built as raw
// maps by the service layer (web/service/singbox.go) because sing-box
// inbound schemas differ per protocol (anytls/tuic/naive).
type Config struct {
	Log          json_util.RawMessage `json:"log"`
	Inbounds     []map[string]any     `json:"inbounds"`
	Outbounds    json_util.RawMessage `json:"outbounds"`
	Route        json_util.RawMessage `json:"route,omitempty"`
	Experimental json_util.RawMessage `json:"experimental,omitempty"`
}

// Equals reports whether two configs produce the same singbox.json.
// Used by the 30s restart job to avoid needless process restarts.
func (c *Config) Equals(other *Config) bool {
	if c == nil || other == nil {
		return c == other
	}
	a, err := json.Marshal(c)
	if err != nil {
		return false
	}
	b, err := json.Marshal(other)
	if err != nil {
		return false
	}
	return bytes.Equal(a, b)
}
