package fabric

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
)

var (
	// ErrInvalidJSON reports a value that cannot cross the JSON-only bridge.
	ErrInvalidJSON = errors.New("invalid JSON value")
	// ErrJSONLimit reports a value that exceeds a configured bridge bound.
	ErrJSONLimit = errors.New("JSON value exceeds limit")
)

// JSONLimits bound data crossing the guest/host bridge. Zero values select the
// conservative defaults from DefaultJSONLimits.
type JSONLimits struct {
	MaxBytes int
	MaxDepth int
	MaxNodes int
}

// DefaultJSONLimits returns the default bridge bounds.
func DefaultJSONLimits() JSONLimits {
	return JSONLimits{
		MaxBytes: 256 << 10,
		MaxDepth: 32,
		MaxNodes: 16_384,
	}
}

func normalizeJSONLimits(limits JSONLimits) JSONLimits {
	defaults := DefaultJSONLimits()
	if limits.MaxBytes <= 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.MaxDepth <= 0 {
		limits.MaxDepth = defaults.MaxDepth
	}
	if limits.MaxNodes <= 0 {
		limits.MaxNodes = defaults.MaxNodes
	}
	return limits
}

type jsonVisit struct {
	kind reflect.Kind
	ptr  uintptr
}

// ValidateJSON verifies that value contains only JSON data and fits within the
// configured depth, node, and encoded-byte bounds.
func ValidateJSON(value JSONValue, limits JSONLimits) error {
	limits = normalizeJSONLimits(limits)
	nodes := 0
	active := make(map[jsonVisit]struct{})
	if err := walkJSON(reflect.ValueOf(value), 1, limits, &nodes, active); err != nil {
		return err
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if len(encoded) > limits.MaxBytes {
		return fmt.Errorf("%w: encoded bytes %d exceed %d", ErrJSONLimit, len(encoded), limits.MaxBytes)
	}
	return nil
}

func walkJSON(
	value reflect.Value,
	depth int,
	limits JSONLimits,
	nodes *int,
	active map[jsonVisit]struct{},
) error {
	*nodes++
	if *nodes > limits.MaxNodes {
		return fmt.Errorf("%w: nodes exceed %d", ErrJSONLimit, limits.MaxNodes)
	}
	if depth > limits.MaxDepth {
		return fmt.Errorf("%w: depth %d exceeds %d", ErrJSONLimit, depth, limits.MaxDepth)
	}
	if !value.IsValid() {
		return nil
	}

	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return walkJSON(value.Elem(), depth, limits, nodes, active)
	case reflect.Bool, reflect.String:
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return nil
	case reflect.Float32, reflect.Float64:
		number := value.Float()
		if math.IsInf(number, 0) || math.IsNaN(number) {
			return fmt.Errorf("%w: non-finite number", ErrInvalidJSON)
		}
		return nil
	case reflect.Map:
		if value.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("%w: object keys must be strings", ErrInvalidJSON)
		}
		return walkJSONContainer(value, depth, limits, nodes, active, func() error {
			iterator := value.MapRange()
			for iterator.Next() {
				if err := walkJSON(iterator.Value(), depth+1, limits, nodes, active); err != nil {
					return err
				}
			}
			return nil
		})
	case reflect.Slice:
		if value.IsNil() {
			return nil
		}
		return walkJSONContainer(value, depth, limits, nodes, active, func() error {
			for i := range value.Len() {
				if err := walkJSON(value.Index(i), depth+1, limits, nodes, active); err != nil {
					return err
				}
			}
			return nil
		})
	case reflect.Invalid:
		return nil
	default:
		return fmt.Errorf("%w: unsupported kind %s", ErrInvalidJSON, value.Kind())
	}
}

func walkJSONContainer(
	value reflect.Value,
	depth int,
	limits JSONLimits,
	nodes *int,
	active map[jsonVisit]struct{},
	walk func() error,
) error {
	visit := jsonVisit{kind: value.Kind(), ptr: value.Pointer()}
	if visit.ptr != 0 {
		if _, exists := active[visit]; exists {
			return fmt.Errorf("%w: cyclic %s", ErrInvalidJSON, value.Kind())
		}
		active[visit] = struct{}{}
		defer delete(active, visit)
	}
	return walk()
}
