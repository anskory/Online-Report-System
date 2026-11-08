package datasource

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql" // MySQL 驱动注册（init 中完成，此处引用 Config 构建安全 DSN）
)

// MySQLDriver MySQL 8.x 驱动适配
type MySQLDriver struct{}

func init() { Register("mysql", MySQLDriver{}) }

func (MySQLDriver) BuildDSN(c ConnConfig) (string, error) {
	if c.Host == "" {
		return "", fmt.Errorf("MySQL 主机不能为空")
	}
	// 用 mysql.Config.FormatDSN 构建：驱动内部会对 user/pass/dbname 做转义，
	// 避免密码含 @ : / ? 等特殊字符时破坏 DSN 结构或被注入额外参数
	cfg := mysql.NewConfig()
	cfg.User = c.Username
	cfg.Passwd = c.Password
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", c.Host, c.Port)
	cfg.DBName = c.ServiceName
	cfg.Params = map[string]string{"charset": "utf8mb4"}
	cfg.ParseTime = true
	cfg.Loc = time.Local
	cfg.InterpolateParams = true
	return cfg.FormatDSN(), nil
}

func (MySQLDriver) Open(dsn string, cfg ConnConfig) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	ApplyPool(db, cfg)
	return db, nil
}

func (MySQLDriver) ServerVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&v)
	return "MySQL " + v, err
}

func (MySQLDriver) ListTables(ctx context.Context, db *sql.DB, keyword string) ([]TableMeta, error) {
	q := `SELECT table_name, COALESCE(table_comment,''), CASE WHEN table_type='BASE TABLE' THEN 'TABLE' ELSE 'VIEW' END
FROM information_schema.tables WHERE table_schema = DATABASE()`
	args := []any{}
	if keyword != "" {
		q += " AND table_name LIKE ?"
		args = append(args, "%"+keyword+"%")
	}
	q += " ORDER BY table_name"
	return scanTables(ctx, db, q, args)
}

func (MySQLDriver) ListColumns(ctx context.Context, db *sql.DB, table string) ([]ColumnMeta, error) {
	// 支持「库.表」跨库引用（与 Oracle 的 OWNER.TABLE 对齐）
	if err := ValidateTableRef(table); err != nil {
		return nil, err
	}
	schema := ""
	if i := strings.Index(table, "."); i >= 0 {
		schema, table = table[:i], table[i+1:]
	}
	q := `SELECT c.column_name, c.column_type,
       c.is_nullable = 'YES', c.column_default,
       COALESCE(c.column_comment,''), (c.column_key = 'PRI')
FROM information_schema.columns c
WHERE c.table_name = ?`
	args := []any{table}
	if schema != "" {
		q += " AND c.table_schema = ?"
		args = append(args, schema)
	} else {
		q += " AND c.table_schema = DATABASE()"
	}
	q += " ORDER BY c.ordinal_position"
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ColumnMeta
	for rows.Next() {
		var m ColumnMeta
		var dft sql.NullString
		if err := rows.Scan(&m.Name, &m.DataType, &m.Nullable, &dft, &m.Comment, &m.IsPK); err != nil {
			return nil, err
		}
		if dft.Valid {
			m.Default = &dft.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (MySQLDriver) QuoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func (MySQLDriver) Rebind(query string) string { return RebindQuestion(query) }

func (MySQLDriver) Paginate(query string, offset, limit int) string {
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", query, limit, offset)
}

// ColumnComments 按列名批量查询当前库的字段注释（展示列显示名的默认值）
func (MySQLDriver) ColumnComments(ctx context.Context, db *sql.DB, cols []string) (map[string]string, error) {
	out := map[string]string{}
	// 去重 + 仅保留合法标识符（防注入）
	uniq := make([]string, 0, len(cols))
	seen := map[string]bool{}
	for _, c := range cols {
		if err := ValidateIdentifier(c); err != nil {
			continue
		}
		u := strings.ToLower(c)
		if !seen[u] {
			seen[u] = true
			uniq = append(uniq, c)
		}
	}
	if len(uniq) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(uniq)), ",")
	q := "SELECT column_name, MAX(column_comment) FROM information_schema.columns " +
		"WHERE table_schema = DATABASE() AND column_name IN (" + ph + ") GROUP BY column_name"
	args := make([]any, 0, len(uniq))
	for _, c := range uniq {
		args = append(args, c)
	}
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var cmt sql.NullString
		if err := rows.Scan(&name, &cmt); err != nil {
			return nil, err
		}
		if cmt.Valid && cmt.String != "" {
			out[strings.ToLower(name)] = cmt.String
		}
	}
	return out, rows.Err()
}

// scanTables 通用表清单扫描
func scanTables(ctx context.Context, db *sql.DB, q string, args []any) ([]TableMeta, error) {
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TableMeta
	for rows.Next() {
		var m TableMeta
		if err := rows.Scan(&m.Name, &m.Comment, &m.TableType); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
