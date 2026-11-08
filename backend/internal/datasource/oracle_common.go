package datasource

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 本文件放 Oracle 驱动的两个实现共享的常量/工具，无 build tag，
// 保证 `go build`（纯 Go）与 `go build -tags oracle`（CGO/godror）都能编译。

// oracleSysSchemas Oracle 内置系统模式，扫描表清单时排除，避免噪声
const oracleSysSchemas = `'AUDSYS','CTXSYS','DBSNMP','DVSYS','EXFSYS','GSMADMIN_INTERNAL',` +
	`'LBACSYS','MDDATA','MDSYS','OLAPSYS','ORACLE_OCM','ORDDATA','ORDPLATES','ORDSYS',` +
	`'OUTLN','OJVMSYS','APPQOSSYS','SYSAUX','SYS','SYSTEM','WMSYS','XDB','XOQ'`

// PaginateRownum Oracle 11g 降级分页模板（12c 以下无 OFFSET/FETCH，需三层嵌套 ROWNUM）
func PaginateRownum(query string, offset, limit int) string {
	return fmt.Sprintf(`SELECT * FROM (
  SELECT t_.*, ROWNUM rn_ FROM ( %s ) t_ WHERE ROWNUM <= %d
) WHERE rn_ > %d`, query, offset+limit, offset)
}

var (
	// 从 banner 提取主版本号："... Release 12.2.0.1.0 ..." / "... Release 11.2.0.4.0 ..."
	oracleVerRe = regexp.MustCompile(`(?i)release\s+(\d+)\.`)
	// 兜底：形如 x.y.z.w 的四段版本号（ServerVersion 可能不是标准 banner）
	oracleVerFallbackRe = regexp.MustCompile(`\b(\d+)\.\d+\.\d+\.\d+\b`)
)

// OracleMajorVersion 从版本描述中解析 Oracle 主版本号；解析失败返回 0（调用方按最新语法处理）
func OracleMajorVersion(version string) int {
	for _, re := range []*regexp.Regexp{oracleVerRe, oracleVerFallbackRe} {
		if m := re.FindStringSubmatch(version); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n
			}
		}
	}
	return 0
}

// OraclePaginate 按服务器版本选择分页写法：
//   - 主版本 >= 12（或未知）：OFFSET/FETCH（12c+ 语法）
//   - 主版本 1~11：ROWNUM 三层嵌套（11g 及以下）
func OraclePaginate(version string, query string, offset, limit int) string {
	if v := OracleMajorVersion(version); v > 0 && v < 12 {
		return PaginateRownum(query, offset, limit)
	}
	return fmt.Sprintf("%s OFFSET %d ROWS FETCH NEXT %d ROWS ONLY", query, offset, limit)
}

// oracleListIndexes Oracle 索引清单（两个驱动共用一份 SQL）：
// 绑定位次 :1=owner（空串表示当前用户）、:2=表名。
// 主键索引用 all_constraints 反查，比按索引名猜（PK_xxx / SYS_Cxxx）可靠。
func oracleListIndexes(ctx context.Context, db *sql.DB, table string) ([]IndexMeta, error) {
	table = strings.ToUpper(strings.TrimSpace(table))
	owner := ""
	if i := strings.Index(table, "."); i >= 0 {
		owner, table = table[:i], table[i+1:]
	}
	for _, s := range []string{owner, table} {
		if s == "" {
			continue
		}
		if err := ValidateIdentifier(s); err != nil {
			return nil, err
		}
	}
	const q = `SELECT i.index_name, c.column_name,
       CASE WHEN i.uniqueness = 'UNIQUE' THEN 0 ELSE 1 END AS non_unique,
       CASE WHEN EXISTS (
              SELECT 1 FROM all_constraints k
               WHERE k.owner = i.table_owner AND k.table_name = i.table_name
                 AND k.constraint_type = 'P' AND k.index_name = i.index_name
            ) THEN 1 ELSE 0 END AS is_primary
FROM all_indexes i
JOIN all_ind_columns c ON c.index_owner = i.owner AND c.index_name = i.index_name
WHERE i.table_owner = COALESCE(NULLIF(:1, ''), USER) AND i.table_name = :2
ORDER BY i.index_name, c.column_position`
	rows, err := db.QueryContext(ctx, q, owner, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []IndexMeta{}
	idx := map[string]int{}
	for rows.Next() {
		var name string
		var col sql.NullString
		var nonUnique, isPrimary int
		if err := rows.Scan(&name, &col, &nonUnique, &isPrimary); err != nil {
			return nil, err
		}
		i, ok := idx[name]
		if !ok {
			out = append(out, IndexMeta{Name: name, Unique: nonUnique == 0, Primary: isPrimary == 1})
			i = len(out) - 1
			idx[name] = i
		}
		// 函数索引的 column_name 为 NULL，跳过不追加空列
		if col.Valid && col.String != "" {
			out[i].Columns = append(out[i].Columns, col.String)
		}
	}
	return out, rows.Err()
}
