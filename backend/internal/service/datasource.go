package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ce/report-system/backend/internal/datasource"
	"github.com/ce/report-system/backend/internal/model"
	"github.com/ce/report-system/backend/internal/pkg/crypto"
	"gorm.io/gorm"
)

// DatasourceService 数据源管理服务
type DatasourceService struct {
	db     *gorm.DB
	aesKey []byte
	mgr    *datasource.Manager
	audit  *AuditService
}

func NewDatasourceService(db *gorm.DB, aesKey []byte, mgr *datasource.Manager, audit *AuditService) *DatasourceService {
	return &DatasourceService{db: db, aesKey: aesKey, mgr: mgr, audit: audit}
}

// SupportedTypes 返回已注册的数据库类型
func (s *DatasourceService) SupportedTypes() []string {
	return datasource.SupportedTypes()
}

// CreateInput 新建/修改数据源入参
type CreateInput struct {
	Name           string `json:"name" binding:"required"`
	DbType         string `json:"dbType" binding:"required"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	ServiceName    string `json:"serviceName"`
	Username       string `json:"username"`
	Password       string `json:"password"` // 明文传入，加密入库；更新时留空表示不变
	ExtraConn      string `json:"extraConn"`
	PoolMaxOpen    int    `json:"poolMaxOpen"`
	PoolMaxIdle    int    `json:"poolMaxIdle"`
	PoolMaxLifeSec int    `json:"poolMaxLifeSec"`
}

// List 数据源列表（密码不出库）
func (s *DatasourceService) List() ([]model.Datasource, error) {
	var list []model.Datasource
	err := s.db.Order("id DESC").Find(&list).Error
	return list, err
}

// Get 按 ID 获取
func (s *DatasourceService) Get(id int64) (*model.Datasource, error) {
	var ds model.Datasource
	if err := s.db.First(&ds, id).Error; err != nil {
		return nil, err
	}
	return &ds, nil
}

// Create 新建数据源
func (s *DatasourceService) Create(in CreateInput, operator string) (*model.Datasource, error) {
	if in.Password == "" && in.DbType != "sqlite" { // SQLite 无密码
		return nil, errors.New("密码不能为空")
	}
	cipher, err := crypto.Encrypt(in.Password, s.aesKey)
	if err != nil {
		return nil, err
	}
	ds := &model.Datasource{
		Name: in.Name, DbType: in.DbType, Host: in.Host, Port: in.Port,
		ServiceName: in.ServiceName, Username: in.Username,
		PasswordCipher: cipher, ExtraConn: in.ExtraConn,
		PoolMaxOpen: in.PoolMaxOpen, PoolMaxIdle: in.PoolMaxIdle, PoolMaxLifeSec: in.PoolMaxLifeSec,
		Status: 1,
	}
	if err := s.db.Create(ds).Error; err != nil {
		return nil, err
	}
	s.audit.LogAsync(AuditEntry{LogType: "config_change", Username: operator, DatasourceID: &ds.ID, Detail: fmt.Sprintf("新建数据源 %s(%s)", in.Name, in.DbType)})
	return ds, nil
}

// Update 修改数据源；密码留空表示保持不变
func (s *DatasourceService) Update(id int64, in CreateInput, operator string) (*model.Datasource, error) {
	ds, err := s.Get(id)
	if err != nil {
		return nil, err
	}
	if in.Password != "" {
		cipher, err := crypto.Encrypt(in.Password, s.aesKey)
		if err != nil {
			return nil, err
		}
		ds.PasswordCipher = cipher
	}
	ds.Name, ds.DbType, ds.Host, ds.Port = in.Name, in.DbType, in.Host, in.Port
	ds.ServiceName, ds.Username, ds.ExtraConn = in.ServiceName, in.Username, in.ExtraConn
	ds.PoolMaxOpen, ds.PoolMaxIdle, ds.PoolMaxLifeSec = in.PoolMaxOpen, in.PoolMaxIdle, in.PoolMaxLifeSec
	if err := s.db.Save(ds).Error; err != nil {
		return nil, err
	}
	// 配置已变更，连接池失效重建
	s.mgr.Invalidate(id)
	s.audit.LogAsync(AuditEntry{LogType: "config_change", Username: operator, DatasourceID: &id, Detail: fmt.Sprintf("修改数据源 %s", in.Name)})
	return ds, nil
}

// Delete 删除数据源
func (s *DatasourceService) Delete(id int64, operator string) error {
	res := s.db.Delete(&model.Datasource{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	s.mgr.Invalidate(id)
	s.audit.LogAsync(AuditEntry{LogType: "config_change", Username: operator, DatasourceID: &id, Detail: "删除数据源"})
	return nil
}

// connConfig 将数据源记录转为连接配置（解密密码）
func (s *DatasourceService) connConfig(ds *model.Datasource) (datasource.ConnConfig, error) {
	pwd := ""
	if len(ds.PasswordCipher) > 0 {
		var err error
		pwd, err = crypto.Decrypt(ds.PasswordCipher, s.aesKey)
		if err != nil {
			// GCM 校验失败几乎只有一个原因：库里密文不是当前 aes_key 加密的。
			// 常见场景：部署时更换/覆盖过 aes_key（config.yaml 或环境变量 AES_KEY）、
			// 数据库从另一套环境迁移而来、多套环境共用同一个库但密钥不同。
			return datasource.ConnConfig{}, fmt.Errorf(
				"密码解密失败: 数据源「%s」的密码密文与当前 aes_key 不匹配。"+
					"多半是部署后更换过 aes_key（config.yaml 或环境变量 AES_KEY）——"+
					"要么把密钥换回加密时的那把，要么到「数据源管理」重新输入密码保存（会用当前密钥重新加密）",
				ds.Name)
		}
	}
	return datasource.ConnConfig{
		Host: ds.Host, Port: ds.Port, ServiceName: ds.ServiceName,
		Username: ds.Username, Password: pwd, ExtraConn: ds.ExtraConn,
		PoolMaxOpen: ds.PoolMaxOpen, PoolMaxIdle: ds.PoolMaxIdle, PoolMaxLifeSec: ds.PoolMaxLifeSec,
	}, nil
}

// Test 测试数据源连通性（临时连接，不影响连接池）
func (s *DatasourceService) Test(ctx context.Context, id int64) (latencyMs int64, version string, err error) {
	ds, err := s.Get(id)
	if err != nil {
		return 0, "", err
	}
	cfg, err := s.connConfig(ds)
	if err != nil {
		return 0, "", err
	}
	latency, ver, err := datasource.TestConnection(ctx, ds.DbType, cfg)
	s.audit.LogAsync(AuditEntry{LogType: "config_change", DatasourceID: &id, Detail: fmt.Sprintf("连接测试: %s", ver), Success: err == nil})
	return latency, ver, err
}

// TestInput 直接以连接参数测试（保存前使用）
func (s *DatasourceService) TestInput(ctx context.Context, in CreateInput) (latencyMs int64, version string, err error) {
	cfg := datasource.ConnConfig{
		Host: in.Host, Port: in.Port, ServiceName: in.ServiceName,
		Username: in.Username, Password: in.Password, ExtraConn: in.ExtraConn,
		PoolMaxOpen: in.PoolMaxOpen, PoolMaxIdle: in.PoolMaxIdle, PoolMaxLifeSec: in.PoolMaxLifeSec,
	}
	return datasource.TestConnection(ctx, in.DbType, cfg)
}

// GetPool 获取（或建立）数据源连接池，返回连接与数据库版本描述
func (s *DatasourceService) GetPool(ctx context.Context, id int64) (*sql.DB, string, error) {
	ds, err := s.Get(id)
	if err != nil {
		return nil, "", err
	}
	cfg, err := s.connConfig(ds)
	if err != nil {
		return nil, "", err
	}
	return s.mgr.Get(ctx, ds.DbType, cfg, ds.ID)
}
