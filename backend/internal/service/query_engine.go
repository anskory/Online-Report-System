package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ce/report-system/backend/internal/config"
	"github.com/ce/report-system/backend/internal/datasource"
	"github.com/ce/report-system/backend/internal/model"
	"gorm.io/gorm"
)

// ---------- SQL 只读防护（设计文档 §5.4） ----------

var forbiddenSQLRe = regexp.MustCompile(`(?is)\b(insert|update|delete|merge|drop|alter|truncate|create|grant|revoke|exec|execute|call|replace|rename|comment|lock|analyze|audit|noaudit|flashback|purge|shutdown|startup|attach|detach|pragma|set)\b`)
var forUpdateRe = regexp.MustCompile(`(?is)\bfor\s+update\b`)

// stripSQLNoise 去除字符串字面量/标识符引用/注释，保留关键字骨架（字符扫描状态机）
func stripSQLNoise(sqlText string) string {
	var b strings.Builder
	state := 0 // 0 normal, 1 single, 2 double, 3 backtick, 4 line-comment, 5 block-comment
	for i := 0; i < len(sqlText); i++ {
		ch := sqlText[i]
		next := byte(0)
		if i+1 < len(sqlText) {
			next = sqlText[i+1]
		}
		switch state {
		case 0:
			switch {
			case ch == '\'':
				state = 1
				b.WriteString("''")
			case ch == '"':
				state = 2
				b.WriteString(`""`)
			case ch == '`':
				state = 3
				b.WriteString("``")
			case ch == '-' && next == '-':
				state = 4
				i++
			case ch == '/' && next == '*':
				state = 5
				i++
			case ch == ';':
				b.WriteString("; ")
			default:
				b.WriteByte(ch)
			}
		case 1:
			if ch == '\'' {
				if next == '\'' { // 转义的引号，跳过
					i++
				} else {
					state = 0
				}
			}
		case 2:
			if ch == '"' {
				if next == '"' {
					i++
				} else {
					state = 0
				}
			}
		case 3:
			if ch == '`' {
				state = 0
			}
		case 4:
			if ch == '\n' {
				state = 0
				b.WriteByte('\n')
			}
		case 5:
			if ch == '*' && next == '/' {
				state = 0
				i++
			}
		}
	}
	return b.String()
}

// validateSQLText 报表 SQL 只读校验：黑名单关键字 + 多语句 + FOR UPDATE
func validateSQLText(sqlText string) error {
	if strings.TrimSpace(sqlText) == "" {
		return ErrSQLRequired
	}
	cleaned := stripSQLNoise(sqlText)
	if forbiddenSQLRe.MatchString(cleaned) {
		return errors.New("SQL 包含非查询语句（只允许 SELECT 查询）")
	}
	if forUpdateRe.MatchString(cleaned) {
		return errors.New("SQL 不允许使用 FOR UPDATE")
	}
	// 多语句：分号分隔后最多一个非空片段
	segments := 0
	for _, seg := range strings.Split(cleaned, ";") {
		if strings.TrimSpace(seg) != "" {
			segments++
		}
	}
	if segments > 1 {
		return errors.New("只允许单条查询语句")
	}
	return nil
}

// validateParamsInSQL 保存时校验条件参数与 SQL 的关系。
// 两种装配模式均合法：① SQL 中写 :name 占位符（range 需 :name_1/:name_2）；
// ② 未写占位符 → 自动装配模式，参数名须为合法列名（作为结果集列过滤）
func validateParamsInSQL(sqlText string, params []model.ReportParam) error {
	cleaned := stripSQLNoise(sqlText)
	for _, p := range params {
		if strings.TrimSpace(p.ParamName) == "" {
			return fmt.Errorf("条件「%s」未填写参数名", p.Label)
		}
		inSQL := regexp.MustCompile(`:` + regexp.QuoteMeta(p.ParamName) + `\b`).MatchString(cleaned)
		if !inSQL {
			// 自动装配模式：参数名作绑定变量（须为英文标识符）；
			// 过滤列名优先取 ColumnName（支持中文），未填则回退参数名
			if err := datasource.ValidateIdentifier(p.ParamName); err != nil {
				return fmt.Errorf("条件「%s」未在 SQL 中书写占位符，自动装配要求参数名为英文标识符（%v）；如需按中文列名过滤，请填写该条件的高级「列名」", p.Label, err)
			}
			if strings.TrimSpace(p.ColumnName) != "" {
				if err := datasource.ValidateColumnName(p.ColumnName); err != nil {
					return fmt.Errorf("条件「%s」的列名非法: %w", p.Label, err)
				}
			}
			continue
		}
		if p.MatchType == "range" {
			if !regexp.MustCompile(`:` + regexp.QuoteMeta(p.ParamName) + `_1\b`).MatchString(cleaned) ||
				!regexp.MustCompile(`:` + regexp.QuoteMeta(p.ParamName) + `_2\b`).MatchString(cleaned) {
				return fmt.Errorf("参数 %s 为范围类型，SQL 中需包含 :%s_1 与 :%s_2", p.ParamName, p.ParamName, p.ParamName)
			}
		}
	}
	return nil
}

// ---------- 查询引擎 ----------

// QueryEngine 查询执行流水线（设计文档 §5.3）
type QueryEngine struct {
	ds   *DatasourceService
	rpt  *ReportService
	dict *DictService

	// 统计总数（SELECT COUNT(*)）治理：大表上 COUNT 往往比取一页数据慢一个数量级，
	// 放任它同步跑会把整个查询拖到超时，前端就只能一直转圈。这里给它独立预算 + 结果缓存。
	countTimeout time.Duration // 单次 COUNT 的超时上限（<=0 时退化为跟随报表超时）
	countTTL     time.Duration // COUNT 结果缓存有效期（<=0 表示不缓存）
	countMu      sync.Mutex
	counts       map[string]countCacheEntry
}

// countCacheEntry COUNT 缓存项
type countCacheEntry struct {
	total   int64
	expires time.Time
}

// NewQueryEngine 构造查询引擎。
// cfg 为零值时按默认值（COUNT 5 秒超时 / 30 秒缓存）运行，便于测试与老调用方。
func NewQueryEngine(ds *DatasourceService, rpt *ReportService, dict *DictService, cfg config.Query) *QueryEngine {
	e := &QueryEngine{ds: ds, rpt: rpt, dict: dict, counts: map[string]countCacheEntry{}}
	if cfg.CountTimeoutSec > 0 {
		e.countTimeout = time.Duration(cfg.CountTimeoutSec) * time.Second
	} else {
		e.countTimeout = 5 * time.Second
	}
	if cfg.CountCacheSec > 0 {
		e.countTTL = time.Duration(cfg.CountCacheSec) * time.Second
	}
	return e
}

// countGet 读 COUNT 缓存（过期即失效）
func (e *QueryEngine) countGet(key string) (int64, bool) {
	if e.countTTL <= 0 || key == "" {
		return 0, false
	}
	e.countMu.Lock()
	defer e.countMu.Unlock()
	it, ok := e.counts[key]
	if !ok || time.Now().After(it.expires) {
		if ok {
			delete(e.counts, key)
		}
		return 0, false
	}
	return it.total, true
}

// countPut 写 COUNT 缓存；顺带清理过期项，避免 map 无限增长
func (e *QueryEngine) countPut(key string, total int64) {
	if e.countTTL <= 0 || key == "" || total < 0 {
		return
	}
	now := time.Now()
	e.countMu.Lock()
	defer e.countMu.Unlock()
	if len(e.counts) > 256 {
		for k, v := range e.counts {
			if now.After(v.expires) {
				delete(e.counts, k)
			}
		}
	}
	e.counts[key] = countCacheEntry{total: total, expires: now.Add(e.countTTL)}
}

// ColumnOut 输出列元数据
type ColumnOut struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	Type       string `json:"type"`
	Width      int    `json:"width"`
	FmtType    string `json:"fmtType"`
	FmtPattern string `json:"fmtPattern"`
	DictCode   string `json:"dictCode,omitempty"`  // 绑定字典编码（列翻译）
	DictType   string `json:"dictType,omitempty"`  // 字典类型筛选
	ListFmt    string `json:"listFmt,omitempty"`   // 列表显示：value/label/both
	ExportFmt  string `json:"exportFmt,omitempty"` // 导出显示：value/label/both
}

// QueryResult 查询结果
type QueryResult struct {
	Columns  []ColumnOut `json:"columns"`
	Rows     [][]any     `json:"rows"`
	Total    int64       `json:"total"` // 总行数；-1 = 统计超时导致总数未知（前端按近似值翻页）
	Page     int         `json:"page"`
	PageSize int         `json:"pageSize"`
}

// 引擎级业务错误
var (
	ErrParamRequired = errors.New("必填查询条件未填写")
	ErrParamInvalid  = errors.New("查询条件值非法")
	ErrSortInvalid   = errors.New("排序字段不在结果列白名单中")
	ErrParamMissing  = errors.New("SQL 中的参数未配置查询条件")
)

// hintORA 为常见 Oracle 错误码附加排查建议（不影响其他数据库的错误透传）。
// ORA-00942 时自动解析 SQL 引用的表并核对可见性，直接指出不可见的对象。
func hintORA(ctx context.Context, db *sql.DB, dbType, sqlText string, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "ORA-00942"):
		if missing := diagnoseMissingTables(ctx, db, dbType, sqlText); missing != "" {
			return fmt.Errorf("%w（ORA-00942：以下对象当前连接用户不可见：%s——需让 DBA 授予 SELECT 权限；授权后 SQL 中跨模式表需写 OWNER.TABLE 形式，或建同义词省略前缀）", err, missing)
		}
		return fmt.Errorf("%w（ORA-00942：当前连接用户看不到某张表——检查表名拼写、是否需要加模式前缀 OWNER.TABLE、或让 DBA 授予 SELECT 权限/建同义词）", err)
	case strings.Contains(msg, "ORA-00904"):
		return fmt.Errorf("%w（ORA-00904：字段名不存在、拼写有误或大小写加引号不匹配）", err)
	case strings.Contains(msg, "ORA-00911"):
		return fmt.Errorf("%w（ORA-00911：SQL 有非法字符，常见为末尾多了分号）", err)
	case strings.Contains(msg, "ORA-01756"):
		return fmt.Errorf("%w（ORA-01756：引号不配对）", err)
	case strings.Contains(msg, "ORA-01017"):
		return fmt.Errorf("%w（ORA-01017：用户名/密码错误）", err)
	case strings.Contains(msg, "ORA-12154"):
		return fmt.Errorf("%w（ORA-12154：无法解析连接标识符/服务名）", err)
	case strings.Contains(msg, "ORA-12514"):
		return fmt.Errorf("%w（ORA-12514：监听器不认识该服务名，检查服务名是否正确）", err)
	case strings.Contains(msg, "ORA-01036"):
		return fmt.Errorf("%w（ORA-01036：绑定变量名/个数与 SQL 不匹配——检查条件参数名是否为合法标识符（字母开头、仅含字母/数字/下划线），以及 SQL 注释里是否残留 :占位符）", err)
	case strings.Contains(msg, "ORA-01008"):
		return fmt.Errorf("%w（ORA-01008：SQL 中存在未配置查询条件的 :占位符，或占位符被字符串/注释包裹导致参数个数不符）", err)
	case strings.Contains(msg, "ORA-01722"):
		return fmt.Errorf("%w（ORA-01722：值类型与列类型不匹配，如字符值写入数字列；检查该条件是否绑错了列/值）", err)
	}
	return err
}

// tableRefRe 匹配 FROM/JOIN 关键字后的表引用（可含 OWNER. 前缀）
var tableRefRe = regexp.MustCompile(`(?i)\b(?:from|join)\s+([a-zA-Z_][a-zA-Z0-9_$#]*)(\.[a-zA-Z_][a-zA-Z0-9_$#]*)?`)

// extractTableRefs 提取 SQL 引用的表/视图名（大写去重，跳过字符串字面量与括号子查询）
func extractTableRefs(sqlText string) []string {
	masked := datasource.MaskStrings(sqlText)
	matches := tableRefRe.FindAllStringSubmatch(masked, -1)
	seen := map[string]bool{}
	var out []string
	for _, m := range matches {
		name := strings.ToUpper(m[1] + m[2])
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// diagnoseMissingTables ORA-00942 自动诊断（仅 Oracle）：解析 SQL 引用的对象，
// 逐一核对 all_objects（表/视图/物化视图）与同义词，返回不可见对象清单。
// 诊断失败或无发现时返回空串，不干扰原始错误。
func diagnoseMissingTables(ctx context.Context, db *sql.DB, dbType, sqlText string) string {
	if dbType != "oracle" || db == nil {
		return ""
	}
	refs := extractTableRefs(sqlText)
	if len(refs) == 0 || len(refs) > 100 {
		return ""
	}
	diagCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// 名称绑定（含 OWNER.TABLE 拆分）
	var plainNames, qualOwners, qualTables []string
	for _, r := range refs {
		if i := strings.Index(r, "."); i >= 0 {
			qualOwners = append(qualOwners, r[:i])
			qualTables = append(qualTables, r[i+1:])
		} else {
			plainNames = append(plainNames, r)
		}
	}

	// 1. 可见的 (owner, name) 集合：当前用户自己的对象 + 已获权限的跨模式对象
	binds := func(names []string) (string, []any) {
		ph := make([]string, len(names))
		args := make([]any, len(names))
		for i, n := range names {
			ph[i] = fmt.Sprintf(":%d", i+1)
			args[i] = n
		}
		return strings.Join(ph, ","), args
	}

	visible := map[string]bool{} // key: "OWNER.NAME"
	if len(plainNames) > 0 {
		ph, args := binds(plainNames)
		rows, err := db.QueryContext(diagCtx, `SELECT owner, object_name FROM all_objects
WHERE object_type IN ('TABLE','VIEW','MATERIALIZED VIEW') AND object_name IN (`+ph+`)`, args...)
		if err != nil {
			return ""
		}
		for rows.Next() {
			var owner, name string
			if rows.Scan(&owner, &name) == nil {
				visible[owner+"."+name] = true
			}
		}
		rows.Close()
		// 裸表名还可能通过私有/公共同义词解析
		synRows, err := db.QueryContext(diagCtx, `SELECT owner, synonym_name FROM all_synonyms
WHERE synonym_name IN (`+ph+`)`, args...)
		if err == nil {
			for synRows.Next() {
				var owner, name string
				if synRows.Scan(&owner, &name) == nil {
					visible[owner+"."+name] = true // owner=USER 私有同义词 / owner=PUBLIC 公共同义词
				}
			}
			synRows.Close()
		}
	}
	if len(qualTables) > 0 {
		phO, argsO := binds(qualOwners)
		phT, argsT := binds(qualTables)
		rows, err := db.QueryContext(diagCtx, `SELECT owner, object_name FROM all_objects
WHERE object_type IN ('TABLE','VIEW','MATERIALIZED VIEW') AND owner IN (`+phO+`) AND object_name IN (`+phT+`)`,
			append(argsO, argsT...)...)
		if err != nil {
			return ""
		}
		for rows.Next() {
			var owner, name string
			if rows.Scan(&owner, &name) == nil {
				visible[owner+"."+name] = true
			}
		}
		rows.Close()
	}

	// 2. 逐个判断可见性：裸名仅按「本模式对象 / PUBLIC 或私有同义词」解析；
	//    其他模式有同名对象不代表裸名可引用（必须 OWNER.TABLE 或同义词）
	var missing []string
	currentUser := ""
	_ = db.QueryRowContext(diagCtx, "SELECT USER FROM DUAL").Scan(&currentUser)
	for _, r := range refs {
		ok := false
		if i := strings.Index(r, "."); i >= 0 {
			ok = visible[r]
		} else {
			ok = visible[currentUser+"."+r] || visible["PUBLIC."+r]
		}
		if !ok {
			missing = append(missing, r)
		}
	}
	if len(missing) == 0 {
		return ""
	}
	return strings.Join(missing, ", ")
}

// Execute 执行报表查询：参数绑定 → 只读校验 → 方言转换 → 计数 → 分页取数
func (e *QueryEngine) Execute(ctx context.Context, reportID int64, input map[string]any, page, pageSize int, sortCol, sortDir string) (*QueryResult, error) {
	report, params, cols, err := e.rpt.GetFull(reportID)
	if err != nil {
		return nil, err
	}
	if input == nil {
		input = map[string]any{}
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}

	// 1. 参数绑定（含 IN/范围展开与动态默认值）
	expandedSQL, binds, err := buildBinds(report.SQLText, params, input)
	if err != nil {
		return nil, err
	}

	// 2. 只读校验（展开后的最终 SQL）
	if err := validateSQLText(expandedSQL); err != nil {
		return nil, err
	}

	// 3. 驱动适配
	dsRow, err := e.ds.Get(report.DatasourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDSNNotFound
		}
		return nil, err
	}
	driver, err := datasource.Get(dsRow.DbType)
	if err != nil {
		return nil, err
	}
	// dbVer：数据源版本描述（banner）。Oracle 用它选择分页方言（12c+ FETCH / 11g ROWNUM）
	db, dbVer, err := e.ds.GetPool(ctx, report.DatasourceID)
	if err != nil {
		return nil, err
	}

	// 自动装配：未在 SQL 中写占位符的查询条件包装为外层 WHERE（列查询开关场景）
	expandedSQL, err = autoWrapSQL(driver, params, binds, expandedSQL)
	if err != nil {
		return nil, err
	}

	// 有序绑定参数（?/$n/@p1 按出现顺序；Oracle 命名参数按出现顺序绑定）
	args, bindNames, err := orderedArgs(dsRow.DbType, expandedSQL, binds)
	if err != nil {
		return nil, err
	}

	timeout := report.QueryTimeout
	if timeout <= 0 {
		timeout = 30
	}

	// 4. 计数
	// 大表上 SELECT COUNT(*) FROM (原SQL) 常比取一页数据慢一个数量级，且报表条件一致时
	// 每翻一页都要重算一遍。这里：① 命中缓存直接复用；② 给 COUNT 独立且更短的超时预算；
	// ③ 超时降级为「总数未知」（total = -1），继续把本页数据返回，不让用户对着转圈干等。
	// 注意：只有「超时」才降级，真正的 SQL 错误照旧抛出（否则会掩盖报表配置错误）。
	countSQL := driver.Rebind("SELECT COUNT(*) FROM (" + expandedSQL + ") rpt_count")
	var total int64
	cacheKey := ""
	if e.countTTL > 0 {
		cacheKey = strconv.FormatInt(reportID, 10) + "|" + expandedSQL + "|" + fmt.Sprintf("%v", args)
	}
	if v, ok := e.countGet(cacheKey); ok {
		total = v
	} else {
		cntBudget := e.countTimeout
		if cntBudget <= 0 || cntBudget > time.Duration(timeout)*time.Second {
			cntBudget = time.Duration(timeout) * time.Second
		}
		cntCtx, cntCancel := context.WithTimeout(ctx, cntBudget)
		cntErr := db.QueryRowContext(cntCtx, countSQL, args...).Scan(&total)
		// 先判定超时再 cancel：cancel 之后 cntCtx.Err() 会被置为 Canceled，掩盖真正的 DeadlineExceeded
		timedOut := errors.Is(cntErr, context.DeadlineExceeded) || errors.Is(cntCtx.Err(), context.DeadlineExceeded)
		cntCancel()
		if cntErr != nil {
			if timedOut {
				log.Printf("[查询] 报表 %d 统计总数超过 %s，降级为总数未知（数据查询继续）", reportID, cntBudget)
				total = -1
			} else {
				return nil, requeryErr("统计总数", expandedSQL, bindNames, binds, hintORA(ctx, db, dsRow.DbType, expandedSQL, cntErr))
			}
		} else {
			e.countPut(cacheKey, total)
		}
	}

	// 5. 排序（白名单校验 + 外层包装，避免与原 SQL 的 ORDER BY 冲突）
	dataSQL := expandedSQL
	if sortCol != "" {
		if !columnAllowed(sortCol, cols) {
			return nil, ErrSortInvalid
		}
		dir := "ASC"
		if strings.EqualFold(sortDir, "desc") {
			dir = "DESC"
		}
		dataSQL = fmt.Sprintf("SELECT * FROM (%s) rpt_order ORDER BY %s %s", dataSQL, driver.QuoteIdentifier(sortCol), dir)
	} else if report.DefaultOrder != "" && columnAllowed(report.DefaultOrder, cols) {
		dataSQL = fmt.Sprintf("SELECT * FROM (%s) rpt_order ORDER BY %s", dataSQL, driver.QuoteIdentifier(report.DefaultOrder))
	}

	// 6. 分页 + 执行
	dataSQL = driver.Rebind(paginateSQL(driver, dbVer, dataSQL, (page-1)*pageSize, pageSize))
	qryCtx, qryCancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer qryCancel()
	rows, err := db.QueryContext(qryCtx, dataSQL, args...)
	if err != nil {
		return nil, requeryErr("查询执行", expandedSQL, bindNames, binds, hintORA(ctx, db, dsRow.DbType, expandedSQL, err))
	}
	defer rows.Close()

	resultCols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	resultTypes, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	typeByName := map[string]string{}
	for i, ct := range resultTypes {
		typeByName[resultCols[i]] = ct.DatabaseTypeName()
	}

	// 7. 输出列组装（配置列优先；未配置则全部输出）
	var outCols []ColumnOut
	var outIdx []int // 结果集列索引 → 输出顺序
	if len(cols) > 0 {
		indexByName := map[string]int{}
		for i, name := range resultCols {
			indexByName[name] = i
		}
		for _, c := range cols {
			if c.Hidden == 1 {
				continue
			}
			idx, ok := indexByName[c.ColName]
			if !ok {
				continue // 配置列不在结果集中，忽略
			}
			outCols = append(outCols, ColumnOut{
				Name: c.ColName, Label: c.Label, Type: typeByName[c.ColName],
				Width: c.Width, FmtType: c.FmtType, FmtPattern: c.FmtPattern,
				DictCode: c.DictCode, DictType: c.DictType,
				ListFmt: c.ListFmt, ExportFmt: c.ExportFmt,
			})
			outIdx = append(outIdx, idx)
		}
	}
	if len(outCols) == 0 {
		// 未配置展示列：显示名默认取数据库字段注释（驱动支持时）。
		// Oracle 驱动把无注释 COALESCE 成单个空格，trim 后为空（如部分视图）则回退字段名
		comments := datasource.CommentsFor(ctx, driver, db, resultCols)
		for i, name := range resultCols {
			lbl := strings.TrimSpace(comments[strings.ToLower(name)])
			if lbl == "" {
				lbl = name
			}
			outCols = append(outCols, ColumnOut{Name: name, Label: lbl, Type: typeByName[name]})
			outIdx = append(outIdx, i)
		}
	}

	// 8. 读取行数据（JSON 安全类型归一化）
	outRows := make([][]any, 0, pageSize)
	raw := make([]any, len(resultCols))
	ptrs := make([]any, len(resultCols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make([]any, len(outIdx))
		for oi, ri := range outIdx {
			row[oi] = normalizeValue(raw[ri])
		}
		outRows = append(outRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// 9. 列翻译：绑定字典的输出列按「列表显示」格式转换（value=不翻译 / label=标签 / both=[键值]标签）
	if err := e.translateOutRows(ctx, outCols, outRows, false); err != nil {
		return nil, err
	}

	return &QueryResult{Columns: outCols, Rows: outRows, Total: total, Page: page, PageSize: pageSize}, nil
}

// translateOutRows 依据列绑定字典翻译行数据（inPlace）。
// useExportFmt=true 时用导出格式，否则用列表格式；空格式按 label 处理，value=不翻译。
func (e *QueryEngine) translateOutRows(ctx context.Context, cols []ColumnOut, rows [][]any, useExportFmt bool) error {
	maps := map[string]map[string]string{} // 缓存 key: code|type
	trans := make([]func(string) string, len(cols))
	for i, c := range cols {
		if c.DictCode == "" {
			continue
		}
		f := c.ListFmt
		if useExportFmt {
			f = c.ExportFmt
		}
		if f == "" {
			f = "label"
		}
		if f == "value" {
			continue
		}
		key := c.DictCode + "|" + c.DictType
		m, ok := maps[key]
		if !ok {
			var err error
			m, err = e.dict.LabelMap(ctx, e.ds, c.DictCode, c.DictType)
			if err != nil {
				return fmt.Errorf("列 %s 字典翻译失败: %w", c.Name, err)
			}
			maps[key] = m
		}
		if f == "both" {
			trans[i] = func(k string) string {
				if lbl, ok := m[k]; ok {
					return "[" + k + "]" + lbl
				}
				return k
			}
		} else {
			trans[i] = func(k string) string {
				if lbl, ok := m[k]; ok {
					return lbl
				}
				return k
			}
		}
	}
	anyTrans := false
	for _, t := range trans {
		if t != nil {
			anyTrans = true
			break
		}
	}
	if !anyTrans {
		return nil
	}
	for _, row := range rows {
		for i, tr := range trans {
			if tr != nil && i < len(row) && row[i] != nil {
				// 查找键 trim：报表列若是 CHAR 类型会带尾部空格，不 trim 会导致翻译映射查不到（显示原键值）
				row[i] = tr(strings.TrimSpace(toString(row[i])))
			}
		}
	}
	return nil
}

// DictTranslators 为导出构建每列翻译函数（返回切片与 cols 下标对齐，无需翻译处为 nil）。
// 导出失败不做整体中断：字典加载失败时该列保留原值。
func (e *QueryEngine) DictTranslators(ctx context.Context, cols []ColumnOut) []func(any) any {
	out := make([]func(any) any, len(cols))
	for i, c := range cols {
		if c.DictCode == "" {
			continue
		}
		f := c.ExportFmt
		if f == "" {
			f = "label"
		}
		if f == "value" {
			continue
		}
		m, err := e.dict.LabelMap(ctx, e.ds, c.DictCode, c.DictType)
		if err != nil || len(m) == 0 {
			continue // 翻译不可用时保留原值，不阻塞导出
		}
		if f == "both" {
			out[i] = func(v any) any {
				k := toString(v)
				if lbl, ok := m[k]; ok {
					return "[" + k + "]" + lbl
				}
				return v
			}
		} else {
			out[i] = func(v any) any {
				if lbl, ok := m[toString(v)]; ok {
					return lbl
				}
				return v
			}
		}
	}
	return out
}

// PrepareExport 构建导出查询（从 offset 行起最多 maxRows 行），返回行集与输出列。
// poutIdx 回传列索引映射（第 i 个输出列在结果集中的原始下标）；调用方负责 rows.Close()。
// 导出不受单查询超时限制（由 maxRows 与行上限约束）。
func (e *QueryEngine) PrepareExport(ctx context.Context, reportID int64, input map[string]any, sortCol, sortDir string, offset, maxRows int64, poutIdx *[]int) (*sql.Rows, []ColumnOut, error) {
	report, params, cols, err := e.rpt.GetFull(reportID)
	if err != nil {
		return nil, nil, err
	}
	if input == nil {
		input = map[string]any{}
	}
	expandedSQL, binds, err := buildBinds(report.SQLText, params, input)
	if err != nil {
		return nil, nil, err
	}
	if err := validateSQLText(expandedSQL); err != nil {
		return nil, nil, err
	}
	dsRow, err := e.ds.Get(report.DatasourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, ErrDSNNotFound
		}
		return nil, nil, err
	}
	driver, err := datasource.Get(dsRow.DbType)
	if err != nil {
		return nil, nil, err
	}
	db, dbVer, err := e.ds.GetPool(ctx, report.DatasourceID)
	if err != nil {
		return nil, nil, err
	}
	// 自动装配：未在 SQL 中写占位符的查询条件包装为外层 WHERE（列查询开关场景）
	expandedSQL, err = autoWrapSQL(driver, params, binds, expandedSQL)
	if err != nil {
		return nil, nil, err
	}
	args, bindNames, err := orderedArgs(dsRow.DbType, expandedSQL, binds)
	if err != nil {
		return nil, nil, err
	}

	dataSQL := expandedSQL
	if sortCol != "" {
		if !columnAllowed(sortCol, cols) {
			return nil, nil, ErrSortInvalid
		}
		dir := "ASC"
		if strings.EqualFold(sortDir, "desc") {
			dir = "DESC"
		}
		dataSQL = fmt.Sprintf("SELECT * FROM (%s) rpt_order ORDER BY %s %s", dataSQL, driver.QuoteIdentifier(sortCol), dir)
	} else if report.DefaultOrder != "" && columnAllowed(report.DefaultOrder, cols) {
		dataSQL = fmt.Sprintf("SELECT * FROM (%s) rpt_order ORDER BY %s", dataSQL, driver.QuoteIdentifier(report.DefaultOrder))
	}
	if maxRows <= 0 {
		maxRows = 1000000
	}
	if offset < 0 {
		offset = 0
	}
	dataSQL = driver.Rebind(paginateSQL(driver, dbVer, dataSQL, int(offset), int(maxRows)))

	rows, err := db.QueryContext(ctx, dataSQL, args...)
	if err != nil {
		return nil, nil, requeryErr("导出查询", expandedSQL, bindNames, binds, hintORA(ctx, db, dsRow.DbType, expandedSQL, err))
	}
	resultCols, err := rows.Columns()
	if err != nil {
		rows.Close()
		return nil, nil, err
	}
	// 输出列映射（配置列优先，隐藏列不导出；未配置则全列输出）
	var outCols []ColumnOut
	var outIdx []int
	if len(cols) > 0 {
		indexByName := map[string]int{}
		for i, name := range resultCols {
			indexByName[name] = i
		}
		for _, c := range cols {
			if c.Hidden == 1 {
				continue
			}
			if idx, ok := indexByName[c.ColName]; ok {
				outCols = append(outCols, ColumnOut{Name: c.ColName, Label: c.Label,
					DictCode: c.DictCode, DictType: c.DictType, ExportFmt: c.ExportFmt})
				outIdx = append(outIdx, idx)
			}
		}
	}
	if len(outCols) == 0 {
		for i := range resultCols {
			outCols = append(outCols, ColumnOut{Name: resultCols[i]})
			outIdx = append(outIdx, i)
		}
	}
	*poutIdx = outIdx
	return rows, outCols, nil
}

// EstimateRows 行数预估（COUNT 查询，受报表超时约束）
func (e *QueryEngine) EstimateRows(ctx context.Context, reportID int64, input map[string]any) (int64, error) {
	report, params, _, err := e.rpt.GetFull(reportID)
	if err != nil {
		return 0, err
	}
	if input == nil {
		input = map[string]any{}
	}
	expandedSQL, binds, err := buildBinds(report.SQLText, params, input)
	if err != nil {
		return 0, err
	}
	if err := validateSQLText(expandedSQL); err != nil {
		return 0, err
	}
	dsRow, err := e.ds.Get(report.DatasourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrDSNNotFound
		}
		return 0, err
	}
	driver, err := datasource.Get(dsRow.DbType)
	if err != nil {
		return 0, err
	}
	db, _, err := e.ds.GetPool(ctx, report.DatasourceID)
	if err != nil {
		return 0, err
	}
	// 自动装配：未在 SQL 中写占位符的查询条件包装为外层 WHERE（列查询开关场景）
	expandedSQL, err = autoWrapSQL(driver, params, binds, expandedSQL)
	if err != nil {
		return 0, err
	}
	args, bindNames, err := orderedArgs(dsRow.DbType, expandedSQL, binds)
	if err != nil {
		return 0, err
	}
	timeout := report.QueryTimeout
	if timeout <= 0 {
		timeout = 30
	}
	ctx2, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	countSQL := driver.Rebind("SELECT COUNT(*) FROM (" + expandedSQL + ") rpt_count")
	var total int64
	if err := db.QueryRowContext(ctx2, countSQL, args...).Scan(&total); err != nil {
		return 0, requeryErr("行数预估", expandedSQL, bindNames, binds, hintORA(ctx, db, dsRow.DbType, expandedSQL, err))
	}
	return total, nil
}

func columnAllowed(name string, cols []model.ReportColumn) bool {
	for _, c := range cols {
		if c.ColName == name {
			return true
		}
	}
	return false
}

// Options 执行查询条件的选项 SQL（选项来源 optionType=sql）。
// OptionSQL 中可引用其他条件的 :param 占位符实现级联过滤；返回最多 200 行 [{value,label}]
func (e *QueryEngine) Options(ctx context.Context, reportID int64, paramName string, input map[string]any) ([]map[string]any, error) {
	report, params, _, err := e.rpt.GetFull(reportID)
	if err != nil {
		return nil, err
	}
	var p *model.ReportParam
	for i := range params {
		if params[i].ParamName == paramName {
			p = &params[i]
			break
		}
	}
	if p == nil {
		return nil, fmt.Errorf("%w: 未找到条件项 %s", ErrParamInvalid, paramName)
	}
	// 选项来源：dict → 按字典编码加载（可按类型筛选）；sql → 条件自带的 OptionSQL；fixed → 前端渲染
	if p.OptionType == "dict" {
		if strings.TrimSpace(p.OptionDict) == "" {
			return nil, fmt.Errorf("%w: 条件 %s 未选择字典", ErrParamInvalid, p.Label)
		}
		return e.dict.Options(ctx, e.ds, p.OptionDict, p.OptionDictType)
	}
	optionSQL := p.OptionSQL
	if strings.TrimSpace(optionSQL) == "" {
		return []map[string]any{}, nil // fixed 选项由前端直接渲染
	}
	if err := validateSQLText(optionSQL); err != nil {
		return nil, err
	}

	// OptionSQL 同样支持 :param 绑定（级联过滤场景）
	expanded, binds, err := buildBinds(optionSQL, params, input)
	if err != nil {
		return nil, err
	}
	dsRow, err := e.ds.Get(report.DatasourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrDSNNotFound
		}
		return nil, err
	}
	driver, err := datasource.Get(dsRow.DbType)
	if err != nil {
		return nil, err
	}
	db, dbVer, err := e.ds.GetPool(ctx, report.DatasourceID)
	if err != nil {
		return nil, err
	}

	// OptionSQL 同样支持 :param 绑定（级联过滤场景）。
	// 未提供的级联参数按 NULL 绑定：不能跳过，否则后面的值会与占位符位次错位
	names := uniqueNames(dsRow.DbType, datasource.ExtractParamNames(expanded))
	args := make([]any, 0, len(names))
	for _, n := range names {
		args = append(args, binds[n])
	}

	timeout := report.QueryTimeout
	if timeout <= 0 {
		timeout = 30
	}
	limitSQL := driver.Rebind(paginateSQL(driver, dbVer, expanded, 0, 200))
	qryCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	rows, err := db.QueryContext(qryCtx, limitSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("选项查询失败: %w", err)
	}
	defer rows.Close()

	resultCols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	raw := make([]any, len(resultCols))
	ptrs := make([]any, len(resultCols))
	for i := range raw {
		ptrs[i] = &raw[i]
	}
	out := []map[string]any{}
	for rows.Next() {
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		val := normalizeValue(raw[0])
		label := val
		if len(resultCols) > 1 {
			label = normalizeValue(raw[1])
		}
		out = append(out, map[string]any{"value": val, "label": label})
	}
	return out, rows.Err()
}

// normalizeValue 归一化为 JSON 安全类型
func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case []byte:
		return string(t)
	case time.Time:
		if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 {
			return t.Format("2006-01-02")
		}
		return t.Format("2006-01-02 15:04:05")
	default:
		return v
	}
}

// ---------- 参数绑定 ----------

// resolveDefault 解析默认值/动态宏
func resolveDefault(p model.ReportParam) any {
	macro := p.DefaultMacro
	if macro == "" && p.DefaultVal != "" {
		if strings.HasPrefix(p.DefaultVal, "${") && strings.HasSuffix(p.DefaultVal, "}") {
			macro = strings.TrimSuffix(strings.TrimPrefix(p.DefaultVal, "${"), "}")
		} else {
			// 多选下拉的默认值存库是逗号拼接的多个键值，这里还原成数组，
			// 否则 toAnySlice 会把 "1,2" 当成单个值送进 IN (:'1,2')
			if p.Widget == "multiSelect" {
				parts := strings.Split(p.DefaultVal, ",")
				out := make([]any, 0, len(parts))
				for _, s := range parts {
					if s = strings.TrimSpace(s); s != "" {
						out = append(out, s)
					}
				}
				return out
			}
			return p.DefaultVal
		}
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	switch macro {
	case "today":
		if p.Widget == "dateRange" {
			return []string{fmtDate(today), fmtDate(today)}
		}
		return fmtDate(today)
	case "yesterday":
		y := today.AddDate(0, 0, -1)
		if p.Widget == "dateRange" {
			return []string{fmtDate(y), fmtDate(y)}
		}
		return fmtDate(y)
	case "last7days":
		return []string{fmtDate(today.AddDate(0, 0, -6)), fmtDate(today)}
	case "last30days":
		return []string{fmtDate(today.AddDate(0, 0, -29)), fmtDate(today)}
	case "thisMonth":
		ms := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
		return []string{fmtDate(ms), fmtDate(today)}
	default:
		if p.DefaultVal != "" {
			return p.DefaultVal
		}
		return nil
	}
}

func fmtDate(t time.Time) string { return t.Format("2006-01-02") }

// toScalar 按控件类型做标量转换
func toScalar(widget string, v any) (any, error) {
	switch widget {
	case "number":
		switch n := v.(type) {
		case float64:
			return n, nil
		case int64:
			return n, nil
		case string:
			return strconv.ParseFloat(strings.TrimSpace(n), 64)
		default:
			return nil, fmt.Errorf("%w: 数字类型收到 %T", ErrParamInvalid, v)
		}
	case "date":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%w: 日期类型收到 %T", ErrParamInvalid, v)
		}
		if _, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local); err != nil {
			return nil, fmt.Errorf("%w: 日期格式应为 yyyy-MM-dd（收到 %s）", ErrParamInvalid, s)
		}
		return strings.TrimSpace(s), nil
	case "dateTime":
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("%w: 日期时间类型收到 %T", ErrParamInvalid, v)
		}
		if _, err := time.ParseInLocation("2006-01-02 15:04:05", strings.TrimSpace(s), time.Local); err != nil {
			return nil, fmt.Errorf("%w: 日期时间格式应为 yyyy-MM-dd HH:mm:ss（收到 %s）", ErrParamInvalid, s)
		}
		return strings.TrimSpace(s), nil
	default:
		if s, ok := v.(string); ok {
			return s, nil
		}
		// 下拉/字典选择可能提交 {value,label} 这类对象，取 value 继续按标量处理
		if m, ok := v.(map[string]any); ok {
			if inner, ok2 := m["value"]; ok2 {
				return toScalar(widget, inner)
			}
			return nil, fmt.Errorf("%w: 下拉值应为标量，收到对象（缺少 value 字段）", ErrParamInvalid)
		}
		// 单元素数组按标量处理（多选控件退化为单选时的提交形态）
		if arr, ok := v.([]any); ok {
			if len(arr) == 1 {
				return toScalar(widget, arr[0])
			}
			return nil, fmt.Errorf("%w: 该条件应为单值，收到 %d 个值", ErrParamInvalid, len(arr))
		}
		if arr, ok := v.([]string); ok {
			if len(arr) == 1 {
				return toScalar(widget, arr[0])
			}
			return nil, fmt.Errorf("%w: 该条件应为单值，收到 %d 个值", ErrParamInvalid, len(arr))
		}
		return v, nil
	}
}

func toDatePair(widget string, v any) (any, any, error) {
	arr, ok := v.([]any)
	if !ok || len(arr) != 2 {
		// 兼容字符串数组
		if ss, ok2 := v.([]string); ok2 && len(ss) == 2 {
			v1, err := toScalar(widget, ss[0])
			if err != nil {
				return nil, nil, err
			}
			v2, err := toScalar(widget, ss[1])
			if err != nil {
				return nil, nil, err
			}
			return v1, v2, nil
		}
		return nil, nil, fmt.Errorf("%w: 范围类型需要 [起, 止] 两个值", ErrParamInvalid)
	}
	v1, err := toScalar(widget, arr[0])
	if err != nil {
		return nil, nil, err
	}
	v2, err := toScalar(widget, arr[1])
	if err != nil {
		return nil, nil, err
	}
	return v1, v2, nil
}

// buildBinds 按条件配置绑定参数：返回展开后的 SQL 与绑定值表
// autoWrapSQL 自动装配查询条件：未在 SQL 中书写占位符的条件（列查询开关生成的），
// 包装为外层 WHERE 过滤——SELECT * FROM (原SQL) rpt_auto WHERE <条件>。
// SQL 中已写 :name 占位符的条件仍走内联绑定，两种方式可混用。
// 无自动条件时原样返回。
func autoWrapSQL(driver datasource.Driver, params []model.ReportParam, binds map[string]any, expandedSQL string) (string, error) {
	if len(params) == 0 || len(binds) == 0 {
		return expandedSQL, nil
	}
	sqlNames := map[string]bool{}
	for _, n := range datasource.ExtractParamNames(expandedSQL) {
		sqlNames[n] = true
	}
	var frags []string
	for _, p := range params {
		if sqlNames[p.ParamName] {
			continue // SQL 内已写占位符，走内联绑定
		}
		// 过滤列名：优先取显式配置的 ColumnName（支持中文等结果集列名），
		// 未配置时回退为参数名（须为合法标识符）。列名经 QuoteIdentifier 转义防注入。
		colName := p.ColumnName
		if strings.TrimSpace(colName) == "" {
			if err := datasource.ValidateIdentifier(p.ParamName); err != nil {
				return "", fmt.Errorf("查询条件「%s」的参数名 %s 不是合法列名，无法自动装配（可填写条件的高级「列名」，或在 SQL 中改用 :%s 占位符）", p.Label, p.ParamName, p.ParamName)
			}
			colName = p.ParamName
		} else if err := datasource.ValidateColumnName(colName); err != nil {
			return "", fmt.Errorf("查询条件「%s」的列名非法: %w", p.Label, err)
		}
		col := driver.QuoteIdentifier(colName)
		switch p.MatchType {
		case "in":
			var ph []string
			for i := 1; ; i++ {
				key := fmt.Sprintf("%s_%d", p.ParamName, i)
				if _, ok := binds[key]; !ok {
					break
				}
				ph = append(ph, ":"+key)
			}
			if len(ph) > 0 {
				frags = append(frags, fmt.Sprintf("%s IN (%s)", col, strings.Join(ph, ", ")))
			}
		case "range":
			_, ok1 := binds[p.ParamName+"_1"]
			_, ok2 := binds[p.ParamName+"_2"]
			switch {
			case ok1 && ok2:
				frags = append(frags, fmt.Sprintf("%s BETWEEN :%s_1 AND :%s_2", col, p.ParamName, p.ParamName))
			case ok1:
				frags = append(frags, fmt.Sprintf("%s >= :%s_1", col, p.ParamName))
			case ok2:
				frags = append(frags, fmt.Sprintf("%s <= :%s_2", col, p.ParamName))
			}
		case "like":
			if _, ok := binds[p.ParamName]; ok {
				frags = append(frags, fmt.Sprintf("%s LIKE :%s", col, p.ParamName))
			}
		default: // eq
			if _, ok := binds[p.ParamName]; ok {
				frags = append(frags, fmt.Sprintf("%s = :%s", col, p.ParamName))
			} else {
				// 多值（文本/数字多行输入按等值匹配）：buildBinds 已展开为 name_i 键，按 IN 装配
				var ph []string
				for i := 1; ; i++ {
					key := fmt.Sprintf("%s_%d", p.ParamName, i)
					if _, ok := binds[key]; !ok {
						break
					}
					ph = append(ph, ":"+key)
				}
				if len(ph) > 0 {
					frags = append(frags, fmt.Sprintf("%s IN (%s)", col, strings.Join(ph, ", ")))
				}
			}
		}
	}
	if len(frags) == 0 {
		return expandedSQL, nil
	}
	return fmt.Sprintf("SELECT * FROM (%s) rpt_auto WHERE %s", expandedSQL, strings.Join(frags, " AND ")), nil
}

func buildBinds(sqlText string, params []model.ReportParam, input map[string]any) (string, map[string]any, error) {
	binds := map[string]any{}
	expanded := sqlText

	for _, p := range params {
		// 参数名是绑定变量的键：为空会生成 ": = 值" 这种非法占位符，
		// 驱动侧只会报出难以定位的绑定错误，这里提前拦下并指明是哪个条件
		if strings.TrimSpace(p.ParamName) == "" {
			return "", nil, fmt.Errorf("查询条件「%s」未配置参数名，请在报表配置中补全（参数名对应 SQL 中的 :参数名，或自动装配时的结果列名）", p.Label)
		}
		val, provided := input[p.ParamName]
		if !provided || val == nil || (isArrayEmpty(val) && p.MatchType != "range") {
			val = resolveDefault(p)
			provided = val != nil
		}
		if !provided || val == nil {
			if p.Required == 1 {
				return "", nil, fmt.Errorf("%w: %s", ErrParamRequired, p.Label)
			}
			continue
		}

		switch p.MatchType {
		case "in":
			arr := toAnySlice(val)
			if len(arr) == 0 {
				if p.Required == 1 {
					return "", nil, fmt.Errorf("%w: %s", ErrParamRequired, p.Label)
				}
				continue
			}
			ph := make([]string, len(arr))
			for i, item := range arr {
				key := fmt.Sprintf("%s_%d", p.ParamName, i+1)
				sv, err := toScalar(p.Widget, item)
				if err != nil {
					return "", nil, err
				}
				binds[key] = sv
				ph[i] = ":" + key
			}
			expanded = replaceParam(expanded, p.ParamName, strings.Join(ph, ", "))
		case "range":
			v1, v2, err := toDatePair(p.Widget, val)
			if err != nil {
				return "", nil, err
			}
			binds[p.ParamName+"_1"] = v1
			binds[p.ParamName+"_2"] = v2
		case "like":
			// 模糊匹配无法安全展开多值（占位符在 LIKE 之内，拿不到列名），明确报错引导改配置
			if arr := toAnySlice(val); len(arr) > 1 {
				return "", nil, fmt.Errorf("%w: 条件「%s」为模糊匹配，不支持多值；请把匹配方式改为「等于」，或在 SQL 中改用 IN + :%s 占位符", ErrParamInvalid, p.Label, p.ParamName)
			}
			w := p.Widget
			if w == "date" {
				w = "text" // 日期模糊匹配按字符串处理
			}
			sv, err := toScalar(w, val)
			if err != nil {
				return "", nil, err
			}
			binds[p.ParamName] = "%" + toString(sv) + "%"
		default: // eq
			// 文本/数字多值搜索：前端把多行（换行/逗号分隔）输入拆成数组传上来。
			// 多个值按 IN 展开（等值语义），单个值仍走普通等值绑定
			if arr := toAnySlice(val); len(arr) > 1 {
				ph := make([]string, len(arr))
				for i, item := range arr {
					key := fmt.Sprintf("%s_%d", p.ParamName, i+1)
					sv, err := toScalar(p.Widget, item)
					if err != nil {
						return "", nil, err
					}
					binds[key] = sv
					ph[i] = ":" + key
				}
				expanded = replaceParam(expanded, p.ParamName, strings.Join(ph, ", "))
			} else {
				if len(arr) == 1 {
					val = arr[0] // 单值数组退化为普通等值绑定
				}
				sv, err := toScalar(p.Widget, val)
				if err != nil {
					return "", nil, err
				}
				binds[p.ParamName] = sv
			}
		}
	}
	return expanded, binds, nil
}

func isArrayEmpty(v any) bool {
	switch t := v.(type) {
	case []any:
		return len(t) == 0
	case []string:
		return len(t) == 0
	case string:
		// 文本/数字条件在页面上填过又删掉后，前端会传来 ""（老版本甚至直接发空串）。
		// 空串若照常绑定会生成 col = ''，任何行都匹配不上，表现为“查不出内容”；
		// 这里把空白串一律按「未填」处理：走默认值，没有默认值则不参与过滤
		return strings.TrimSpace(t) == ""
	default:
		return false
	}
}

func toAnySlice(v any) []any {
	switch t := v.(type) {
	case []any:
		return t
	case []string:
		out := make([]any, len(t))
		for i, s := range t {
			out[i] = s
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		return []any{t}
	default:
		return []any{v}
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// uniqueNames Oracle 使用原生命名参数（:name），同名占位符在服务端只有 1 个绑定变量，
// 因此按「首次出现」去重，否则多传值会导致绑定位次错位；
// 其他方言由 Rebind 把每个出现位置改写为 ?/$n/@pN，必须保留全部出现。
func uniqueNames(dbType string, names []string) []string {
	if dbType != "oracle" {
		return names
	}
	seen := make(map[string]bool, len(names))
	uniq := make([]string, 0, len(names))
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			uniq = append(uniq, n)
		}
	}
	return uniq
}

// orderedArgs 按 SQL 中占位符出现顺序生成绑定值。
func orderedArgs(dbType, sqlText string, binds map[string]any) ([]any, []string, error) {
	names := uniqueNames(dbType, datasource.ExtractParamNames(sqlText))
	args := make([]any, 0, len(names))
	for _, n := range names {
		v, ok := binds[n]
		if !ok {
			return nil, names, fmt.Errorf("%w: :%s", ErrParamMissing, n)
		}
		args = append(args, v)
	}
	return args, names, nil
}

// paginateSQL 按驱动能力选择分页写法：
// 实现 PaginateFor 的驱动（两个 Oracle 实现）按服务器版本选方言（12c+ FETCH / 11g ROWNUM）；
// 其余驱动走通用 Paginate。
func paginateSQL(driver datasource.Driver, version, query string, offset, limit int) string {
	if pv, ok := driver.(interface {
		PaginateFor(version string, query string, offset, limit int) string
	}); ok {
		return pv.PaginateFor(version, query, offset, limit)
	}
	return driver.Paginate(query, offset, limit)
}

// bindTrace 生成「绑定参数：名=值, ...」诊断片段（值过长截断），
// 用于查询失败的报错——绑定类错误靠它就能直接看出是哪个条件、传了什么值。
func bindTrace(names []string, binds map[string]any) string {
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("（绑定参数：")
	for i, n := range names {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(n)
		b.WriteString("=")
		v := toString(binds[n])
		if len(v) > 60 {
			v = v[:60] + "…"
		}
		b.WriteString(v)
	}
	b.WriteString("）")
	return b.String()
}

// sqlExcerpt 截取 SQL 片段用于错误提示，避免整段长 SQL 撑爆提示框
func sqlExcerpt(sqlText string) string {
	s := strings.Join(strings.Fields(sqlText), " ")
	if len(s) > 600 {
		return s[:600] + "…"
	}
	return s
}

// requeryErr 组装查询失败信息：原始错误 + 方言提示 + 绑定参数 + SQL 摘要，
// 并同步写服务端日志，便于用户截图报错时一次定位。
func requeryErr(stage, sqlText string, names []string, binds map[string]any, err error) error {
	detail := bindTrace(names, binds)
	log.Printf("[query] %s 失败：err=%v SQL=%s 绑定=%s", stage, err, sqlExcerpt(sqlText), detail)
	return fmt.Errorf("%s失败: %w%s；SQL = %s", stage, err, detail, sqlExcerpt(sqlText))
}

// replaceParam 替换 SQL 中的 :name 占位符（\b 词边界，避免误伤 :name_x）
func replaceParam(sqlText, name, replacement string) string {
	re := regexp.MustCompile(`:` + regexp.QuoteMeta(name) + `\b`)
	// 跳过字符串字面量（字面量内可能出现 ':name' 形式的普通文本）
	return datasource.MapOutsideStrings(sqlText, func(s string) string {
		return re.ReplaceAllString(s, replacement)
	})
}
