package engine

import (
	"fmt"
	"reflect"
)

func toSlice(value any) ([]any, error) {
	switch typed := value.(type) {
	case []any:
		return typed, nil
	case []string:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return items, nil
	case []map[string]any:
		items := make([]any, 0, len(typed))
		for _, item := range typed {
			items = append(items, item)
		}
		return items, nil
	}

	ref := reflect.ValueOf(value)
	if ref.Kind() != reflect.Slice {
		return nil, fmt.Errorf("expected slice for for_each")
	}
	items := make([]any, 0, ref.Len())
	for i := 0; i < ref.Len(); i++ {
		items = append(items, ref.Index(i).Interface())
	}
	return items, nil
}
