package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/ce/report-system/backend/internal/datasource"
	"github.com/ce/report-system/backend/internal/model"
	"gorm.io/gorm"
)

// dictCodeRe 字典编码规则：字母开头，字母/数字/下划线
var dictCodeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*$`)

// dictRowLimit 字典内容单次加载上限（防止全表大结果拖垮内存）
const dictRowLimit = 5000

// labelCacheTTL 字典翻译缓存有效期：宽表常绑定几十个字典列，
// 每页查询若逐列回源业务库会明显拖慢首屏，这里用短 TTL 缓存摊平
const labelCacheTTL = 60 * time.Second

// dictLabelEntry 缓存的 键值→标签 映射
type dictLabelEntry struct {
	labels   map[string]string
	loadedAt time.Time
}

// dictLabelCache 字典翻译缓存（key: code|typeFilter）；字典保存/删除后整体失效
var dictLabelCache = struct {
	mu    sync.RWMutex
	items map[string]*dictLabelEntry
}{items: map[string]*dictLabelEntry{}}

func labelCacheGet(key string) (map[string]string, bool) {
	dictLabelCache.mu.RLock()
	defer dictLabelCache.mu.RUnlock()
	e, ok := dictLabelCache.items[key]
	if !ok || time.Since(e.loadedAt) > labelCacheTTL {
		return nil, false
	}
	return e.labels, true
}

func labelCachePut(key string, labels map[string]string) {
	dictLabelCache.mu.Lock()
	defer dictLabelCache.mu.Unlock()
	dictLabelCache.items[key] = &dictLabelEntry{labels: labels, loadedAt: time.Now()}
}

// labelCacheClear 字典配置变更后清空翻译缓存
func labelCacheClear() {
	dictLabelCache.mu.Lock()
	defer dictLabelCache.mu.Unlock()
	dictLabelCache.items = map[string]*dictLabelEntry{}
}

// DictService 数据字典服务：下拉选项与列翻译的集中配置（三种来源），可被多个报表复用
type DictService struct {
	db *gorm.DB
}

func NewDictService(db *gorm.DB) *DictService {
	return &DictService{db: db}
}

// List 字典清单（按编码排序）
func (s *DictService) List() ([]model.Dict, error) {
	var list []model.Dict
	err := s.db.Order("code").Find(&list).Error
	return list, err
}

// GetByCode 按编码取字典
func (s *DictService) GetByCode(code string) (*model.Dict, error) {
	var d model.Dict
	err := s.db.Where("code = ?", code).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("字典 %s 不存在", code)
	}
	return &d, err
}

// GetByID 按 ID 取字典
func (s *DictService) GetByID(id int64) (*model.Dict, error) {
	var d model.Dict
	if err := s.db.First(&d, id).Error; err != nil {
		return nil, errors.New("字典不存在")
	}
	return &d, nil
}

// Validate 保存前校验（按来源类型校验各自的必填项与标识符合法性）
func (s *DictService) Validate(d *model.Dict) error {
	if strings.TrimSpace(d.Code) == "" {
		return errors.New("字典编码必填")
	}
	if !dictCodeRe.MatchString(d.Code) {
		return errors.New("字典编码仅允许字母、数字、下划线")
	}
	if strings.TrimSpace(d.Name) == "" {
		return errors.New("字典名称必填")
	}
	if d.DatasourceID <= 0 {
		return errors.New("请选择字典数据所在的数据源")
	}
	if strings.TrimSpace(d.TypeLabelCol) != "" {
		if err := datasource.ValidateIdentifier(d.TypeLabelCol); err != nil {
			return fmt.Errorf("类型名称列非法: %w", err)
		}
	}
	switch d.SourceType {
	case "separated": // 分离式：类型表 + 数据表 + 关联字段
		// 表名允许「模式.表名」跨模式引用；列名仍为单标识符
		for _, name := range []string{d.TypeTable, d.DataTable} {
			if strings.TrimSpace(name) == "" {
				return errors.New("分离式：类型表/数据表为必填")
			}
			if err := datasource.ValidateTableRef(name); err != nil {
				return fmt.Errorf("分离式表名非法: %w", err)
			}
		}
		for _, name := range []string{d.TypeCodeCol, d.DataFKCol, d.ValueCol, d.LabelCol} {
			if strings.TrimSpace(name) == "" {
				return errors.New("分离式：类型编码列/关联字段/键值列/标签列均为必填")
			}
			if err := datasource.ValidateIdentifier(name); err != nil {
				return fmt.Errorf("分离式标识符非法: %w", err)
			}
		}
	case "aggregate": // 聚合式：单表
		if strings.TrimSpace(d.SrcTable) == "" {
			return errors.New("聚合式：表名为必填")
		}
		if err := datasource.ValidateTableRef(d.SrcTable); err != nil {
			return fmt.Errorf("聚合式表名非法: %w", err)
		}
		for _, name := range []string{d.TypeCol, d.ValueCol, d.LabelCol} {
			if strings.TrimSpace(name) == "" {
				return errors.New("聚合式：字典类型列/键值列/标签列均为必填")
			}
			if err := datasource.ValidateIdentifier(name); err != nil {
				return fmt.Errorf("聚合式标识符非法: %w", err)
			}
		}
	case "sql": // SQL式：查询 + 结果列映射
		if strings.TrimSpace(d.SQLText) == "" {
			return errors.New("SQL式：必须填写查询语句")
		}
		if err := validateSQLText(d.SQLText); err != nil {
			return fmt.Errorf("字典 SQL 校验失败: %w", err)
		}
		for _, name := range []string{d.TypeCol, d.ValueCol, d.LabelCol} {
			if strings.TrimSpace(name) == "" {
				return errors.New("SQL式：类型列/键值列/标签列（结果列名映射）均为必填")
			}
			if err := datasource.ValidateIdentifier(name); err != nil {
				return fmt.Errorf("SQL式结果列名非法: %w", err)
			}
		}
	default:
		return errors.New("来源类型仅支持 separated（分离式）/ aggregate（聚合式）/ sql")
	}
	return nil
}

// Save 新建或更新（按 ID 判断）；编码唯一性由数据库约束保证
func (s *DictService) Save(d *model.Dict) error {
	if err := s.Validate(d); err != nil {
		return err
	}
	defer labelCacheClear() // 字典配置变更后翻译缓存立即失效
	if d.ID > 0 {
		var count int64
		s.db.Model(&model.Dict{}).Where("id = ?", d.ID).Count(&count)
		if count == 0 {
			return errors.New("字典不存在")
		}
		return s.db.Model(&model.Dict{}).Where("id = ?", d.ID).Updates(map[string]any{
			"code": d.Code, "name": d.Name, "source_type": d.SourceType,
			"datasource_id": d.DatasourceID,
			"type_table": d.TypeTable, "type_code_col": d.TypeCodeCol,
			"data_table": d.DataTable, "data_fk_col": d.DataFKCol,
			"table_name": d.SrcTable, "type_col": d.TypeCol, "type_label_col": d.TypeLabelCol,
			"value_col": d.ValueCol, "label_col": d.LabelCol,
			"sql_text": d.SQLText, "filter_where": d.FilterWhere,
			"status": d.Status, "remark": d.Remark,
		}).Error
	}
	return s.db.Create(d).Error
}

// Delete 删除字典
func (s *DictService) Delete(id int64) error {
	defer labelCacheClear()
	return s.db.Delete(&model.Dict{}, id).Error
}

// contentSQL 组装内容查询：固定输出三列——键值、标签、类型（列名均已在 Validate 校验）
func contentSQL(d *model.Dict) string {
	var q string
	switch d.SourceType {
	case "separated":
		q = fmt.Sprintf("SELECT %s, %s, %s FROM %s", d.ValueCol, d.LabelCol, d.DataFKCol, d.DataTable)
		if strings.TrimSpace(d.FilterWhere) != "" {
			q += " WHERE " + d.FilterWhere
		}
	case "aggregate":
		q = fmt.Sprintf("SELECT %s, %s, %s FROM %s", d.ValueCol, d.LabelCol, d.TypeCol, d.SrcTable)
		if strings.TrimSpace(d.FilterWhere) != "" {
			q += " WHERE " + d.FilterWhere
		}
	default: // sql
		q = fmt.Sprintf("SELECT %s, %s, %s FROM (%s) rpt_dict_src", d.ValueCol, d.LabelCol, d.TypeCol, d.SQLText)
	}
	return q
}

// dictRows 加载字典内容（最多 dictRowLimit 行），返回 [键值, 标签, 类型] 三元组
func dictRows(ctx context.Context, dsSvc *DatasourceService, d *model.Dict) ([][3]any, error) {
	if d.DatasourceID <= 0 {
		return nil, errors.New("字典未配置数据源")
	}
	dsRow, err := dsSvc.Get(d.DatasourceID)
	if err != nil {
		return nil, fmt.Errorf("字典数据源不存在: %w", err)
	}
	driver, err := datasource.Get(dsRow.DbType)
	if err != nil {
		return nil, err
	}
	db, _, err := dsSvc.GetPool(ctx, d.DatasourceID)
	if err != nil {
		return nil, err
	}
	q := driver.Rebind(driver.Paginate(contentSQL(d), 0, dictRowLimit))
	rows, err := db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("字典内容查询失败: %w", hintORA(ctx, db, dsRow.DbType, contentSQL(d), err))
	}
	defer rows.Close()
	var out [][3]any
	for rows.Next() {
		var v, l, t sql.NullString
		if err := rows.Scan(&v, &l, &t); err != nil {
			// 键值可能为数字——改用 any 中转重试该行不可行（Scan 已消费），整体改用 any 扫描
			return nil, fmt.Errorf("字典内容读取失败: %w", err)
		}
		// Oracle CHAR 列会补尾部空格：键值/标签/类型统一 trim。
		// 不 trim 的话——类型筛选（内容行类型=数据表外键列，与类型表编码列可能一 CHAR 一 VARCHAR）会全部失配，
		// 表现为下拉无选项且无报错；翻译映射的键同理会对不上报表单元格值。
		out = append(out, [3]any{
			strings.TrimSpace(v.String), strings.TrimSpace(l.String), strings.TrimSpace(t.String),
		})
	}
	return out, rows.Err()
}

// Types 查询字典的可用「字典类型」清单（value=类型编码，label=类型名称；名称列未配置时 label=编码）
func (s *DictService) Types(ctx context.Context, dsSvc *DatasourceService, d *model.Dict) ([]map[string]any, error) {
	hasLabel := strings.TrimSpace(d.TypeLabelCol) != ""
	var q string
	switch d.SourceType {
	case "separated":
		cols := d.TypeCodeCol
		if hasLabel {
			cols += ", " + d.TypeLabelCol
		}
		q = fmt.Sprintf("SELECT DISTINCT %s FROM %s", cols, d.TypeTable)
	case "aggregate":
		cols := d.TypeCol
		if hasLabel {
			cols += ", " + d.TypeLabelCol
		}
		q = fmt.Sprintf("SELECT DISTINCT %s FROM %s", cols, d.SrcTable)
		if strings.TrimSpace(d.FilterWhere) != "" {
			q += " WHERE " + d.FilterWhere
		}
	default: // sql：从查询结果中取类型列（可带名称列）
		cols := d.TypeCol
		if hasLabel {
			cols += ", " + d.TypeLabelCol
		}
		q = fmt.Sprintf("SELECT DISTINCT %s FROM (%s) rpt_dict_types", cols, d.SQLText)
	}
	dsRow, err := dsSvc.Get(d.DatasourceID)
	if err != nil {
		return nil, err
	}
	driver, err := datasource.Get(dsRow.DbType)
	if err != nil {
		return nil, err
	}
	db, _, err := dsSvc.GetPool(ctx, d.DatasourceID)
	if err != nil {
		return nil, err
	}
	limitSQL := driver.Rebind(driver.Paginate(q, 0, 500))
	rows, err := db.QueryContext(ctx, limitSQL)
	if err != nil {
		return nil, fmt.Errorf("字典类型查询失败: %w", hintORA(ctx, db, dsRow.DbType, q, err))
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var code, lbl sql.NullString
		if hasLabel {
			if err := rows.Scan(&code, &lbl); err != nil {
				return nil, fmt.Errorf("字典类型读取失败: %w", err)
			}
		} else if err := rows.Scan(&code); err != nil {
			return nil, fmt.Errorf("字典类型读取失败: %w", err)
		}
		// Oracle CHAR 列补空格：trim 后再去空/判等，保证与内容行的类型值口径一致
		code.String = strings.TrimSpace(code.String)
		lbl.String = strings.TrimSpace(lbl.String)
		if code.String == "" {
			continue
		}
		label := lbl.String
		if label == "" || label == code.String {
			label = code.String
		}
		out = append(out, map[string]any{"value": code.String, "label": label})
	}
	return out, rows.Err()
}

// Content 字典内容清单（键值/标签/类型），可按类型筛选（空=全部）——供管理页试跑
func (s *DictService) Content(ctx context.Context, dsSvc *DatasourceService, code, typeFilter string) ([]map[string]any, error) {
	d, err := s.GetByCode(code)
	if err != nil {
		return nil, err
	}
	if d.Status != 1 {
		return nil, fmt.Errorf("字典 %s 已停用", code)
	}
	triplets, err := dictRows(ctx, dsSvc, d)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	tf := strings.TrimSpace(typeFilter)
	for _, t := range triplets {
		if tf != "" && fmt.Sprint(t[2]) != tf {
			continue
		}
		out = append(out, map[string]any{"value": t[0], "label": t[1], "type": t[2]})
	}
	return out, nil
}

// Options 解析字典为下拉选项 [{value,label}]（值=键值，显示=标签），可按类型筛选
func (s *DictService) Options(ctx context.Context, dsSvc *DatasourceService, code, typeFilter string) ([]map[string]any, error) {
	rows, err := s.Content(ctx, dsSvc, code, typeFilter)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// LabelMap 构建 键值→标签 映射（列翻译用），可按类型筛选；结果按 code|typeFilter 缓存 60s
func (s *DictService) LabelMap(ctx context.Context, dsSvc *DatasourceService, code, typeFilter string) (map[string]string, error) {
	key := code + "|" + typeFilter
	if m, ok := labelCacheGet(key); ok {
		return m, nil
	}
	d, err := s.GetByCode(code)
	if err != nil {
		return nil, err
	}
	if d.Status != 1 {
		return nil, fmt.Errorf("字典 %s 已停用", code)
	}
	triplets, err := dictRows(ctx, dsSvc, d)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(triplets))
	tf := strings.TrimSpace(typeFilter)
	for _, t := range triplets {
		if tf != "" && fmt.Sprint(t[2]) != tf {
			continue
		}
		k := fmt.Sprint(t[0])
		if k == "" {
			continue
		}
		m[k] = fmt.Sprint(t[1])
	}
	labelCachePut(key, m)
	return m, nil
}

// Test 管理页试跑：直接按传入的字典定义加载内容（支持未保存的编辑中配置）
func (s *DictService) Test(ctx context.Context, dsSvc *DatasourceService, d *model.Dict) ([]map[string]any, error) {
	if err := s.Validate(d); err != nil {
		return nil, err
	}
	triplets, err := dictRows(ctx, dsSvc, d)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, t := range triplets {
		out = append(out, map[string]any{"value": t[0], "label": t[1], "type": t[2]})
	}
	return out, nil
}
