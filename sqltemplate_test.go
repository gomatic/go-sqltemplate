package sqltemplate_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gomatic/go-sqltemplate"
)

func TestNormalize(t *testing.T) {
	got := sqltemplate.Normalize("select\t1\n  from  t")
	assert.Equal(t, sqltemplate.Statement("select 1 from t"), got)
}

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

func TestParameterizeDropsInvalidNames(t *testing.T) {
	// Empty, over-long, and internal ("."/"_") names are dropped; an unreferenced
	// invalid-named param does not break a statement using only valid names.
	result, err := sqltemplate.Parameterize("select {{good}}", sqltemplate.Params{
		"good": "v",
		"":     "x",
		sqltemplate.Name(strings.Repeat("n", 31)): "y",
		".dot":   "z",
		"_under": "w",
	})

	require.NoError(t, err)
	assert.Equal(t, []sqltemplate.Value{"v"}, result.Bindings)
}

func TestParameterizeSanitizesStaticValues(t *testing.T) {
	// The verbatim {{.name}} path strips ;'" as a backstop (bind values are not).
	result, err := sqltemplate.Parameterize("select * from ({{.src}}) s", sqltemplate.Params{"src": `t'; drop`})

	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select * from (t drop) s"), result.SQL)
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

func TestParameterizeDeduplicatesByValue(t *testing.T) {
	result, err := sqltemplate.Parameterize(
		"select {{a}}, {{b}}, {{c}}",
		sqltemplate.Params{"a": "same", "b": "same", "c": "other"},
	)
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select $1, $1, $2"), result.SQL)
	assert.Equal(t, []sqltemplate.Value{"same", "other"}, result.Bindings)
}

func TestParameterizeSubstitutesStatics(t *testing.T) {
	result, err := sqltemplate.Parameterize(
		"select * from ({{.source}}) as s where id={{id}}",
		sqltemplate.Params{"source": "select 1 as id", "id": "7"},
	)
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select * from (select 1 as id) as s where id=$1"), result.SQL)
	assert.Equal(t, []sqltemplate.Value{"7"}, result.Bindings)
}

func TestParameterizeRestoresMissingStatic(t *testing.T) {
	result, err := sqltemplate.Parameterize("select * from ({{.source}}) as s", nil)
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select * from ({{.source}}) as s"), result.SQL)
	assert.Empty(t, result.Bindings)
}

func TestParameterizeRejectsUnusableParam(t *testing.T) {
	// A "_"-prefixed name is invalid and dropped, leaving its bind unprovided so
	// the template fails to parse.
	_, err := sqltemplate.Parameterize("select {{_secret}}", sqltemplate.Params{"_secret": "x"})
	assert.ErrorIs(t, err, sqltemplate.ErrInvalidStatement)
}

func TestParameterizeParseError(t *testing.T) {
	_, err := sqltemplate.Parameterize("select {{missing}}", nil)
	assert.ErrorIs(t, err, sqltemplate.ErrInvalidStatement)
}

func TestParameterizeExecuteError(t *testing.T) {
	// Parses cleanly but fails at execution because the named template is undefined.
	_, err := sqltemplate.Parameterize(`{{template "nope"}}`, nil)
	assert.ErrorIs(t, err, sqltemplate.ErrInvalidStatement)
}

func TestParameterizeEmptyStatement(t *testing.T) {
	// An empty statement renders to empty SQL with no bindings.
	result, err := sqltemplate.Parameterize("", nil)
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query(""), result.SQL)
	assert.Empty(t, result.Bindings)
}

func TestParameterizeNoVariables(t *testing.T) {
	// A statement with no variables passes through (modulo whitespace) unbound.
	result, err := sqltemplate.Parameterize("select  1", nil)
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select 1"), result.SQL)
	assert.Empty(t, result.Bindings)
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

func TestParameterizeRepeatedNameDeduplicates(t *testing.T) {
	// The same name referenced twice resolves to one binding and a repeated $1.
	result, err := sqltemplate.Parameterize(
		"select {{a}}, {{a}}",
		sqltemplate.Params{"a": "v"},
	)
	require.NoError(t, err)
	assert.Equal(t, sqltemplate.Query("select $1, $1"), result.SQL)
	assert.Equal(t, []sqltemplate.Value{"v"}, result.Bindings)
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

// FuzzParameterize asserts the package's two load-bearing invariants over
// arbitrary statements and (injection-shaped) values:
//
//  1. Total: Parameterize never panics, and every error it returns is the
//     ErrInvalidStatement sentinel (matchable with errors.Is).
//  2. Parameterization: a bound value reaches the caller byte-for-byte. With a
//     single provided param, every emitted binding must equal that exact value,
//     proving bind values are never mutated on their way to the driver.
func FuzzParameterize(f *testing.F) {
	seeds := []struct {
		statement string
		value     string
	}{
		{"select {{x}}", "ordinary"},
		{"select {{x}}", "'; DROP TABLE users; --"},
		{"select {{x}}", `O'Brien`},
		{"select {{x}}", `"quoted"`},
		{"select {{x}}", "a;b'c\"d"},
		{"select {{x}}", ""},
		{"select {{x}}", "üñîçødé 🜲"},
		{"select {{x}}, {{x}}", "dup"},
		{"", "v"},
		{"select * from ({{.x}}) s", "select 1"},
		{"select {{.x}}{{x}}", "/-/x/-/"},
		{"select {{missing}}", "v"},
		{`{{template "nope"}}`, "v"},
		{"select\t{{x}}\n  from t", "ws"},
	}
	for _, seed := range seeds {
		f.Add(seed.statement, seed.value)
	}

	f.Fuzz(func(t *testing.T, statement, value string) {
		result, err := sqltemplate.Parameterize(
			sqltemplate.Statement(statement),
			sqltemplate.Params{"x": sqltemplate.Value(value)},
		)
		if err != nil {
			require.ErrorIs(t, err, sqltemplate.ErrInvalidStatement)
			return
		}
		for _, binding := range result.Bindings {
			assert.Equal(t, sqltemplate.Value(value), binding)
		}
	})
}
