package service

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/ce/report-system/backend/internal/datasource"
)

// ---------- 测试替身 ----------

// fakeDriver 只实现 QuoteIdentifier，用「双引号 + 原样」模拟 PG/Oracle 风格，
// 便于断言「中文列名 / 保留字 / 跨模式表名」是否被正确包裹。
type fakeDriver struct{}

func (fakeDriver) BuildDSN(datasource.ConnConfig) (string, error) { return "", nil }
func (fakeDriver) Open(string, datasource.ConnConfig) (*sql.DB, error) {
	return nil, nil
}
func (fakeDriver) ServerVersion(context.Context, *sql.DB) (string, error) { return "", nil }
func (fakeDriver) ListTables(context.Context, *sql.DB, string) ([]datasource.TableMeta, error) {
	return nil, nil
}
func (fakeDriver) ListColumns(context.Context, *sql.DB, string) ([]datasource.ColumnMeta, error) {
	return nil, nil
}
func (fakeDriver) QuoteIdentifier(name string) string { return `"` + name + `"` }
func (fakeDriver) Rebind(q string) string             { return q }
func (fakeDriver) Paginate(q string, off, lim int) string {
	return q
}

// fakeMeta 元数据替身：直接返回预置的字段清单，不连数据库
type fakeMeta struct {
	cols []datasource.ColumnMeta
	err  error
}

func (f *fakeMeta) DriverOf(int64) (datasource.Driver, error) { return fakeDriver{}, nil }

func (f *fakeMeta) Columns(context.Context, int64, string, bool) (*ColumnListResult, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &ColumnListResult{Items: f.cols}, nil
}

// newTestWizard 构造一个连不上库但能跑通生成流程的 WizardService
func newTestWizard(cols ...datasource.ColumnMeta) *WizardService {
	return NewWizardService(&fakeMeta{cols: cols})
}

// ---------- 用例 ----------

// 跨模式表名 + 主键兜底排序 + 字段注释作为展示名
func TestWizardGenerate_Basic(t *testing.T) {
	w := newTestWizard(
		datasource.ColumnMeta{Name: "MAT_NO", DataType: "NUMBER(10)", IsPK: true, Comment: "材料号"},
		datasource.ColumnMeta{Name: "DICT_CODE", DataType: "VARCHAR2(32)", Comment: "代码"},
	)
	res, err := w.Generate(context.Background(), WizardRequest{
		DatasourceID: 1,
		Table:        "FGPESE0.SYS_DICT_TYPE",
		Columns:      []WizardColumn{{Name: "MAT_NO"}, {Name: "DICT_CODE"}},
	})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}

	wantSQL := "SELECT MAT_NO, DICT_CODE\nFROM FGPESE0.SYS_DICT_TYPE\nORDER BY MAT_NO ASC"
	if res.SQLText != wantSQL {
		t.Errorf("SQL 不符\n实际:\n%s\n期望:\n%s", res.SQLText, wantSQL)
	}
	if len(res.Columns) != 2 {
		t.Fatalf("展示列数量 %d，期望 2", len(res.Columns))
	}
	if res.Columns[0].Label != "材料号" {
		t.Errorf("展示名应取字段注释，实际 %q", res.Columns[0].Label)
	}
	if res.Columns[0].ColType != "NUMBER(10)" {
		t.Errorf("列类型应为元数据类型，实际 %q", res.Columns[0].ColType)
	}
	if res.Columns[1].SortNo != 1 {
		t.Errorf("SortNo 应为下标 1，实际 %d", res.Columns[1].SortNo)
	}
}

// 中文别名：表达式要加引号，且条件草稿的 columnName 必须与结果列名（别名）一致，
// 否则查询引擎按结果列名自动装配时会过滤不到
func TestWizardGenerate_ChineseAlias(t *testing.T) {
	w := newTestWizard(
		datasource.ColumnMeta{Name: "MAT_NO", DataType: "VARCHAR2(20)", IsPK: true, Comment: "材料号"},
	)
	res, err := w.Generate(context.Background(), WizardRequest{
		DatasourceID: 1,
		Table:        "T_MAT",
		Columns:      []WizardColumn{{Name: "MAT_NO", Alias: "材料编号"}},
		WithParams:   true,
	})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if !strings.Contains(res.SQLText, `MAT_NO AS "材料编号"`) {
		t.Errorf("中文别名应被引号包裹，实际 SQL:\n%s", res.SQLText)
	}
	if len(res.Params) != 1 {
		t.Fatalf("应生成 1 个条件草稿，实际 %d", len(res.Params))
	}
	if res.Params[0].ColumnName != "材料编号" {
		t.Errorf("条件列名应为别名，实际 %q", res.Params[0].ColumnName)
	}
	if res.Params[0].ParamName != "cond1" {
		t.Errorf("参数名应为 cond1（英文标识符），实际 %q", res.Params[0].ParamName)
	}
}

// 未知字段（元数据读不到）也要能生成，但必须给出告警且不猜条件
func TestWizardGenerate_UnknownColumn(t *testing.T) {
	w := newTestWizard() // 空元数据
	res, err := w.Generate(context.Background(), WizardRequest{
		DatasourceID: 1,
		Table:        "T_MAT",
		Columns:      []WizardColumn{{Name: "MAT_NO"}},
		WithParams:   true,
	})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if len(res.Params) != 0 {
		t.Errorf("字段未知时不应猜条件，实际生成 %d 个", len(res.Params))
	}
	if !hasWarning(res.Warnings, "未能读取该表的字段清单") {
		t.Errorf("缺少元数据缺失告警，实际: %v", res.Warnings)
	}
	if !hasWarning(res.Warnings, "未检测到主键") {
		t.Errorf("缺少无主键告警，实际: %v", res.Warnings)
	}
}

// 无主键：不生成 ORDER BY，但必须告警（分页顺序不稳定会重复/漏行）
func TestWizardGenerate_NoPrimaryKey(t *testing.T) {
	w := newTestWizard(
		datasource.ColumnMeta{Name: "A", DataType: "VARCHAR2(10)"},
	)
	res, err := w.Generate(context.Background(), WizardRequest{
		DatasourceID: 1,
		Table:        "T_NO_PK",
		Columns:      []WizardColumn{{Name: "A"}},
	})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if strings.Contains(res.SQLText, "ORDER BY") {
		t.Errorf("无主键时不应凭空生成 ORDER BY，实际:\n%s", res.SQLText)
	}
	if !hasWarning(res.Warnings, "未检测到主键") {
		t.Errorf("缺少无主键告警，实际: %v", res.Warnings)
	}
}

// 用户指定排序优先于主键兜底；未勾选的字段排序请求被静默忽略
func TestWizardGenerate_ExplicitOrder(t *testing.T) {
	w := newTestWizard(
		datasource.ColumnMeta{Name: "ID", DataType: "NUMBER", IsPK: true},
		datasource.ColumnMeta{Name: "AMT", DataType: "NUMBER"},
	)
	res, err := w.Generate(context.Background(), WizardRequest{
		DatasourceID: 1,
		Table:        "T_ORDER",
		Columns:      []WizardColumn{{Name: "ID"}, {Name: "AMT"}},
		OrderBy: []WizardOrder{
			{Name: "AMT", Desc: true},
			{Name: "NOT_SELECTED", Desc: false}, // 未勾选 → 忽略
		},
	})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if !strings.HasSuffix(res.SQLText, "ORDER BY AMT DESC") {
		t.Errorf("排序子句不符，实际:\n%s", res.SQLText)
	}
}

// 列数超过 wizardInlineCols 时换行输出，DISTINCT 紧跟在 SELECT 之后
func TestWizardGenerate_DistinctAndMultiline(t *testing.T) {
	cols := make([]datasource.ColumnMeta, 0, 7)
	picks := make([]WizardColumn, 0, 7)
	for _, n := range []string{"C1", "C2", "C3", "C4", "C5", "C6", "C7"} {
		cols = append(cols, datasource.ColumnMeta{Name: n, DataType: "VARCHAR2(10)"})
		picks = append(picks, WizardColumn{Name: n})
	}
	w := newTestWizard(cols...)
	res, err := w.Generate(context.Background(), WizardRequest{
		DatasourceID: 1,
		Table:        "T_WIDE",
		Columns:      picks,
		Distinct:     true,
	})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if !strings.HasPrefix(res.SQLText, "SELECT DISTINCT\n") {
		t.Errorf("DISTINCT 位置不符，实际:\n%s", res.SQLText)
	}
	if got := strings.Count(res.SQLText, "\n  C"); got != 7 {
		t.Errorf("多列应每行一个（7 行），实际 %d 行:\n%s", got, res.SQLText)
	}
}

// 保留字必须加引号，否则裸写会语法报错
func TestWizardIdent_ReservedWord(t *testing.T) {
	d := fakeDriver{}
	cases := []struct{ in, want string }{
		{"MAT_NO", "MAT_NO"},
		{"ORDER", `"ORDER"`},
		{"level", `"level"`},
		{"材料号", `"材料号"`},
		{"含 空格", `"含 空格"`},
	}
	for _, c := range cases {
		if got := wizardIdent(d, c.in); got != c.want {
			t.Errorf("wizardIdent(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

// 表名/列名是拼进 SQL 的，注入必须在校验层被挡住
func TestWizardGenerate_RejectInjection(t *testing.T) {
	w := newTestWizard(datasource.ColumnMeta{Name: "A", DataType: "VARCHAR2(10)"})
	bad := []string{
		"SYS.T; DROP TABLE X",
		"A.B.C",
		"T_MAT --",
	}
	for _, tb := range bad {
		_, err := w.Generate(context.Background(), WizardRequest{
			DatasourceID: 1,
			Table:        tb,
			Columns:      []WizardColumn{{Name: "A"}},
		})
		if err == nil {
			t.Errorf("表名 %q 应被拒绝", tb)
		}
	}

	if _, err := w.Generate(context.Background(), WizardRequest{
		DatasourceID: 1,
		Table:        "T_MAT",
		Columns:      []WizardColumn{{Name: `A" OR 1=1 --`}},
	}); err == nil {
		t.Error("含非法字符的列名应被拒绝")
	}
}

// 结果列名重复会让查询引擎按列名取值时串列，必须在生成阶段拦下
func TestWizardGenerate_DuplicateColumn(t *testing.T) {
	w := newTestWizard(datasource.ColumnMeta{Name: "A", DataType: "VARCHAR2(10)"})
	_, err := w.Generate(context.Background(), WizardRequest{
		DatasourceID: 1,
		Table:        "T_MAT",
		Columns:      []WizardColumn{{Name: "A"}, {Name: "A", Alias: "a"}},
	})
	if err == nil || !strings.Contains(err.Error(), "重复") {
		t.Errorf("重复结果列名应报错，实际: %v", err)
	}
}

// 大宽表：制造/钢铁实绩表常有几百个字段，必须能一次生成。
// 早先 100 列上限会逼用户「分批建报表」，等于把一张表拆成多张，破坏业务语义。
func TestWizardGenerate_WideTable(t *testing.T) {
	const n = 500
	cols := make([]datasource.ColumnMeta, 0, n)
	picks := make([]WizardColumn, 0, n)
	for i := 0; i < n; i++ {
		name := "C" + itoa(i)
		cols = append(cols, datasource.ColumnMeta{Name: name, DataType: "VARCHAR2(32)"})
		picks = append(picks, WizardColumn{Name: name})
	}
	cols[0].IsPK = true // 无显式排序时回落到主键

	res, err := newTestWizard(cols...).Generate(context.Background(), WizardRequest{
		DatasourceID: 1, Table: "T_MAT", Columns: picks, WithParams: true,
	})
	if err != nil {
		t.Fatalf("%d 列应能一次生成，实际报错: %v", n, err)
	}
	if len(res.Columns) != n {
		t.Errorf("展示列应为 %d 个，实际 %d", n, len(res.Columns))
	}
	if !strings.Contains(res.SQLText, "\n  C0,\n") {
		t.Errorf("多列应按「一列一行」输出，实际前 200 字节:\n%.200s", res.SQLText)
	}
	if !strings.HasSuffix(res.SQLText, "ORDER BY C0 ASC") {
		t.Errorf("应回落到主键排序，实际结尾: %.40s", res.SQLText[len(res.SQLText)-40:])
	}
	if hasWarning(res.Warnings, "未检测到主键") {
		t.Error("表有主键时不应告警")
	}
}

// 生成的 SQL 超过 64KB（MySQL TEXT 上限）时要给出可读原因，而不是让保存时报 Data too long
func TestWizardGenerate_SQLTooLong(t *testing.T) {
	// 列名取 60 字符（加序号后仍 ≤ 64，避免先被「结果列名过长」拦下），
	// 1000 列即可让 SQL 超过 60000 字节
	const n = 1000
	cols := make([]datasource.ColumnMeta, 0, n)
	picks := make([]WizardColumn, 0, n)
	long := strings.Repeat("X", 60)
	for i := 0; i < n; i++ {
		name := long + itoa(i)
		cols = append(cols, datasource.ColumnMeta{Name: name, DataType: "VARCHAR2(32)"})
		picks = append(picks, WizardColumn{Name: name})
	}
	_, err := newTestWizard(cols...).Generate(context.Background(), WizardRequest{
		DatasourceID: 1, Table: "T_MAT", Columns: picks,
	})
	if err == nil || !strings.Contains(err.Error(), "过长") {
		t.Errorf("超长 SQL 应报「过长」，实际: %v", err)
	}
}

// 入参校验：未选数据源 / 未选表 / 未选字段 / 超过列数上限
func TestWizardGenerate_Validate(t *testing.T) {
	w := newTestWizard()
	base := WizardRequest{
		DatasourceID: 1,
		Table:        "T_MAT",
		Columns:      []WizardColumn{{Name: "A"}},
	}

	noDs := base
	noDs.DatasourceID = 0
	if _, err := w.Generate(context.Background(), noDs); err == nil {
		t.Error("未选数据源应报错")
	}

	noTable := base
	noTable.Table = "  "
	if _, err := w.Generate(context.Background(), noTable); err == nil {
		t.Error("未选表应报错")
	}

	noCol := base
	noCol.Columns = nil
	if _, err := w.Generate(context.Background(), noCol); err == nil {
		t.Error("未选字段应报错")
	}

	tooMany := base
	tooMany.Columns = make([]WizardColumn, wizardMaxColumns+1)
	for i := range tooMany.Columns {
		tooMany.Columns[i] = WizardColumn{Name: "C" + itoa(i)}
	}
	if _, err := w.Generate(context.Background(), tooMany); err == nil {
		t.Errorf("超过 %d 列应报错", wizardMaxColumns)
	}
}

// 结果列名超过 64 字符：SQLite 不报错，但 MySQL 严格模式会插入失败，必须在生成阶段拦下
func TestWizardGenerate_NameTooLong(t *testing.T) {
	long := strings.Repeat("列", wizardMaxNameLen+1)
	w := newTestWizard(datasource.ColumnMeta{Name: "A", DataType: "VARCHAR2(10)"})
	_, err := w.Generate(context.Background(), WizardRequest{
		DatasourceID: 1, Table: "T_MAT",
		Columns: []WizardColumn{{Name: "A", Alias: long}},
	})
	if err == nil || !strings.Contains(err.Error(), "过长") {
		t.Errorf("超长别名应报错，实际: %v", err)
	}
}

// 字段注释过长时展示名截断到 64 字符：不阻断生成，但也不能超列宽
func TestWizardGenerate_LabelTruncated(t *testing.T) {
	long := strings.Repeat("注", wizardMaxNameLen+10)
	w := newTestWizard(datasource.ColumnMeta{Name: "A", DataType: "VARCHAR2(10)", Comment: long})
	res, err := w.Generate(context.Background(), WizardRequest{
		DatasourceID: 1, Table: "T_MAT", Columns: []WizardColumn{{Name: "A"}},
	})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if got := len([]rune(res.Columns[0].Label)); got != wizardMaxNameLen {
		t.Errorf("展示名应截断到 %d 字符，实际 %d", wizardMaxNameLen, got)
	}
}

// ---------- 小工具 ----------
func hasWarning(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// itoa 避免为一个测试引入 strconv，同时保证列名唯一
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
