package sqltemplate_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gomatic/go-sqltemplate"
)

func TestParameterizeBindsValuesUntouched(t *testing.T) {
	// Bind values must reach the driver verbatim; the $N placeholder makes them
	// safe. The old code sanitized them, corrupting O'Brien -> OBrien.
	long := sqltemplate.Value(strings.Repeat("x", 80))

	result, err := sqltemplate.Parameterize(
		"insert into t values ({{name}}, {{note}})",
		sqltemplate.Params{"name": "O'Brien", "note": long},
	)

	require.NoError(t, err)
	assert.ElementsMatch(t, []sqltemplate.Value{"O'Brien", long}, result.Bindings)
}

func TestParameterizeBindsValues(t *testing.T) {
	result, err := sqltemplate.Parameterize(
		"select * from t where name={{name}}::text and value={{value}}::text",
		sqltemplate.Params{"name": "abc", "value": "123"},
	)
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select * from t where name=$1::text and value=$2::text"), result.SQL)
	assert.Equal(t, []sqltemplate.Value{"abc", "123"}, result.Bindings)
}

func TestParameterizeBindValuePreservesInjection(t *testing.T) {
	// SECURITY: an injection-shaped bind value is parameterized, never interpolated.
	// The $N placeholder carries it to the driver byte-for-byte; none of its
	// dangerous characters leak into the SQL text.
	payload := sqltemplate.Value(`'; DROP TABLE users; --`)
	result, err := sqltemplate.Parameterize(
		"select * from t where name={{name}}",
		sqltemplate.Params{"name": payload},
	)
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select * from t where name=$1"), result.SQL)
	assert.Equal(t, []sqltemplate.Value{payload}, result.Bindings)
	assert.NotContains(t, string(result.SQL), "DROP")
	assert.NotContains(t, string(result.SQL), ";")
	assert.NotContains(t, string(result.SQL), "'")
	assert.NotContains(t, string(result.SQL), "--")
}

func TestParameterizeUnreferencedValidParamYieldsNoBinding(t *testing.T) {
	// A valid but unreferenced param allocates no placeholder: bindings are
	// allocated lazily, only when the template actually emits the variable.
	result, err := sqltemplate.Parameterize(
		"select {{used}}",
		sqltemplate.Params{"used": "u", "unused": "x"},
	)
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select $1"), result.SQL)
	assert.Equal(t, []sqltemplate.Value{"u"}, result.Bindings)
}

func TestParameterizeEmptyBindValue(t *testing.T) {
	// An empty value is a legitimate bind: it is parameterized, not dropped.
	result, err := sqltemplate.Parameterize("select {{v}}", sqltemplate.Params{"v": ""})
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select $1"), result.SQL)
	assert.Equal(t, []sqltemplate.Value{""}, result.Bindings)
}

func TestParameterizeUnicodeBindValuePreserved(t *testing.T) {
	// Unicode bind values reach the driver untouched.
	value := sqltemplate.Value("üñîçødé — 名前 — 🜲")
	result, err := sqltemplate.Parameterize("select {{v}}", sqltemplate.Params{"v": value})
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select $1"), result.SQL)
	assert.Equal(t, []sqltemplate.Value{value}, result.Bindings)
}

func TestParameterizeStaticAndBindShareName(t *testing.T) {
	// When one name is used both verbatim ({{.x}}, sanitized) and as a bind
	// ({{x}}, untouched), each path keeps its own contract.
	result, err := sqltemplate.Parameterize(
		"select {{.x}}, {{x}}",
		sqltemplate.Params{"x": "a'b"},
	)
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select ab, $1"), result.SQL)
	assert.Equal(t, []sqltemplate.Value{"a'b"}, result.Bindings)
}

// TestBinderDeduplicatesByValueAndKeepsPlaceholderOrder names binder's claim.
// The same value used twice must reuse one placeholder — emitting $1 and $2 for
// identical values would grow the bind list without bound on a repetitive
// template — while distinct values must keep distinct, ascending placeholders,
// because the driver matches them to the bindings slice by POSITION. A
// deduplication that reordered would send the right values to the wrong columns.
func TestBinderDeduplicatesByValueAndKeepsPlaceholderOrder(t *testing.T) {
	t.Parallel()

	got, err := sqltemplate.Parameterize(
		`select {{a}}, {{b}}, {{c}}, {{d}} from t`,
		sqltemplate.Params{"a": "one", "b": "two", "c": "one", "d": "three"},
	)

	require.NoError(t, err)
	assert.Equal(t, []sqltemplate.Value{"one", "two", "three"}, got.Bindings,
		"a repeated value binds once, in first-seen order")
	assert.Contains(t, string(got.SQL), "$1")
	assert.Contains(t, string(got.SQL), "$2")
	assert.Contains(t, string(got.SQL), "$3")
	assert.NotContains(t, string(got.SQL), "$4", "no placeholder may exceed the bindings length")
}
