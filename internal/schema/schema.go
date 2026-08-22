// Package schema reads `terraform providers schema -json` output so that a
// field path can be checked against what the provider actually declares.
package schema

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

type Attr struct {
	// Primitive is "bool", "string" or "number". Composite types (lists, sets,
	// objects) leave it empty, because a protection flag is never one.
	Primitive string
}

type Schema struct {
	provider  string
	resources map[string]block
}

type block struct {
	Attributes map[string]rawAttr  `json:"attributes"`
	BlockTypes map[string]nestedBl `json:"block_types"`
}

type rawAttr struct {
	// A primitive is a bare string; a composite is a nested array, so the type
	// has to survive decoding before it can be narrowed.
	Type json.RawMessage `json:"type"`
}

type nestedBl struct {
	NestingMode string `json:"nesting_mode"`
	Block       block  `json:"block"`
}

func Load(path, provider string) (*Schema, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read provider schema: %w", err)
	}

	var doc struct {
		ProviderSchemas map[string]struct {
			ResourceSchemas map[string]struct {
				Block block `json:"block"`
			} `json:"resource_schemas"`
		} `json:"provider_schemas"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("decode provider schema: %w", err)
	}

	p, ok := doc.ProviderSchemas[provider]
	if !ok {
		have := make([]string, 0, len(doc.ProviderSchemas))
		for k := range doc.ProviderSchemas {
			have = append(have, k)
		}
		sort.Strings(have)
		return nil, fmt.Errorf("provider %q not in schema; it has %v", provider, have)
	}

	s := &Schema{provider: provider, resources: make(map[string]block, len(p.ResourceSchemas))}
	for name, r := range p.ResourceSchemas {
		s.resources[name] = r.Block
	}
	return s, nil
}

func (s *Schema) Provider() string { return s.provider }

func (s *Schema) ResourceTypes() []string {
	out := make([]string, 0, len(s.resources))
	for name := range s.resources {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Attributes flattens a resource type into dotted paths. Only list-nested
// blocks are descended, because those are the ones Terraform renders inline in
// a plan's before/after values.
func (s *Schema) Attributes(resourceType string) (map[string]Attr, error) {
	b, ok := s.resources[resourceType]
	if !ok {
		return nil, fmt.Errorf("no resource type %q in %s", resourceType, s.provider)
	}
	out := map[string]Attr{}
	flatten(b, "", out)
	return out, nil
}

func flatten(b block, prefix string, out map[string]Attr) {
	for name, a := range b.Attributes {
		out[prefix+name] = Attr{Primitive: primitive(a.Type)}
	}
	for name, nested := range b.BlockTypes {
		if nested.NestingMode != "list" {
			continue
		}
		flatten(nested.Block, prefix+name+".", out)
	}
}

func primitive(t json.RawMessage) string {
	var s string
	if err := json.Unmarshal(t, &s); err != nil {
		return ""
	}
	switch s {
	case "bool", "string", "number":
		return s
	}
	return ""
}

// Resolve walks a dotted path and fails if any segment is missing, is a block
// with the wrong nesting, or ends on something that is not a primitive.
func (s *Schema) Resolve(resourceType, path string) (Attr, error) {
	b, ok := s.resources[resourceType]
	if !ok {
		return Attr{}, fmt.Errorf("no resource type %q in %s", resourceType, s.provider)
	}

	parts := strings.Split(path, ".")
	for _, p := range parts[:len(parts)-1] {
		nested, ok := b.BlockTypes[p]
		if !ok {
			return Attr{}, fmt.Errorf("%s: no block %q in path %q", resourceType, p, path)
		}
		if nested.NestingMode != "list" {
			return Attr{}, fmt.Errorf("%s.%s: block %q nests as %q, not list", resourceType, path, p, nested.NestingMode)
		}
		b = nested.Block
	}

	leaf := parts[len(parts)-1]
	a, ok := b.Attributes[leaf]
	if !ok {
		return Attr{}, fmt.Errorf("%s: no attribute %q in path %q", resourceType, leaf, path)
	}
	prim := primitive(a.Type)
	if prim == "" {
		return Attr{}, fmt.Errorf("%s.%s: not a primitive attribute", resourceType, path)
	}
	return Attr{Primitive: prim}, nil
}

// Accepts reports whether value could legitimately appear at that path in a
// plan, which is what keeps a generated fixture honest.
func (s *Schema) Accepts(resourceType, path string, value any) error {
	a, err := s.Resolve(resourceType, path)
	if err != nil {
		return err
	}
	switch a.Primitive {
	case "bool":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s.%s: declared bool, got %T (%v)", resourceType, path, value, value)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s.%s: declared string, got %T (%v)", resourceType, path, value, value)
		}
	case "number":
		switch value.(type) {
		case int, int64, float64:
		default:
			return fmt.Errorf("%s.%s: declared number, got %T (%v)", resourceType, path, value, value)
		}
	}
	return nil
}
