// Package datasource 多数据库驱动适配层。
// 各数据库差异（元数据 SQL、分页方言、占位符）由 Driver 实现封装，
// 引擎层只依赖本包接口，新增数据库类型只需实现 Driver 并注册。
package datasource

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// TableMeta 表元数据
type TableMeta struct {
	Name      string `json:"name"`
	Comment   string `json:"comment"`
	TableType string `json:"tableType"` // TABLE | VIEW
}

// ColumnMeta 字段元数据
type ColumnMeta struct {
	Name     string  `json:"name"`
	DataType string  `json:"dataType"`
	Nullable bool    `json:"nullable"`
	Default  *string `json:"default"`
	IsPK     bool    `json:"isPk"`
	Comment  string  `json:"comment"`
}

// ConnConfig 连接配置（由 model.Datasource 转换而来，密码为明文，仅在内存中短暂存在）
type ConnConfig struct {
	Host           string
	Port           int
	ServiceName    string // 库名 / Oracle 服务名 / SQLite 文件路径
	Username       string
	Password       string
	ExtraConn      string // Oracle TNS 直连串等
	PoolMaxOpen    int
	PoolMaxIdle    int
	PoolMaxLifeSec int
}

// Driver 数据库驱动适配接口
type Driver interface {
	// BuildDSN 根据连接配置构建驱动 DSN
	BuildDSN(cfg ConnConfig) (string, error)
	// Open 建立连接并配置连接池
	Open(dsn string, cfg ConnConfig) (*sql.DB, error)
	// ServerVersion 返回数据库版本描述
	ServerVersion(ctx context.Context, db *sql.DB) (string, error)
	// ListTables 表（含视图）清单，keyword 为空时返回全部
	ListTables(ctx context.Context, db *sql.DB, keyword string) ([]TableMeta, error)
	// ListColumns 指定表的字段清单
	ListColumns(ctx context.Context, db *sql.DB, table string) ([]ColumnMeta, error)
	// QuoteIdentifier 标识符转义（防注入）
	QuoteIdentifier(name string) string
	// Rebind 将 SQL 中的 :name 占位符转换为目标库占位符（Oracle 直通返回）
	Rebind(query string) string
	// Paginate 分页包装，limit/offset 为正整数（防注入：仅接受 int）
	Paginate(query string, offset, limit int) string
}

// 命名占位符匹配：:name 形式（名称规则同 Go 标识符）
var namedParamRe = regexp.MustCompile(`:[a-zA-Z_][a-zA-Z0-9_]*`)

// sqlSegment SQL 片段：inString 标记是否处于单引号字符串字面量内，
// inComment 标记是否处于 -- 行注释或 /* */ 块注释内
type sqlSegment struct {
	text      string
	inString  bool
	inComment bool
}

// splitSQLSegments 按单引号字符串字面量与注释切分 SQL（处理 '' 转义）。
// 所有片段拼接后与原 SQL 完全一致；起始引号随前段（字面量外），结束引号随本段（字面量内）。
// 用途：命名参数解析/替换必须跳过字面量与注释——
//  1. 字面量：Oracle 日期格式 'YYYY-MM-DD HH24:MI:SS' 中的 :MI/:SS 不是参数；
//  2. 注释：`-- 旧写法 :MAT_STATUS` 这类注释里的占位符不会被执行，
//     若被当成参数会给驱动多传一个绑定值，导致绑定位次错位（报绑定变量相关错误）。
func splitSQLSegments(sql string) []sqlSegment {
	var segs []sqlSegment
	var sb strings.Builder
	inStr, inLineCmt, inBlockCmt := false, false, false
	isComment := func() bool { return inLineCmt || inBlockCmt }
	// flush 用「当前状态」收尾：调用方必须在状态切换前调用，
	// 这样开引号/注释符算前一段，闭引号/注释尾算后一段
	flush := func() {
		if sb.Len() > 0 {
			segs = append(segs, sqlSegment{text: sb.String(), inString: inStr, inComment: isComment()})
			sb.Reset()
		}
	}
	for i := 0; i < len(sql); i++ {
		ch := sql[i]

		if inLineCmt { // 行注释：到换行为止
			sb.WriteByte(ch)
			if ch == '\n' {
				flush()
				inLineCmt = false
			}
			continue
		}
		if inBlockCmt { // 块注释：到 */ 为止
			sb.WriteByte(ch)
			if ch == '/' && i > 0 && sql[i-1] == '*' {
				flush()
				inBlockCmt = false
			}
			continue
		}
		if inStr { // 字面量：处理 '' 转义
			sb.WriteByte(ch)
			if ch == '\'' {
				if i+1 < len(sql) && sql[i+1] == '\'' {
					sb.WriteByte('\'')
					i++
					continue
				}
				flush()
				inStr = false
			}
			continue
		}

		// 以下为非字面量、非注释状态
		if ch == '-' && i+1 < len(sql) && sql[i+1] == '-' {
			sb.WriteByte(ch)
			flush()
			inLineCmt = true
			continue
		}
		if ch == '/' && i+1 < len(sql) && sql[i+1] == '*' {
			sb.WriteByte(ch)
			flush()
			inBlockCmt = true
			continue
		}
		if ch == '\'' {
			sb.WriteByte(ch)
			flush()
			inStr = true
			continue
		}
		sb.WriteByte(ch)
	}
	flush()
	return segs
}

// MapOutsideStrings 仅对「字符串字面量与注释以外」的 SQL 片段执行 fn
// （逐段调用，拼接结果与原 SQL 等价）
func MapOutsideStrings(sql string, fn func(string) string) string {
	var b strings.Builder
	for _, seg := range splitSQLSegments(sql) {
		if seg.inString || seg.inComment {
			b.WriteString(seg.text)
		} else {
			b.WriteString(fn(seg.text))
		}
	}
	return b.String()
}

// MaskStrings 将 SQL 中的字符串字面量替换为等长空格（保留位置与整体长度），
// 便于对结果做正则分析时跳过字面量内容（如日期格式串、URL 等）。
func MaskStrings(sql string) string {
	var b strings.Builder
	for _, seg := range splitSQLSegments(sql) {
		if seg.inString {
			b.WriteString(strings.Repeat(" ", len(seg.text)))
		} else {
			b.WriteString(seg.text)
		}
	}
	return b.String()
}

// MaskComments 将 SQL 中的注释内容替换为等长空白（保留换行与长度），便于日志与诊断。
func MaskComments(sql string) string {
	var b strings.Builder
	for _, seg := range splitSQLSegments(sql) {
		if seg.inComment {
			b.WriteString(strings.Repeat(" ", len(seg.text)))
		} else {
			b.WriteString(seg.text)
		}
	}
	return b.String()
}

// ApplyPool 按配置设置连接池参数（含默认值）
func ApplyPool(db *sql.DB, cfg ConnConfig) {
	maxOpen := cfg.PoolMaxOpen
	if maxOpen <= 0 {
		maxOpen = 20
	}
	maxIdle := cfg.PoolMaxIdle
	if maxIdle <= 0 {
		maxIdle = 5
	}
	life := cfg.PoolMaxLifeSec
	if life <= 0 {
		life = 3600
	}
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(time.Duration(life) * time.Second)
}

// RebindQuestion 将 :name 占位符按出现顺序转换为 ? （MySQL/SQLite 等问号方言共用）。
// 跳过字符串字面量与注释，避免破坏 'HH24:MI' 这类内容或把注释里的 :name 变成多余占位符
func RebindQuestion(query string) string {
	return MapOutsideStrings(query, func(s string) string {
		return namedParamRe.ReplaceAllString(s, "?")
	})
}

// ExtractParamNames 提取 SQL 中按顺序出现的命名占位符。
// 跳过字符串字面量与注释：前者如 Oracle 日期格式 'YYYY-MM-DD HH24:MI:SS' 里的 :MI/:SS，
// 后者如 '-- 旧条件 :MAT_STATUS'——注释不会执行，算进去会导致绑定值多传、位次错位
func ExtractParamNames(query string) []string {
	var names []string
	for _, seg := range splitSQLSegments(query) {
		if seg.inString || seg.inComment {
			continue
		}
		for _, m := range namedParamRe.FindAllString(seg.text, -1) {
			names = append(names, strings.TrimPrefix(m, ":"))
		}
	}
	return names
}

// ValidateIdentifier 校验标识符合法性（字母数字下划线），防注入的第一道防线
func ValidateIdentifier(name string) error {
	if name == "" {
		return fmt.Errorf("标识符不能为空")
	}
	if len(name) > 128 {
		return fmt.Errorf("标识符过长")
	}
	if !regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_$#]*$`).MatchString(name) {
		return fmt.Errorf("标识符包含非法字符: %s", name)
	}
	return nil
}

// ValidateColumnName 校验结果集列名（自动装配过滤用）：允许中文等任意字符，
// 但禁止双引号/点号/空白边界/换行——列名会经 QuoteIdentifier 双引号包裹后拼入 SQL，
// 禁掉引号与点号保证转义后仍是单一标识符，杜绝注入。
func ValidateColumnName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("列名不能为空")
	}
	if len(name) > 128 {
		return fmt.Errorf("列名过长")
	}
	if name != strings.TrimSpace(name) {
		return fmt.Errorf("列名首尾不能有空格: %q", name)
	}
	if strings.ContainsAny(name, "\".\n\r\t;") {
		return fmt.Errorf("列名不允许包含双引号、点号、分号等特殊字符: %s", name)
	}
	return nil
}

// ValidateTableRef 校验表引用：允许「表名」或跨模式的「模式.表名」（如 FGPESE0.SYS_DICT_TYPE）。
// 表名在查询中原样拼接（不做引号包裹），因此这里逐段校验是防注入的关键；
// 至多一个点号，且每段都必须是合法标识符。
func ValidateTableRef(name string) error {
	if name == "" {
		return fmt.Errorf("表名不能为空")
	}
	if len(name) > 257 { // 模式.表名：两段各最长 128 + 点号
		return fmt.Errorf("表名过长")
	}
	parts := strings.Split(name, ".")
	if len(parts) > 2 {
		return fmt.Errorf("表名仅支持「表名」或「模式.表名」格式: %s", name)
	}
	for _, p := range parts {
		if err := ValidateIdentifier(p); err != nil {
			return fmt.Errorf("表名段非法: %w", err)
		}
	}
	return nil
}

// ColumnCommentProvider 驱动可选实现：按结果列名批量查询字段注释。
// 用于展示列显示名的默认值（取字段注释）；未实现的驱动回退为列名本身。
type ColumnCommentProvider interface {
	ColumnComments(ctx context.Context, db *sql.DB, cols []string) (map[string]string, error)
}

// CommentsFor 若驱动支持注释查询则返回 map（key 为小写列名），不支持或失败返回 nil（不影响主流程）
func CommentsFor(ctx context.Context, d Driver, db *sql.DB, cols []string) map[string]string {
	p, ok := d.(ColumnCommentProvider)
	if !ok || len(cols) == 0 {
		return nil
	}
	m, err := p.ColumnComments(ctx, db, cols)
	if err != nil {
		return nil
	}
	return m
}

// ---------- 注册表 ----------

var registry = map[string]Driver{}

// Register 注册驱动实现（各驱动文件 init 中调用）
func Register(dbType string, d Driver) {
	registry[strings.ToLower(dbType)] = d
}

// Get 获取指定类型的驱动
func Get(dbType string) (Driver, error) {
	d, ok := registry[strings.ToLower(dbType)]
	if !ok {
		return nil, fmt.Errorf("不支持的数据库类型: %s", dbType)
	}
	return d, nil
}

// SupportedTypes 已注册的数据库类型清单
func SupportedTypes() []string {
	types := make([]string, 0, len(registry))
	for k := range registry {
		types = append(types, k)
	}
	sort.Strings(types)
	return types
}
