package datasource

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	_ "github.com/lib/pq" // PostgreSQL 驱动注册
)

// PostgresDriver PostgreSQL 驱动适配
type PostgresDriver struct{}

func init() { Register("postgresql", PostgresDriver{}) }

func (PostgresDriver) BuildDSN(c ConnConfig) (string, error) {
	if c.Host == "" {
		return "", fmt.Errorf("PostgreSQL 主机不能为空")
	}
	// lib/pq keyword/value 格式：值必须单引号包裹并转义 \ 与 '，
	// 否则密码含空格/引号/反斜杠时会破坏解析或注入额外连接参数
	q := func(s string) string {
		return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
	}
	return fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		q(c.Host), c.Port, q(c.Username), q(c.Password), q(c.ServiceName)), nil
}

func (PostgresDriver) Open(dsn string, cfg ConnConfig) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	ApplyPool(db, cfg)
	return db, nil
}

func (PostgresDriver) ServerVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, "SELECT version()").Scan(&v)
	return v, err
}

func (PostgresDriver) ListTables(ctx context.Context, db *sql.DB, keyword string) ([]TableMeta, error) {
	q := `SELECT c.relname,
       COALESCE(obj_description(c.oid), ''),
       CASE c.relkind WHEN 'r' THEN 'TABLE' WHEN 'p' THEN 'TABLE' ELSE 'VIEW' END
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relkind IN ('r','p','v')`
	args := []any{}
	if keyword != "" {
		q += " AND c.relname LIKE $1"
		args = append(args, "%"+keyword+"%")
	}
	q += " ORDER BY c.relname"
	return scanTables(ctx, db, q, args)
}

func (PostgresDriver) ListColumns(ctx context.Context, db *sql.DB, table string) ([]ColumnMeta, error) {
	// 支持「模式.表」跨模式引用（如 public.users）；缺省用 current_schema()
	if err := ValidateTableRef(table); err != nil {
		return nil, err
	}
	schema := ""
	if i := strings.Index(table, "."); i >= 0 {
		schema, table = table[:i], table[i+1:]
	}
	// 主键字段集合
	pkSet := map[string]bool{}
	pkRows, err := db.QueryContext(ctx, `
SELECT a.attname
FROM pg_index i
JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
JOIN pg_class t ON t.oid = i.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE i.indisprimary AND t.relname = $1 AND n.nspname = COALESCE(NULLIF($2,''), current_schema())`, table, schema)
	if err != nil {
		return nil, err
	}
	defer pkRows.Close()
	for pkRows.Next() {
		var name string
		if err := pkRows.Scan(&name); err != nil {
			return nil, err
		}
		pkSet[name] = true
	}
	if err := pkRows.Err(); err != nil {
		return nil, err
	}

	q := `SELECT a.attname, format_type(a.atttypid, a.atttypmod),
       NOT a.attnotnull, pg_get_expr(d.adbin, d.adrelid),
       COALESCE(col_description(a.attrelid, a.attnum), '')
FROM pg_attribute a
JOIN pg_class t ON t.oid = a.attrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
WHERE n.nspname = COALESCE(NULLIF($2,''), current_schema()) AND t.relname = $1
  AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`
	rows, err := db.QueryContext(ctx, q, table, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ColumnMeta
	for rows.Next() {
		var m ColumnMeta
		var dft sql.NullString
		if err := rows.Scan(&m.Name, &m.DataType, &m.Nullable, &dft, &m.Comment); err != nil {
			return nil, err
		}
		if dft.Valid {
			m.Default = &dft.String
		}
		m.IsPK = pkSet[m.Name]
		out = append(out, m)
	}
	return out, rows.Err()
}

func (PostgresDriver) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// ListIndexes 索引清单：pg_index + pg_attribute，按 indkey 在索引内的序号（ord）还原列序
func (PostgresDriver) ListIndexes(ctx context.Context, db *sql.DB, table string) ([]IndexMeta, error) {
	if err := ValidateTableRef(table); err != nil {
		return nil, err
	}
	schema := ""
	if i := strings.Index(table, "."); i >= 0 {
		schema, table = table[:i], table[i+1:]
	}
	q := `SELECT i.relname AS index_name,
       a.attname AS column_name,
       CASE WHEN ix.indisunique THEN 0 ELSE 1 END AS non_unique,
       CASE WHEN ix.indisprimary THEN 1 ELSE 0 END AS is_primary
FROM pg_index ix
JOIN pg_class i ON i.oid = ix.indexrelid
JOIN pg_class t ON t.oid = ix.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
JOIN LATERAL unnest(ix.indkey) WITH ORDINALITY AS k(attnum, ord) ON true
JOIN pg_attribute a ON a.attrelid = t.oid AND a.attnum = k.attnum
WHERE t.relname = $1 AND n.nspname = COALESCE(NULLIF($2, ''), current_schema())
ORDER BY i.relname, k.ord`
	rows, err := db.QueryContext(ctx, q, table, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIndexRows(rows)
}

// Rebind :name → $1..$n（按出现顺序编号；跳过字符串字面量）
func (PostgresDriver) Rebind(query string) string {
	i := 0
	return MapOutsideStrings(query, func(s string) string {
		return namedParamRe.ReplaceAllStringFunc(s, func(m string) string {
			i++
			return fmt.Sprintf("$%d", i)
		})
	})
}

func (PostgresDriver) Paginate(query string, offset, limit int) string {
	return fmt.Sprintf("%s LIMIT %d OFFSET %d", query, limit, offset)
}
