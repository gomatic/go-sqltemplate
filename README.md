# go-sqltemplate

Rewrite a SQL template into a parameterized query. A statement carries two kinds of variable: `{{name}}` becomes an ordered, value-deduplicated bind placeholder (`$1`, `$2`, …) whose values are returned alongside the query, and `{{.name}}` is substituted verbatim (sanitized) for composing SQL fragments such as a sub-query source.

## Install

```sh
go get github.com/gomatic/go-sqltemplate
```

## Usage

```go
package main

import (
	"fmt"

	"github.com/gomatic/go-sqltemplate"
)

func main() {
	result, err := sqltemplate.Parameterize(
		"select * from ({{.source}}) as s where name={{name}}::text and value={{value}}::text",
		sqltemplate.Params{"source": "select 1", "name": "abc", "value": "123"},
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.SQL)      // select * from (select 1) as s where name=$1::text and value=$2::text
	fmt.Println(result.Bindings) // [abc 123]
}
```

Verbatim values are sanitized before substitution; bind values are passed through untouched for the driver to parameterize. A statement that cannot be parsed or rendered returns an error matchable with `errors.Is(err, sqltemplate.ErrInvalidStatement)`.

## Maintenance

The shared build config (`Makefile`, `.golangci.yaml`, `.editorconfig`, `.gitignore`, `.github/`) is owned and distributed by [`nicerobot/tools.repository`](https://github.com/nicerobot/tools.repository) — do not edit it in-tree; per-repo divergence belongs in a `Makefile.local`.
