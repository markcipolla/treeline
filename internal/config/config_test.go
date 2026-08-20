package config

import (
	"encoding/json"
	"testing"
)

// TestRepoConfigUnmarshal accepts both the legacy plain-path string and the
// current object form with lifecycle scripts.
func TestRepoConfigUnmarshal(t *testing.T) {
	var c Config
	data := []byte(`{"repos": {
		"legacy": "/tmp/legacy",
		"rich": {"path": "/tmp/rich", "setup": "make setup", "cleanup": "make clean"}
	}}`)
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Repos["legacy"].Path != "/tmp/legacy" {
		t.Errorf("legacy path = %q", c.Repos["legacy"].Path)
	}
	r := c.Repos["rich"]
	if r.Path != "/tmp/rich" || r.Setup != "make setup" || r.Cleanup != "make clean" {
		t.Errorf("rich entry = %+v", r)
	}
}
