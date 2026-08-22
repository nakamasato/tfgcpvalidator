// Package plan reads the subset of `terraform show -json` output that checks need.
package plan

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

type Plan struct {
	FormatVersion   string           `json:"format_version"`
	ResourceChanges []ResourceChange `json:"resource_changes"`
}

type ResourceChange struct {
	Address string `json:"address"`
	Mode    string `json:"mode"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Deposed string `json:"deposed"`
	Change  Change `json:"change"`
}

type Change struct {
	Actions []string       `json:"actions"`
	Before  map[string]any `json:"before"`
	After   map[string]any `json:"after"`
}

func (c Change) HasAction(action string) bool {
	for _, a := range c.Actions {
		if a == action {
			return true
		}
	}
	return false
}

func (rc ResourceChange) IsManaged() bool { return rc.Mode == "managed" }

func Parse(r io.Reader) (*Plan, error) {
	var p Plan
	if err := json.NewDecoder(r).Decode(&p); err != nil {
		return nil, fmt.Errorf("decode plan json: %w", err)
	}
	return &p, nil
}

func Load(path string) (*Plan, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open plan file: %w", err)
	}
	defer f.Close()
	return Parse(f)
}

// Lookup walks a dot-separated path through decoded JSON.
// Terraform renders a single-instance block as a one-element list, so a list on
// the path is searched element by element rather than treated as a dead end.
func Lookup(v any, path string) (any, bool) {
	if path == "" {
		return v, v != nil
	}
	key, rest, _ := strings.Cut(path, ".")
	switch t := v.(type) {
	case map[string]any:
		next, ok := t[key]
		if !ok {
			return nil, false
		}
		return Lookup(next, rest)
	case []any:
		for _, elem := range t {
			if got, ok := Lookup(elem, path); ok {
				return got, true
			}
		}
		return nil, false
	default:
		return nil, false
	}
}
