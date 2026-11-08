package model

import "time"

// Dict 数据字典（表 rpt_dict）：下拉选项与列翻译的集中配置，可复用于多个报表。
// 围绕四个概念：字典类型（分组索引）/ 字典名称（配置名）/ 字典标签（键值显示名）/ 字典键值（内容）。
// 三种来源：
//   separated 分离式——类型表 + 数据表两张表，数据表外键列 → 类型表类型编码列 关联
//   aggregate 聚合式——同一张表中既有字典类型列又有键值/标签列
//   sql       SQL式——自定义查询（最灵活），按结果列名映射类型/键值/标签
type Dict struct {
	ID           int64  `gorm:"primaryKey" json:"id"`
	Code         string `gorm:"size:64;uniqueIndex;not null" json:"code"`
	Name         string `gorm:"size:64;not null" json:"name"`
	SourceType   string `gorm:"size:10;not null;default:'aggregate'" json:"sourceType"` // separated | aggregate | sql
	DatasourceID int64  `gorm:"not null;default:0" json:"datasourceId"`                  // 字典数据所在数据源（0=未配置，保存时校验必填）

	// 分离式：类型表 + 数据表
	TypeTable   string `gorm:"size:128;default:''" json:"typeTable"`  // 类型表
	TypeCodeCol string `gorm:"size:64;default:''" json:"typeCodeCol"` // 类型表-类型编码列
	DataTable   string `gorm:"size:128;default:''" json:"dataTable"`  // 数据表
	DataFKCol   string `gorm:"size:64;default:''" json:"dataFKCol"`   // 数据表-关联字段（→类型编码列）

	// 聚合式 / SQL式
	SrcTable string `gorm:"column:table_name;size:128;default:''" json:"tableName"` // 聚合式表名
	TypeCol  string `gorm:"size:64;default:''" json:"typeCol"`                      // 字典类型列（聚合式/SQL式）

	TypeLabelCol string `gorm:"size:64;default:''" json:"typeLabelCol"` // 字典类型名称列（可选，配置后类型下拉显示 名称（编码））

	ValueCol    string `gorm:"size:64;default:''" json:"valueCol"`     // 字典键值列
	LabelCol    string `gorm:"size:64;default:''" json:"labelCol"`     // 字典标签列
	SQLText     string `gorm:"type:text;default:''" json:"sqlText"`    // SQL式查询语句
	FilterWhere string `gorm:"size:255;default:''" json:"filterWhere"` // 过滤条件（不含 WHERE 关键字）

	Status    int       `gorm:"default:1" json:"status"` // 1启用 0停用
	Remark    string    `gorm:"size:255;default:''" json:"remark"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func (Dict) TableName() string { return "rpt_dict" }
