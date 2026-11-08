//go:build oracle

package datasource

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/godror/godror" // Oracle 驱动注册（CGO + Instant Client）
)

// OracleDriverCGO 基于 godror 的 Oracle 适配（12c+ 起步，11g 降级 ROWNUM）。
// 编译方式：go build -tags oracle（需 CGO 工具链 + Oracle Instant Client）。
type OracleDriverCGO struct{}

func init() { Register("oracle", OracleDriverCGO{}) }

func (OracleDriverCGO) BuildDSN(c ConnConfig) (string, error) {
	// 支持 TNS 直连串（RAC/Scan 场景）
	if c.ExtraConn != "" {
		return c.ExtraConn, nil
	}
	if c.Host == "" {
		return "", fmt.Errorf("Oracle 主机不能为空")
	}
	// 用 oracle:// URL 形式构建（godror 原生支持，经 net/url 解析）：
	// url.UserPassword 会对 user/pass 中的 @ / : 等特殊字符做百分号编码，
	// 避免 EZCONNECT 形式（user/pass@host:port/service）下密码含 / 或 @ 时解析错位
	u := &url.URL{
		Scheme: "oracle",
		User:   url.UserPassword(c.Username, c.Password),
		Host:   fmt.Sprintf("%s:%d", c.Host, c.Port),
		Path:   "/" + c.ServiceName,
	}
	return u.String(), nil
}

func (OracleDriverCGO) Open(dsn string, cfg ConnConfig) (*sql.DB, error) {
	db, err := sql.Open("godror", dsn)
	if err != nil {
		return nil, err
	}
	ApplyPool(db, cfg)
	return db, nil
}

func (OracleDriverCGO) ServerVersion(ctx context.Context, db *sql.DB) (string, error) {
	var v string
	err := db.QueryRowContext(ctx,
		"SELECT banner FROM v$version WHERE ROWNUM = 1").Scan(&v)
	return v, err
}

// oracleSysSchemas 定义在 oracle_common.go（两种实现共用）

func (OracleDriverCGO) ListTables(ctx context.Context, db *sql.DB, keyword string) ([]TableMeta, error) {
	// all_tables/all_views：当前用户自己的表 + 已获 SELECT 授权的其他模式表。
	// 跨模式表显示为 OWNER.TABLE_NAME，与报表 SQL 中必须写的模式前缀保持一致。
	q := `SELECT CASE WHEN t.owner = USER THEN t.table_name ELSE t.owner || '.' || t.table_name END AS table_name,
       COALESCE(c.comments, ' ') AS comments,
       'TABLE' AS table_type
FROM all_tables t
LEFT JOIN all_tab_comments c ON c.owner = t.owner AND c.table_name = t.table_name
WHERE t.owner NOT IN (` + oracleSysSchemas + `)
UNION ALL
SELECT CASE WHEN v.owner = USER THEN v.view_name ELSE v.owner || '.' || v.view_name END AS table_name,
       COALESCE(vc.comments, ' ') AS comments,
       'VIEW' AS table_type
FROM all_views v
LEFT JOIN all_tab_comments vc ON vc.owner = v.owner AND vc.table_name = v.view_name
WHERE v.owner NOT IN (` + oracleSysSchemas + `)`
	args := []any{}
	if keyword != "" {
		q = `SELECT * FROM (` + q + `) WHERE table_name LIKE :kw ORDER BY table_name`
		args = append(args, "%"+strings.ToUpper(keyword)+"%")
	} else {
		q += " ORDER BY 1"
	}
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

func (OracleDriverCGO) ListColumns(ctx context.Context, db *sql.DB, table string) ([]ColumnMeta, error) {
	table = strings.ToUpper(strings.TrimSpace(table))
	// 支持跨模式表名 OWNER.TABLE（与表清单显示一致）；owner 为空 = 当前用户
	owner := ""
	if i := strings.Index(table, "."); i >= 0 {
		owner, table = table[:i], table[i+1:]
	}
	if err := ValidateIdentifier(table); err != nil {
		return nil, err
	}
	if owner != "" {
		if err := ValidateIdentifier(owner); err != nil {
			return nil, err
		}
	}

	// 主键字段集合（按是否跨模式选择 user_* / all_* 视图）
	var pkQuery string
	var pkArgs []any
	if owner != "" {
		pkQuery = `SELECT cc.column_name
FROM all_constraints con
JOIN all_cons_columns cc ON cc.owner = con.owner AND cc.constraint_name = con.constraint_name
WHERE con.constraint_type = 'P' AND con.owner = :1 AND con.table_name = :2`
		pkArgs = []any{owner, table}
	} else {
		pkQuery = `SELECT cc.column_name
FROM user_constraints con
JOIN user_cons_columns cc ON cc.constraint_name = con.constraint_name
WHERE con.constraint_type = 'P' AND con.table_name = :1`
		pkArgs = []any{table}
	}
	pkRows, err := db.QueryContext(ctx, pkQuery, pkArgs...)
	if err != nil {
		return nil, err
	}
	defer pkRows.Close()
	pkSet := map[string]bool{}
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

	var q string
	var qArgs []any
	if owner != "" {
		q = `SELECT col.column_name,
       col.data_type || CASE WHEN col.data_precision IS NOT NULL
            THEN '(' || col.data_precision || ',' || NVL(col.data_scale,0) || ')'
            WHEN col.char_length > 0 THEN '(' || col.char_length || ')'
            ELSE '' END,
       CASE WHEN col.nullable = 'Y' THEN 1 ELSE 0 END,
       COALESCE(cm.comments, ' '),
       col.data_default
FROM all_tab_columns col
LEFT JOIN all_col_comments cm ON cm.owner = col.owner
     AND cm.table_name = col.table_name AND cm.column_name = col.column_name
WHERE col.owner = :1 AND col.table_name = :2
ORDER BY col.column_id`
		qArgs = []any{owner, table}
	} else {
		q = `SELECT col.column_name,
       col.data_type || CASE WHEN col.data_precision IS NOT NULL
            THEN '(' || col.data_precision || ',' || NVL(col.data_scale,0) || ')'
            WHEN col.char_length > 0 THEN '(' || col.char_length || ')'
            ELSE '' END,
       CASE WHEN col.nullable = 'Y' THEN 1 ELSE 0 END,
       COALESCE(cm.comments, ' '),
       col.data_default
FROM user_tab_columns col
LEFT JOIN user_col_comments cm ON cm.table_name = col.table_name AND cm.column_name = col.column_name
WHERE col.table_name = :1
ORDER BY col.column_id`
		qArgs = []any{table}
	}
	rows, err := db.QueryContext(ctx, q, qArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ColumnMeta
	for rows.Next() {
		var m ColumnMeta
		var dft sql.NullString
		// godror 数字以 godror.Number 返回，无法直接 Scan 进 bool，先收 int64 再转
		var nullable int64
		if err := rows.Scan(&m.Name, &m.DataType, &nullable, &m.Comment, &dft); err != nil {
			return nil, err
		}
		m.Nullable = nullable == 1
		if dft.Valid {
			m.Default = &dft.String
		}
		m.IsPK = pkSet[m.Name]
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// 表有字段才正常；空结果 = 表不可见（不存在/无权限/引号大小写名）
	if len(out) == 0 {
		full := table
		if owner != "" {
			full = owner + "." + table
		}
		return nil, fmt.Errorf("未找到表 %s 的字段：表不存在、当前用户无 SELECT 权限、或表名以引号创建需精确匹配", full)
	}
	return out, nil
}

func (OracleDriverCGO) QuoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(strings.ToUpper(name), `"`, `""`) + `"`
}

func (OracleDriverCGO) Rebind(query string) string { return query } // Oracle 原生 :name

// Paginate 默认分页：12c+ 的 OFFSET/FETCH（未知版本也按此处理）
func (OracleDriverCGO) Paginate(query string, offset, limit int) string {
	return fmt.Sprintf("%s OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", query, offset, limit)
}

// PaginateFor 按服务器版本选择分页写法（11g 及以下降级 ROWNUM）
func (OracleDriverCGO) PaginateFor(version string, query string, offset, limit int) string {
	return OraclePaginate(version, query, offset, limit)
}

// ColumnComments 按列名批量查询当前用户下的字段注释（展示列显示名的默认值）。
// 同名列可能来自多张表，取 MAX(comments)（任一非空注释即可）。
func (OracleDriverCGO) ColumnComments(ctx context.Context, db *sql.DB, cols []string) (map[string]string, error) {
	out := map[string]string{}
	// 去重 + 仅保留合法标识符（防注入）
	uniq := make([]string, 0, len(cols))
	seen := map[string]bool{}
	for _, c := range cols {
		if err := ValidateIdentifier(c); err != nil {
			continue
		}
		u := strings.ToUpper(c)
		if !seen[u] {
			seen[u] = true
			uniq = append(uniq, u)
		}
	}
	if len(uniq) == 0 {
		return out, nil
	}
	ph := make([]string, len(uniq))
	args := make([]any, len(uniq))
	for i, c := range uniq {
		ph[i] = fmt.Sprintf(":%d", i+1)
		args[i] = c
	}
	q := "SELECT column_name, MAX(comments) FROM user_col_comments WHERE column_name IN (" +
		strings.Join(ph, ",") + ") GROUP BY column_name"
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

// PaginateRownum 已移至 oracle_common.go（两种实现共用）

// 编译期校验：确保 CGO 实现与接口一致
var _ Driver = OracleDriverCGO{}
var _ ColumnCommentProvider = OracleDriverCGO{}
