// Package jsonmerge merges JSON objects without retaining a general-purpose
// configuration merging dependency.
package jsonmerge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
)

// Merge combines JSON objects from left to right. Scalar values are replaced,
// nested objects are merged recursively, arrays are appended, and null values
// leave the existing value unchanged.
func Merge(inputs ...[]byte) ([]byte, error) {
	merged := make(map[string]any)
	for i, input := range inputs {
		if len(bytes.TrimSpace(input)) == 0 {
			continue
		}
		var source map[string]any
		if err := json.Unmarshal(input, &source); err != nil {
			return nil, fmt.Errorf("decode input %d: %w", i, err)
		}
		if err := mergeMap(merged, source); err != nil {
			return nil, fmt.Errorf("merge input %d: %w", i, err)
		}
	}
	return json.Marshal(merged)
}

func mergeMap(target, source map[string]any) error {
	for key, incoming := range source {
		current, exists := target[key]
		if !exists || current == nil {
			target[key] = incoming
			continue
		}
		if incoming == nil {
			continue
		}
		if reflect.TypeOf(current) != reflect.TypeOf(incoming) {
			return fmt.Errorf("field %q: type mismatch, expected %T, incoming %T", key, current, incoming)
		}
		switch value := incoming.(type) {
		case map[string]any:
			if err := mergeMap(current.(map[string]any), value); err != nil {
				return fmt.Errorf("field %q: %w", key, err)
			}
		case []any:
			target[key] = append(current.([]any), value...)
		default:
			target[key] = incoming
		}
	}
	return nil
}
