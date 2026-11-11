package db

import sq "github.com/Masterminds/squirrel"

// SQL builds queries with PostgreSQL ($1, $2, ...) placeholders. Use it for
// queries assembled at runtime (filters, sorting, pagination, table names);
// fixed queries are clearer as plain SQL strings.
var SQL = sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
