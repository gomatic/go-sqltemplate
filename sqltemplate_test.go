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
