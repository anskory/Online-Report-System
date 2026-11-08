package datasource

import (
	"fmt"
	"regexp"
	"strconv"
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
