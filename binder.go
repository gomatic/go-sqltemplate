package sqltemplate

import (
	"fmt"
	"text/template"
)

// Bind allocation. A bind value never reaches the SQL text — only its $N
// placeholder does — so this half of the package is what makes an arbitrary
// caller value safe, and the ORDER it assigns is load-bearing: the driver
// matches placeholders to the bindings slice by position.

// binder assigns ordered, value-deduplicated bind placeholders.
//
// binder is a value type: the accumulating state (placeholder ordering and the
// bindings slice) lives behind reference fields — the order map and the
// *[]Value bindings pointer — so copies share the same state and every method
// takes a value receiver.
type binder struct {
	order    map[Value]int
	bindings *[]Value
}

func newBinder() binder {
	return binder{order: map[Value]int{}, bindings: &[]Value{}}
}

// placeholder returns the $N placeholder for value, allocating a new binding
// the first time a distinct value is seen.
func (b binder) placeholder(value Value) string {
	if position, seen := b.order[value]; seen {
		return fmt.Sprintf("$%d", position)
	}
	*b.bindings = append(*b.bindings, value)
	position := len(*b.bindings)
	b.order[value] = position
	return fmt.Sprintf("$%d", position)
}

// funcs builds the template function map: each parameter name resolves to its
// bind placeholder when the engine encounters {{name}}.
func (b binder) funcs(params Params) template.FuncMap {
	functions := make(template.FuncMap, len(params))
	for name, value := range params {
		bound := value
		functions[string(name)] = func() string { return b.placeholder(bound) }
	}
	return functions
}
