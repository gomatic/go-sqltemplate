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

func TestClean(t *testing.T) {
	tests := []struct {
		name      string
		paramName sqltemplate.Name
		value     sqltemplate.Value
		want      sqltemplate.Value
		ok        bool
	}{
		{"empty name", "", "v", "", false},
		{"long name", sqltemplate.Name(strings.Repeat("n", 31)), "v", "", false},
		{"dot prefix", ".internal", "v", "", false},
		{"underscore prefix", "_internal", "v", "", false},
		{"empty value", "n", "", "", false},
		{"long value", "n", sqltemplate.Value(strings.Repeat("v", 51)), "", false},
		{"strips dangerous chars", "n", `a';"b`, "ab", true},
		{"plain value", "n", "abc", "abc", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := sqltemplate.Clean(tt.paramName, tt.value)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
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
	// A "_"-prefixed name is dropped by Clean, leaving its bind unprovided so
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
