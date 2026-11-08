// Package service 业务逻辑层。
// report_wizard.go：基于数据库表快速生成报表草稿（FR-11）。
//
// 只负责「生成」：输出 SQL、展示列定义、查询条件草稿与告警，不落库。
// 落库仍走既有 POST /api/reports（权限、审计、分组授权默认值都在那里），
// 避免出现第二条写库路径。
package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ce/report-system/backend/internal/datasource"
	"github.com/ce/report-system/backend/internal/model"
)

const (
	// wizardMaxColumns 单次生成列数上限。
	// 制造/钢铁行业的实绩表动辄几百个字段，早先的 100 会逼用户「分批建报表」，
	// 那等于把一张表拆成多张报表，反而破坏业务语义。
	// 各库单表列数上限：Oracle 1000、PostgreSQL 1664、SQLite 2000、MySQL/SQL Server 4096，
	// 取 2000 已高于最常见的三种库，保留它只是拦异常/恶意超大请求，不再拦正常业务表。
	wizardMaxColumns = 2000
	wizardInlineCols = 6  // 列数不超过此值时 SELECT 写在一行，便于阅读
	wizardParamLimit = 20 // 自动生成的条件草稿上限
	// wizardMaxNameLen 结果列名/别名的字符上限，与 model.ReportColumn.ColName(size:64) 保持一致。
	// 超长在 SQLite 下不报错，但 MySQL 严格模式会直接插入失败，所以在生成阶段就拦住。
	wizardMaxNameLen = 64
	// wizardMaxSQLLen 生成 SQL 的字节上限，对应 model.Report.SQLText 在 MySQL 上的 TEXT(64KB)。
	// 宽表一次选满时 SQL 很容易到几十 KB，超长会在保存时报 "Data too long"，
	// 在这里提前拦下并给出可读原因，好过让用户在保存那一步看到数据库原始错误。
	wizardMaxSQLLen = 60000
)

// WizardColumn 向导选中的字段
type WizardColumn struct {
	Name  string `json:"name"`  // 物理列名（必填）
	Alias string `json:"alias"` // 结果列别名，空=同 Name
	Label string `json:"label"` // 展示名，空=字段注释→列名
}

// WizardOrder 排序项
type WizardOrder struct {
	Name string `json:"name"`
	Desc bool   `json:"desc"`
}

// WizardRequest 生成请求
type WizardRequest struct {
	DatasourceID int64          `json:"datasourceId"`
	Table        string         `json:"table"` // 支持「模式.表名」
	Columns      []WizardColumn `json:"columns"`
	Distinct     bool           `json:"distinct"`
	OrderBy      []WizardOrder  `json:"orderBy"`
	WithParams   bool           `json:"withParams"`
}

// WizardResult 生成结果（前端确认后组装成报表保存请求）
type WizardResult struct {
	SQLText  string               `json:"sqlText"`
	Columns  []model.ReportColumn `json:"columns"`
	Params   []model.ReportParam  `json:"params"`
	Warnings []string             `json:"warnings"`
}

// wizardMeta 生成器依赖的元数据能力。抽成接口便于单测注入替身，
// 生产环境传入的 *MetaService 天然满足（同时复用其 TTL 缓存与单飞）。
type wizardMeta interface {
	DriverOf(datasourceID int64) (datasource.Driver, error)
	Columns(ctx context.Context, datasourceID int64, table string, refresh bool) (*ColumnListResult, error)
}

// WizardService 基于表结构生成报表草稿
type WizardService struct {
	meta wizardMeta
}

func NewWizardService(meta wizardMeta) *WizardService {
	return &WizardService{meta: meta}
}

// 标准标识符：字母/下划线开头，仅含字母数字 _$#。命中且非保留字时裸写，可读性最好
var wizardIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$#]*$`)

// 常见保留字：裸写会语法报错，必须加引号（各库保留字不完全一致，取交集偏保守）
var wizardReserved = map[string]bool{
	"ORDER": true, "GROUP": true, "BY": true, "SELECT": true, "FROM": true,
	"WHERE": true, "TABLE": true, "COLUMN": true, "USER": true, "DATE": true,
	"LEVEL": true, "NUMBER": true, "SIZE": true, "COMMENT": true, "TYPE": true,
	"START": true, "CHECK": true, "KEY": true, "INDEX": true, "PRIMARY": true,
	"VALUE": true, "LIMIT": true, "OFFSET": true, "ROW": true, "ROWS": true,
	"AND": true, "OR": true, "NOT": true, "NULL": true, "DEFAULT": true,
}

// wizardPicked 生成过程中的中间结构：一个选中字段的完整信息
type wizardPicked struct {
	expr  string // SELECT 中的表达式（含别名）
	out   string // 结果列名（= 别名或物理列名）
	label string // 展示名
	col   datasource.ColumnMeta
	known bool // 字段是否在元数据里存在
}

// Generate 根据表结构生成 SQL + 展示列 + 条件草稿
func (s *WizardService) Generate(ctx context.Context, req WizardRequest) (*WizardResult, error) {
	table := strings.TrimSpace(req.Table)
	if req.DatasourceID <= 0 {
		return nil, errors.New("请选择数据源")
	}
	if table == "" {
		return nil, errors.New("请选择数据表")
	}
	// 表名会原样拼进 SQL：必须先按「表名 / 模式.表名」逐段校验，防注入
	if err := datasource.ValidateTableRef(table); err != nil {
		return nil, fmt.Errorf("表名非法: %w", err)
	}
	if len(req.Columns) == 0 {
		return nil, errors.New("请至少选择一个字段")
	}
	if len(req.Columns) > wizardMaxColumns {
		return nil, fmt.Errorf("字段数超过上限（%d > %d）：该数字已高于常见数据库的单表列数上限，请检查是否重复提交",
			len(req.Columns), wizardMaxColumns)
	}

	driver, err := s.meta.DriverOf(req.DatasourceID)
	if err != nil {
		return nil, err
	}

	// 字段元数据（走元数据缓存）：用于取真实大小写、类型与注释
	metaRes, err := s.meta.Columns(ctx, req.DatasourceID, table, false)
	if err != nil {
		return nil, err
	}
	metaByUpper := make(map[string]datasource.ColumnMeta, len(metaRes.Items))
	for _, c := range metaRes.Items {
		metaByUpper[strings.ToUpper(c.Name)] = c
	}

	res := &WizardResult{Warnings: []string{}}
	if len(metaRes.Items) == 0 {
		res.Warnings = append(res.Warnings,
			"未能读取该表的字段清单（可能没有元数据查看权限），已按输入字段名生成，请自行确认字段存在")
	}

	// ---------- 结果列 ----------
	picks := make([]wizardPicked, 0, len(req.Columns))
	seenOut := map[string]bool{}
	for i, c := range req.Columns {
		name := strings.TrimSpace(c.Name)
		if name == "" {
			return nil, fmt.Errorf("第 %d 个字段名为空", i+1)
		}
		if err := datasource.ValidateColumnName(name); err != nil {
			return nil, fmt.Errorf("字段名非法（%s）: %w", name, err)
		}
		alias := strings.TrimSpace(c.Alias)
		if alias != "" {
			if err := datasource.ValidateColumnName(alias); err != nil {
				return nil, fmt.Errorf("别名非法（%s）: %w", alias, err)
			}
		}
		out := name
		if alias != "" {
			out = alias
		}
		if len([]rune(out)) > wizardMaxNameLen {
			return nil, fmt.Errorf("结果列名过长（%d 字符，上限 %d）：%s", len([]rune(out)), wizardMaxNameLen, out)
		}
		if seenOut[strings.ToLower(out)] {
			return nil, fmt.Errorf("结果列名重复：%s（请给其中一个设置别名）", out)
		}
		seenOut[strings.ToLower(out)] = true

		m, known := metaByUpper[strings.ToUpper(name)]
		physical := name
		if known {
			physical = m.Name // 用数据库里的真实大小写，避免引号包裹后大小写不匹配
		}

		expr := wizardIdent(driver, physical)
		if alias != "" && alias != physical {
			expr += " AS " + wizardIdent(driver, alias)
		}

		label := strings.TrimSpace(c.Label)
		if label == "" && known {
			label = strings.TrimSpace(m.Comment)
		}
		if label == "" {
			label = out
		}
		// 展示名列宽 64：字段注释常常很长，超长直接截断比让保存失败友好
		if r := []rune(label); len(r) > wizardMaxNameLen {
			label = string(r[:wizardMaxNameLen])
		}

		picks = append(picks, wizardPicked{expr: expr, out: out, label: label, col: m, known: known})
	}

	// ---------- SQL ----------
	exprs := make([]string, 0, len(picks))
	for _, p := range picks {
		exprs = append(exprs, p.expr)
	}
	head := "SELECT "
	if req.Distinct {
		head = "SELECT DISTINCT "
	}

	var b strings.Builder
	if len(exprs) > wizardInlineCols {
		// 列多时一列一行，便于在配置页直接阅读；行尾不留空白
		b.WriteString(strings.TrimRight(head, " "))
		b.WriteString("\n  " + strings.Join(exprs, ",\n  ") + "\n")
	} else {
		b.WriteString(head + strings.Join(exprs, ", ") + "\n")
	}
	b.WriteString("FROM " + table)

	// ---------- 排序 ----------
	orderSQL, orderWarn := wizardOrderBy(driver, req.OrderBy, picks, metaRes.Items)
	if orderWarn != "" {
		res.Warnings = append(res.Warnings, orderWarn)
	}
	if orderSQL != "" {
		b.WriteString("\n" + orderSQL)
	}
	res.SQLText = b.String()
	// SQL 存 rpt_report.sql_text（MySQL TEXT = 64KB），超长会在保存时变成不可读的数据库报错，
	// 这里提前拦住并说清原因：宽表选满时确实可能触顶，用户需要知道是「字段太多」而不是系统坏了。
	if len(res.SQLText) > wizardMaxSQLLen {
		return nil, fmt.Errorf("生成的 SQL 过长（%d 字节，上限 %d 字节）：当前 %d 个字段，"+
			"请减少勾选字段，或给超长字段名设置较短别名",
			len(res.SQLText), wizardMaxSQLLen, len(picks))
	}

	// ---------- 展示列 ----------
	res.Columns = make([]model.ReportColumn, 0, len(picks))
	for i, p := range picks {
		res.Columns = append(res.Columns, model.ReportColumn{
			ColName: p.out,
			Label:   p.label,
			ColType: p.col.DataType,
			SortNo:  i,
		})
	}

	// ---------- 条件草稿 ----------
	if req.WithParams {
		res.Params, res.Warnings = wizardParams(picks, res.Warnings)
	}

	return res, nil
}

// wizardIdent 标识符输出：标准标识符且非保留字时裸写，否则用驱动转义
func wizardIdent(driver datasource.Driver, name string) string {
	if wizardIdentRe.MatchString(name) && !wizardReserved[strings.ToUpper(name)] {
		return name
	}
	return driver.QuoteIdentifier(name)
}

// wizardOrderBy 生成 ORDER BY 子句。
// 用户没指定时用主键兜底——Oracle/MySQL 在无排序时分页结果顺序不稳定，
// 会出现「翻页重复或漏行」，这是必须先堵住的正确性问题。
func wizardOrderBy(driver datasource.Driver, req []WizardOrder, picks []wizardPicked, meta []datasource.ColumnMeta) (string, string) {
	valid := map[string]string{} // 小写 → 结果列名
	for _, p := range picks {
		valid[strings.ToLower(p.out)] = p.out
	}

	var parts []string
	for _, o := range req {
		name := strings.TrimSpace(o.Name)
		if name == "" {
			continue
		}
		out, ok := valid[strings.ToLower(name)]
		if !ok {
			continue // 未勾选的字段不能排序，静默忽略（前端只允许从已选字段里选）
		}
		dir := " ASC"
		if o.Desc {
			dir = " DESC"
		}
		parts = append(parts, wizardIdent(driver, out)+dir)
	}
	if len(parts) > 0 {
		return "ORDER BY " + strings.Join(parts, ", "), ""
	}

	// 兜底：已选字段里的主键列
	for _, m := range meta {
		if !m.IsPK {
			continue
		}
		if out, ok := valid[strings.ToLower(m.Name)]; ok {
			parts = append(parts, wizardIdent(driver, out)+" ASC")
		}
	}
	if len(parts) > 0 {
		return "ORDER BY " + strings.Join(parts, ", "), ""
	}
	return "", "该表未检测到主键（或主键未勾选），建议指定排序字段：无 ORDER BY 时分页结果顺序不稳定，可能出现翻页重复或漏行"
}

// wizardParams 生成查询条件草稿：主键列与 编号/代码/名称 类列各生成一个等值条件。
// 参数名固定 condN（英文标识符，作绑定变量），中文列名放在 columnName 里走查询引擎的自动装配。
func wizardParams(picks []wizardPicked, warnings []string) ([]model.ReportParam, []string) {
	var out []model.ReportParam
	var dateCols []string // 日期/时间列：不自动生成区间条件，但要提示
	n := 0
	for _, p := range picks {
		if p.known && isDateType(p.col.DataType) {
			dateCols = append(dateCols, p.out)
		}
		if !p.known || !wizardParamWorthy(p.col) {
			continue
		}
		if n >= wizardParamLimit {
			warnings = append(warnings, fmt.Sprintf("条件草稿最多生成 %d 个，其余请到配置页手动添加", wizardParamLimit))
			break
		}
		n++
		widget := "text"
		if isNumericType(p.col.DataType) {
			widget = "number"
		}
		out = append(out, model.ReportParam{
			ParamName:  fmt.Sprintf("cond%d", n),
			ColumnName: p.out, // 自动装配按「结果列名」过滤，必须用别名/结果列名
			Label:      p.label,
			Widget:     widget,
			MatchType:  "eq",
			SortNo:     n - 1,
		})
	}
	if len(dateCols) > 0 {
		shown := dateCols
		if len(shown) > 3 {
			shown = shown[:3]
		}
		warnings = append(warnings, "日期/时间字段（"+strings.Join(shown, "、")+
			"）未自动生成区间条件：需要按时间筛选时请在配置页手动添加，匹配方式选 range")
	}
	if len(out) > 0 {
		warnings = append(warnings,
			"已生成条件草稿（默认非必填）：SQL 中未写占位符，条件由查询引擎按结果列名自动装配；参数名可改但不能用中文")
	}
	return out, warnings
}

// wizardParamWorthy 判断某列是否值得生成查询条件
func wizardParamWorthy(c datasource.ColumnMeta) bool {
	if c.IsPK {
		return true
	}
	up := strings.ToUpper(c.Name)
	for _, suffix := range []string{"_NO", "_CODE", "_NAME", "_ID", "NO", "CODE"} {
		if strings.HasSuffix(up, suffix) {
			return true
		}
	}
	return false
}

// isNumericType 粗判数值类型（用于条件控件选择）
func isNumericType(dataType string) bool {
	t := strings.ToUpper(dataType)
	for _, k := range []string{"INT", "NUMBER", "DECIMAL", "NUMERIC", "FLOAT", "DOUBLE", "REAL", "MONEY", "SERIAL"} {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}

// isDateType 粗判日期/时间类型（不自动生成条件，改为提示用户手动加区间）
func isDateType(dataType string) bool {
	t := strings.ToUpper(dataType)
	for _, k := range []string{"DATE", "TIME", "TIMESTAMP"} {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}
