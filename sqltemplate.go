// Package sqltemplate rewrites a SQL template into a parameterized query.
//
// A statement carries two kinds of variables:
//
//	{{name}}  - rewritten into ordered bind placeholders ($1, $2, ...) whose
//	            values are returned alongside the query. Identical values are
//	            deduplicated to a single placeholder.
//	{{.name}} - substituted verbatim into the statement (for composing SQL
//	            fragments such as a sub-query source).
//
// For example, given the statement
//
//	select * from ({{.source}}) as s where name={{name}}::text and value={{value}}::text
//
// and the parameters name="abc", value="123", source="select 1" the engine
// produces
//
//	select * from (select 1) as s where name=$1::text and value=$2::text
//
// with bindings ["abc", "123"]. Bind values ({{name}}) are passed to the driver
// untouched — the $N placeholders make them injection-safe regardless of content.
//
// Verbatim values ({{.name}}) are substituted directly into the SQL text, so they
// must be TRUSTED fragments (e.g. a controlled sub-query source). The strip of
// ;'" is only a backstop, not a defense against injection; never feed untrusted
// input through {{.name}} — use a {{name}} bind placeholder instead.
//
// The package is pure and has no dependencies beyond the standard library: it
// performs only string and text/template work and never touches a database or
// driver.
package sqltemplate

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"text/template"
)

// Error is the sentinel error type for the sqltemplate package.
type Error string

// Error returns the error message.
func (e Error) Error() string { return string(e) }

// ErrInvalidStatement is returned when a statement cannot be parsed or rendered
// into a parameterized query.
const ErrInvalidStatement Error = "sqltemplate: invalid sql statement"

type (
	// Statement is a SQL template containing {{name}} and {{.name}} variables.
	Statement string
	// Query is a rendered statement whose binds are $1, $2, ... placeholders.
	Query string
	// Name is the identifier of a template variable.
	Name string
	// Value is the textual value bound to a template variable.
	Value string
)

// Params maps variable names to their values.
type Params map[Name]Value

// Result is the outcome of rendering a statement.
type Result struct {
	SQL      Query   `json:"sql"`
	Bindings []Value `json:"bindings"`
}

const maxNameLength = 30

var (
	reStaticVariable  = regexp.MustCompile(`[{]{2}[.]([^}]+)[}]{2}`)
	reMissingVariable = regexp.MustCompile(`/-/([^/]+)/-/`)
	reWhitespace      = regexp.MustCompile(`\s+`)
	valueStripper     = strings.NewReplacer(`;`, ``, `'`, ``, `"`, ``)
)

// Normalize collapses runs of whitespace in a statement to single spaces.
func Normalize(statement Statement) Statement {
	return Statement(reWhitespace.ReplaceAllString(string(statement), " "))
}

// validName reports whether a variable name may be substituted. Internal names
// (prefixed with "." or "_") and over-long names are rejected. Values are not
// constrained — a bind value may be any length, including empty.
func validName(name Name) bool {
	if len(name) == 0 || len(name) > maxNameLength {
		return false
	}
	return !strings.HasPrefix(string(name), ".") && !strings.HasPrefix(string(name), "_")
}

// usable returns the parameters whose names are valid, with values UNTOUCHED so
// that bind values reach the driver unmodified.
func usable(params Params) Params {
	out := make(Params, len(params))
	for name, value := range params {
		if validName(name) {
			out[name] = value
		}
	}
	return out
}

// sanitizeStatic strips characters that could break out of a verbatim {{.name}}
// substitution. It applies only to the static path; bind values are never
// sanitized.
func sanitizeStatic(params Params) Params {
	out := make(Params, len(params))
	for name, value := range params {
		out[name] = Value(valueStripper.Replace(string(value)))
	}
	return out
}

// binder assigns ordered, value-deduplicated bind placeholders.
//
// The pointer receiver is required: a binder accumulates placeholder ordering
// and the bindings slice as the template engine invokes its functions.
type binder struct {
	order    map[Value]int
	bindings []Value
}

func newBinder() *binder {
	return &binder{order: map[Value]int{}}
}

// placeholder returns the $N placeholder for value, allocating a new binding
// the first time a distinct value is seen.
func (b *binder) placeholder(value Value) string {
	if position, seen := b.order[value]; seen {
		return fmt.Sprintf("$%d", position)
	}
	b.bindings = append(b.bindings, value)
	position := len(b.bindings)
	b.order[value] = position
	return fmt.Sprintf("$%d", position)
}

// funcs builds the template function map: each parameter name resolves to its
// bind placeholder when the engine encounters {{name}}.
func (b *binder) funcs(params Params) template.FuncMap {
	functions := make(template.FuncMap, len(params))
	for name, value := range params {
		bound := value
		functions[string(name)] = func() string { return b.placeholder(bound) }
	}
	return functions
}

// replaceStatics substitutes {{.name}} with its verbatim value, marking
// unprovided statics so they survive template execution.
func replaceStatics(statement Statement, params Params) Statement {
	replace := func(match string) string {
		name := Name(reStaticVariable.FindStringSubmatch(match)[1])
		if value, ok := params[name]; ok {
			return string(value)
		}
		return fmt.Sprintf("/-/%s/-/", name)
	}
	return Statement(reStaticVariable.ReplaceAllStringFunc(string(statement), replace))
}

// restoreMissing turns unprovided static markers back into {{.name}}.
func restoreMissing(query Query) Query {
	restore := func(match string) string {
		name := reMissingVariable.FindStringSubmatch(match)[1]
		return fmt.Sprintf("{{.%s}}", name)
	}
	return Query(reMissingVariable.ReplaceAllStringFunc(string(query), restore))
}

// Parameterize renders statement against params into a query plus bindings.
func Parameterize(statement Statement, params Params) (Result, error) {
	use := usable(params)
	prepared := replaceStatics(Normalize(statement), sanitizeStatic(use))

	binder := newBinder()
	parsed, err := template.New("").Funcs(binder.funcs(use)).Parse(string(prepared))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidStatement, err)
	}

	var rendered bytes.Buffer
	if err := parsed.Execute(&rendered, nil); err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrInvalidStatement, err)
	}

	return Result{
		SQL:      restoreMissing(Query(rendered.String())),
		Bindings: binder.bindings,
	}, nil
}
