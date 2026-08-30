package fabric

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	maxDeclarationSchemaDepth = 6
	maxDeclarationAlternates  = 12
	maxDeclarationTypeBytes   = 2_500
)

var typeScriptIdentifierPattern = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// Declarations returns a deterministic, bounded TypeScript surface generated
// from the pinned capability descriptors. Registry validation remains
// authoritative if a schema cannot be represented exactly in TypeScript.
func (r *Registry) Declarations(view *CapabilityView) (string, error) {
	catalog, err := r.Catalog(view)
	if err != nil {
		return "", err
	}
	providers := make(map[string][]CatalogAction)
	for _, action := range catalog {
		if !typeScriptIdentifierPattern.MatchString(action.Provider) || strings.Contains(action.Descriptor.Name, ".") {
			continue
		}
		providers[action.Provider] = append(providers[action.Provider], action)
	}
	providerNames := make([]string, 0, len(providers))
	for provider := range providers {
		providerNames = append(providerNames, provider)
	}
	slices.Sort(providerNames)

	var declarations strings.Builder
	for _, provider := range providerNames {
		declarations.WriteString("declare const ")
		declarations.WriteString(provider)
		declarations.WriteString(": {\n")
		actions := providers[provider]
		slices.SortFunc(actions, func(a, b CatalogAction) int {
			return strings.Compare(a.Descriptor.Name, b.Descriptor.Name)
		})
		for _, action := range actions {
			declarations.WriteString("  ")
			declarations.WriteString(typeScriptProperty(action.Descriptor.Name))
			args, required := declarationArguments(action.Descriptor.InputSchema)
			declarations.WriteString("(args")
			if !required {
				declarations.WriteByte('?')
			}
			declarations.WriteString(": ")
			declarations.WriteString(args)
			declarations.WriteString("): Promise<unknown>;\n")
		}
		declarations.WriteString("};\n")
	}
	return declarations.String(), nil
}

func declarationArguments(raw json.RawMessage) (string, bool) {
	var schema any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&schema) != nil {
		return "Record<string, unknown>", false
	}
	rendered := renderSchemaType(schema, 0)
	if len(rendered) > maxDeclarationTypeBytes {
		return "Record<string, unknown>", false
	}
	required := false
	if object, ok := schema.(map[string]any); ok {
		if values, ok := object["required"].([]any); ok {
			required = len(values) > 0
		}
	}
	return rendered, required
}

func renderSchemaType(schema any, depth int) string {
	if depth > maxDeclarationSchemaDepth {
		return "unknown"
	}
	object, ok := schema.(map[string]any)
	if !ok {
		return "unknown"
	}
	if value, exists := object["const"]; exists {
		return typeScriptLiteral(value)
	}
	if values, ok := object["enum"].([]any); ok && len(values) > 0 {
		return renderUnion(values, depth)
	}
	for _, key := range []string{"anyOf", "oneOf"} {
		if values, ok := object[key].([]any); ok && len(values) > 0 {
			return renderUnion(values, depth)
		}
	}
	if values, ok := object["allOf"].([]any); ok && len(values) > 0 {
		parts := make([]string, 0, min(len(values), maxDeclarationAlternates))
		for _, value := range values[:min(len(values), maxDeclarationAlternates)] {
			parts = append(parts, "("+renderSchemaType(value, depth+1)+")")
		}
		return strings.Join(parts, " & ")
	}

	typeName, _ := object["type"].(string)
	switch typeName {
	case "string":
		return "string"
	case "number", "integer":
		return "number"
	case "boolean":
		return "boolean"
	case "null":
		return "null"
	case "array":
		return "Array<" + renderSchemaType(object["items"], depth+1) + ">"
	case "object", "":
		if _, hasProperties := object["properties"]; hasProperties || typeName == "object" {
			return renderObjectType(object, depth)
		}
	}
	return "unknown"
}

func renderObjectType(schema map[string]any, depth int) string {
	properties, _ := schema["properties"].(map[string]any)
	required := make(map[string]struct{})
	if values, ok := schema["required"].([]any); ok {
		for _, value := range values {
			if name, ok := value.(string); ok {
				required[name] = struct{}{}
			}
		}
	}
	names := make([]string, 0, len(properties))
	for name := range properties {
		names = append(names, name)
	}
	slices.Sort(names)
	members := make([]string, 0, len(names)+1)
	for _, name := range names {
		optional := "?"
		if _, ok := required[name]; ok {
			optional = ""
		}
		members = append(members, typeScriptProperty(name)+optional+": "+renderSchemaType(properties[name], depth+1))
	}
	if additional, exists := schema["additionalProperties"]; !exists || additional != false {
		additionalType := "unknown"
		if _, ok := additional.(map[string]any); ok {
			additionalType = renderSchemaType(additional, depth+1)
		}
		members = append(members, "[key: string]: "+additionalType)
	}
	return "{ " + strings.Join(members, "; ") + " }"
}

func renderUnion(values []any, depth int) string {
	limit := min(len(values), maxDeclarationAlternates)
	parts := make([]string, 0, limit)
	for _, value := range values[:limit] {
		if _, ok := value.(map[string]any); ok {
			parts = append(parts, renderSchemaType(value, depth+1))
		} else {
			parts = append(parts, typeScriptLiteral(value))
		}
	}
	slices.Sort(parts)
	parts = slices.Compact(parts)
	return strings.Join(parts, " | ")
}

func typeScriptProperty(name string) string {
	if typeScriptIdentifierPattern.MatchString(name) {
		return name
	}
	encoded, _ := json.Marshal(name)
	return string(encoded)
}

func typeScriptLiteral(value any) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case bool:
		if value {
			return "true"
		}
		return "false"
	case string:
		encoded, _ := json.Marshal(value)
		return string(encoded)
	case json.Number:
		return value.String()
	case float64:
		return fmt.Sprint(value)
	default:
		return "unknown"
	}
}
