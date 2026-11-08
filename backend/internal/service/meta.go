package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/ce/report-system/backend/internal/datasource"
	"gorm.io/gorm"
)

// MetaService 元数据扫描服务（表/字段清单，供 SQL 编写与条件配置使用）
type MetaService struct {
	ds *DatasourceService
}

func NewMetaService(ds *DatasourceService) *MetaService {
	return &MetaService{ds: ds}
}

// Tables 表（含视图）清单；keyword 模糊匹配表名
func (s *MetaService) Tables(ctx context.Context, datasourceID int64, keyword string) ([]datasource.TableMeta, error) {
	db, _, err := s.ds.GetPool(ctx, datasourceID)
	if err != nil {
		return nil, err
	}
	driver, err := s.driverOf(datasourceID)
	if err != nil {
		return nil, err
	}
	list, err := driver.ListTables(ctx, db, strings.TrimSpace(keyword))
	if err != nil {
		return nil, fmt.Errorf("表清单扫描失败: %w", err)
	}
	return list, nil
}

// Columns 指定表的字段清单
func (s *MetaService) Columns(ctx context.Context, datasourceID int64, table string) ([]datasource.ColumnMeta, error) {
	table = strings.TrimSpace(table)
	if table == "" {
		return nil, errors.New("表名不能为空")
	}
	db, _, err := s.ds.GetPool(ctx, datasourceID)
	if err != nil {
		return nil, err
	}
	driver, err := s.driverOf(datasourceID)
	if err != nil {
		return nil, err
	}
	list, err := driver.ListColumns(ctx, db, table)
	if err != nil {
		return nil, fmt.Errorf("字段扫描失败: %w", err)
	}
	return list, nil
}

func (s *MetaService) driverOf(datasourceID int64) (datasource.Driver, error) {
	ds, err := s.ds.Get(datasourceID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("数据源不存在")
		}
		return nil, err
	}
	return datasource.Get(ds.DbType)
}
