package datasource

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/microsoft/go-mssqldb" // SQL Server 驱动注册
)

// SQLServerDriver SQL Server 2012+ 驱动适配
type SQLServerDriver struct{}

func init() { Register("sqlserver", SQLServerDriver{}) }

func (SQLServerDriver) BuildDSN(c ConnConfig) (string, error) {
	if c.Host == "" {
		return "", fmt.Errorf("SQL Server 主机不能为空")
	}
	// 用 net/url 构建：url.UserPassword 会对 user/pass 中的 @ : / ? 等特殊字符做
	// 百分号编码，避免密码含特殊字符时破坏 URL 解析或注入额外连接参数
	u := &url.URL{
		Scheme: "sqlserver",
		User:   url.UserPassword(c.Username, c.Password),
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
	}
	q := u.Query()
	q.Set("database", c.ServiceName)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func (SQLServerDriver) Open(dsn string, cfg ConnConfig) (*sql.DB, error) {
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, err
	}
	ApplyPool(db, cfg)
	return db, nil
}

func (SQLServerDriver) ServerVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	err := db.QueryRowContext(ctx, "SELECT @@VERSION").Scan(&v)
	return v, err
}

func (SQLServerDriver) ListTables(ctx context.Context, db *sql.DB, keyword string) ([]TableMeta, error) {
	q := `SELECT o.name,
       COALESCE(CAST(ep.value AS NVARCHAR(4000)), ''),
       CASE o.type WHEN 'U' THEN 'TABLE' ELSE 'VIEW' END
FROM sys.objects o
LEFT JOIN sys.extended_properties ep
  ON ep.major_id = o.object_id AND ep.minor_id = 0 AND ep.name = 'MS_Description'
WHERE o.type IN ('U','V') AND SCHEMA_NAME(o.schema_id) = SCHEMA_NAME()`
	args := []any{}
	if keyword != "" {
		q += " AND o.name LIKE ?"
		args = append(args, "%"+keyword+"%")
	}
	q += " ORDER BY o.name"
	return scanTables(ctx, db, q, args)
}

func (SQLServerDriver) ListColumns(ctx context.Context, db *sql.DB, table string) ([]ColumnMeta, error) {
	// 支持「模式.表」跨模式引用（如 dbo.users）；OBJECT_ID 原生支持全限定名
	if err := ValidateTableRef(table); err != nil {
		return nil, err
	}
	full := strings.TrimSpace(table) // OBJECT_ID 等元数据函数需要全限定名
	schema := ""
	if i := strings.Index(table, "."); i >= 0 {
		schema, table = table[:i], table[i+1:]
	}
	// 主键字段集合（OBJECT_ID 需要全限定名才能命中跨模式表）
	pkSet := map[string]bool{}
	pkRows, err := db.QueryContext(ctx, `
SELECT c.name
FROM sys.indexes i
JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
WHERE i.is_primary_key = 1 AND i.object_id = OBJECT_ID(?)`, full)
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

	q := `SELECT c.name, COALESCE(t.name, '') + CASE WHEN t.name IN ('varchar','nvarchar','char','nchar','decimal','numeric')
              THEN '(' + CAST(c.max_length AS VARCHAR) + ')' ELSE '' END,
       c.is_nullable, d.definition,
       COALESCE(CAST(ep.value AS NVARCHAR(4000)), '')
FROM sys.columns c
JOIN sys.types t ON t.user_type_id = c.user_type_id
JOIN sys.objects o ON o.object_id = c.object_id
LEFT JOIN sys.default_constraints d ON d.object_id = c.default_object_id
LEFT JOIN sys.extended_properties ep
  ON ep.major_id = c.object_id AND ep.minor_id = c.column_id AND ep.name = 'MS_Description'
WHERE o.name = ?`
	args := []any{table}
	if schema != "" {
		q += " AND SCHEMA_NAME(o.schema_id) = ?"
		args = append(args, schema)
	} else {
		q += " AND SCHEMA_NAME(o.schema_id) = SCHEMA_NAME()"
	}
	q += " ORDER BY c.column_id"
	rows, err := db.QueryContext(ctx, q, args...)
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

func (SQLServerDriver) QuoteIdentifier(name string) string {
	return "[" + strings.ReplaceAll(name, "]", "]]") + "]"
}

// ListIndexes 索引清单：sys.indexes + sys.index_columns，排除包含列（is_included_column），
// 只保留键列，并用 key_ordinal 还原索引内的列顺序
func (SQLServerDriver) ListIndexes(ctx context.Context, db *sql.DB, table string) ([]IndexMeta, error) {
	if err := ValidateTableRef(table); err != nil {
		return nil, err
	}
	full := strings.TrimSpace(table) // OBJECT_ID 需要全限定名才能命中跨模式表
	q := `SELECT i.name, c.name,
       CASE WHEN i.is_unique = 1 THEN 0 ELSE 1 END AS non_unique,
       CASE WHEN i.is_primary_key = 1 THEN 1 ELSE 0 END AS is_primary
FROM sys.indexes i
JOIN sys.index_columns ic ON ic.object_id = i.object_id AND ic.index_id = i.index_id
JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
WHERE i.object_id = OBJECT_ID(?) AND ic.is_included_column = 0 AND i.name IS NOT NULL
ORDER BY i.name, ic.key_ordinal`
	rows, err := db.QueryContext(ctx, q, full)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanIndexRows(rows)
}

// Rebind :name → @p1..@pn（跳过字符串字面量）
func (SQLServerDriver) Rebind(query string) string {
	i := 0
	return MapOutsideStrings(query, func(s string) string {
		return namedParamRe.ReplaceAllStringFunc(s, func(m string) string {
			i++
			return fmt.Sprintf("@p%d", i)
		})
	})
}

func (SQLServerDriver) Paginate(query string, offset, limit int) string {
	// 先屏蔽字符串字面量与注释再判断：避免 WHERE x = '... order by ...'
	// 或 -- 注释里的 ORDER BY 造成误判（漏加 ORDER BY 时 OFFSET/FETCH 会直接报错）
	upper := strings.ToUpper(MaskComments(MaskStrings(query)))
	if !strings.Contains(upper, "ORDER BY") {
		query += " ORDER BY (SELECT NULL)"
	}
	return fmt.Sprintf("%s OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", query, offset, limit)
}
