package db

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type TableStat struct {
	Filenode uint32
	Size     string
}

func InspectTable(ctx context.Context, conn *pgx.Conn, table string) (TableStat, error) {
	var stat TableStat
	err := conn.QueryRow(ctx, `
		SELECT pg_relation_filenode($1::regclass),
		       pg_size_pretty(pg_total_relation_size($1::regclass))
	`, table).Scan(&stat.Filenode, &stat.Size)
	return stat, err
}
