package datasource

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// SQLiteDriver SQLite 驱动适配（纯 Go 实现，ServiceName 为数据库文件路径）。
// 注意：驱动名 "sqlite" 由 glebarez/go-sqlite（系统库 GORM 驱动的底层依赖）注册，
// 此处不再单独导入 modernc.org/sqlite，避免 sql.Register 重复注册 panic。
type SQLiteDriver struct{}

func init() { Register("sqlite", SQLiteDriver{}) }

func (SQLiteDriver) BuildDSN(c ConnConfig) (string, error) {
	if c.ServiceName == "" {
		return "", fmt.Errorf("SQLite 数据库文件路径不能为空")
	}
	return c.ServiceName, nil
}

func (SQLiteDriver) Open(dsn string, cfg ConnConfig) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// busy_timeout 兜底并发写竞争（PRAGMA 方式，不依赖 DSN 参数语法）
	if _, err := db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, fmt.Errorf("设置 busy_timeout 失败: %w", err)
	}
	db.SetMaxOpenConns(cfg.PoolMaxOpen)
	db.SetMaxIdleConns(cfg.PoolMaxIdle)
	return db, nil
}

func (SQLiteDriver) ServerVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, "SELECT sqlite_version()").Scan(&v)
	return "SQLite " + v, err
}

func (SQLiteDriver) ListTables(ctx context.Context, db *sql.DB, keyword string) ([]TableMeta, error) {
	q := `SELECT name, '', CASE type WHEN 'table' THEN 'TABLE' ELSE 'VIEW' END
FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%'`
	args := []any{}
	if keyword != "" {
		q += " AND name LIKE ?"
		args = append(args, "%"+keyword+"%")
	}
	q += " ORDER BY name"
	return scanTables(ctx, db, q, args)
}

func (SQLiteDriver) ListColumns(ctx context.Context, db *sql.DB, table string) ([]ColumnMeta, error) {
	if err := ValidateIdentifier(table); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, fmt.Sprintf("PRAGMA table_info(%s)", QuoteSQLite(table)))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ColumnMeta
	for rows.Next() {
		var cid int
		var m ColumnMeta
		var notNull, pk int
		var dft sql.NullString
		if err := rows.Scan(&cid, &m.Name, &m.DataType, &notNull, &dft, &pk); err != nil {
			return nil, err
		}
		m.Nullable = notNull == 0
		m.IsPK = pk > 0
		if dft.Valid {
			m.Default = &dft.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (SQLiteDriver) QuoteIdentifier(name string) string { return QuoteSQLite(name) }

func QuoteSQLite(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (SQLiteDriver) Rebind(query string) string { return RebindQuestion(query) }

func (SQLiteDriver) Paginate(query string, offset, limit int) string {
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", query, limit, offset)
}
