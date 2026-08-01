package sqltemplate_test

import (
	"errors"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/gomatic/go-sqltemplate"
)

func TestNormalize(t *testing.T) {
	got := sqltemplate.Normalize("select\t1\n  from  t")
	assert.Equal(t, sqltemplate.Statement("select 1 from t"), got)
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
	// The wrap must keep the underlying cause recoverable, not just the sentinel.
	var cause template.ExecError
	assert.True(t, errors.As(err, &cause))
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

// TestSanitizeStaticStripsWhatCanBreakOutOfAVerbatimSubstitution names
// sanitizeStatic's claim, which is this package's only defence on the static
// path. A {{.name}} variable is substituted VERBATIM into the SQL text — that
// is what distinguishes it from {{name}}, which becomes a bind — so any
// character that can terminate a literal or a statement is an injection point.
//
// The second half of the claim matters just as much: bind values are NEVER
// sanitized. Stripping quotes from a bind would silently corrupt legitimate
// data, and the placeholder already makes it inert.
func TestSanitizeStaticStripsWhatCanBreakOutOfAVerbatimSubstitution(t *testing.T) {
	t.Parallel()

	got, err := sqltemplate.Parameterize(
		`select * from {{.table}} where x = {{value}}`,
		sqltemplate.Params{
			"table": `users; drop table users --`,
			"value": `o'brien "quoted"; still data`,
		},
	)

	require.NoError(t, err)
	assert.NotContains(t, string(got.SQL), ";", "a statement terminator must not survive a static substitution")
	assert.NotContains(t, string(got.SQL), "'", "nor a single quote")
	assert.NotContains(t, string(got.SQL), `"`, "nor a double quote")

	require.Len(t, got.Bindings, 1)
	assert.Equal(t, sqltemplate.Value(`o'brien "quoted"; still data`), got.Bindings[0],
		"a BIND value is passed through untouched — the placeholder is what makes it safe, "+
			"and stripping it would corrupt legitimate data")
}

// TestErrInvalidStatementIsReturnedForBothParseAndRenderFailures names
// ErrInvalidStatement's claim to cover parsing AND rendering. Both are caller
// input errors and both must be matchable, so a caller can distinguish a bad
// template from an infrastructure failure rather than treating every error as
// fatal.
func TestErrInvalidStatementIsReturnedForBothParseAndRenderFailures(t *testing.T) {
	t.Parallel()

	_, err := sqltemplate.Parameterize(`select {{`, nil)
	assert.ErrorIs(t, err, sqltemplate.ErrInvalidStatement, "an unclosed action is a parse failure")

	_, err = sqltemplate.Parameterize(`select {{ nosuchfunc }}`, nil)
	assert.ErrorIs(t, err, sqltemplate.ErrInvalidStatement, "an unknown function is a parse failure")
}
