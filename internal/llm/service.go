// Package llm 中的 ModelService 是模型配置的业务层：
// 封装参数校验、默认模型规则、API Key 脱敏与环境变量展开。
// 读路径每次直查数据库（无缓存），保证 WebUI 增删改立即生效，
// 多节点共享 MySQL/PG 时天然一致。
package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

var (
	ErrModelNotFound    = errors.New("模型不存在")
	ErrModelDisabled    = errors.New("模型已禁用")
	ErrNoDefaultModel   = errors.New("尚未配置模型，请在设置中创建模型")
	ErrNameExists       = errors.New("模型名称已存在")
	ErrDefaultProtected = errors.New("默认模型不允许删除或禁用，请先将其他模型设为默认")
	ErrInvalidModel     = errors.New("模型配置无效")
)

// ModelService 模型配置业务层
type ModelService struct {
	repo repo.ModelRepo
}

func NewModelService(r repo.ModelRepo) *ModelService {
	return &ModelService{repo: r}
}

// GetByName 按名称获取可用模型；name 为空时返回默认模型。
// APIKey 中的 ${ENV_VAR} 引用会被展开。禁用的模型返回 ErrModelDisabled。
func (s *ModelService) GetByName(ctx context.Context, name string) (*repo.Model, error) {
	var m *repo.Model
	var err error
	if name == "" {
		m, err = s.repo.GetDefault(ctx)
		if errors.Is(err, repo.ErrNotFound) {
			return nil, ErrNoDefaultModel
		}
	} else {
		m, err = s.repo.GetByName(ctx, name)
		if errors.Is(err, repo.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrModelNotFound, name)
		}
	}
	if err != nil {
		return nil, err
	}
	if !m.Enabled {
		return nil, fmt.Errorf("%w: %s", ErrModelDisabled, m.Name)
	}
	m.APIKey = config.ExpandEnv(m.APIKey)
	return m, nil
}

// GetStored 按名称获取模型（不检查 enabled，APIKey 展开环境变量）。
// 供连接测试等管理场景使用。
func (s *ModelService) GetStored(ctx context.Context, name string) (*repo.Model, error) {
	m, err := s.repo.GetByName(ctx, name)
	if errors.Is(err, repo.ErrNotFound) {
		return nil, fmt.Errorf("%w: %s", ErrModelNotFound, name)
	}
	if err != nil {
		return nil, err
	}
	m.APIKey = config.ExpandEnv(m.APIKey)
	return m, nil
}

// List 返回全部模型（APIKey 保持库中原文，由调用方决定脱敏方式）。
func (s *ModelService) List(ctx context.Context) ([]*repo.Model, error) {
	return s.repo.List(ctx)
}

// Create 创建模型。库中没有任何模型时，新模型自动成为默认模型并强制启用。
// 不修改调用方传入的 m（内部拷贝后写库），因此调用方拿不到写入后的时间戳/默认标记。
func (s *ModelService) Create(ctx context.Context, m *repo.Model) error {
	if err := validateModel(m); err != nil {
		return err
	}
	if _, err := s.repo.GetByName(ctx, m.Name); err == nil {
		return fmt.Errorf("%w: %s", ErrNameExists, m.Name)
	} else if !errors.Is(err, repo.ErrNotFound) {
		return err
	}
	n, err := s.repo.Count(ctx)
	if err != nil {
		return err
	}
	rec := *m
	if n == 0 {
		rec.IsDefault = true
		rec.Enabled = true
	}
	now := time.Now()
	rec.CreatedAt = now
	rec.UpdatedAt = now
	return s.repo.Create(ctx, &rec)
}

// Update 按原名称 name 更新模型。m.APIKey 为空表示保持库中原值；
// 允许重命名（m.Name != name），新名称冲突返回 ErrNameExists；
// 默认模型不允许禁用（is_default 本身不通过 Update 修改）。
// 不修改调用方传入的 m（内部拷贝后写库），避免明文 APIKey 回流到调用方对象。
func (s *ModelService) Update(ctx context.Context, name string, m *repo.Model) error {
	existing, err := s.repo.GetByName(ctx, name)
	if errors.Is(err, repo.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrModelNotFound, name)
	}
	if err != nil {
		return err
	}
	upd := *m
	if upd.APIKey == "" {
		upd.APIKey = existing.APIKey
	}
	upd.IsDefault = existing.IsDefault
	if err := validateModel(&upd); err != nil {
		return err
	}
	if existing.IsDefault && !upd.Enabled {
		return ErrDefaultProtected
	}
	if upd.Name != name {
		if _, err := s.repo.GetByName(ctx, upd.Name); err == nil {
			return fmt.Errorf("%w: %s", ErrNameExists, upd.Name)
		} else if !errors.Is(err, repo.ErrNotFound) {
			return err
		}
	}
	return s.repo.Update(ctx, name, &upd)
}

// Delete 删除模型；默认模型返回 ErrDefaultProtected。
func (s *ModelService) Delete(ctx context.Context, name string) error {
	existing, err := s.repo.GetByName(ctx, name)
	if errors.Is(err, repo.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrModelNotFound, name)
	}
	if err != nil {
		return err
	}
	if existing.IsDefault {
		return ErrDefaultProtected
	}
	return s.repo.Delete(ctx, name)
}

// SetDefault 把指定模型设为默认；禁用的模型返回 ErrModelDisabled。
func (s *ModelService) SetDefault(ctx context.Context, name string) error {
	m, err := s.repo.GetByName(ctx, name)
	if errors.Is(err, repo.ErrNotFound) {
		return fmt.Errorf("%w: %s", ErrModelNotFound, name)
	}
	if err != nil {
		return err
	}
	if !m.Enabled {
		return fmt.Errorf("%w: %s", ErrModelDisabled, name)
	}
	return s.repo.SetDefault(ctx, name)
}

// validateModel 校验必填字段与参数范围。
func validateModel(m *repo.Model) error {
	if strings.TrimSpace(m.Name) == "" {
		return fmt.Errorf("%w: 名称不能为空", ErrInvalidModel)
	}
	if m.BaseURL == "" {
		return fmt.Errorf("%w: base_url 不能为空", ErrInvalidModel)
	}
	if m.APIKey == "" {
		return fmt.Errorf("%w: api_key 不能为空", ErrInvalidModel)
	}
	if m.Model == "" {
		return fmt.Errorf("%w: model 不能为空", ErrInvalidModel)
	}
	if m.Temperature < 0.0 || m.Temperature > 2.0 {
		return fmt.Errorf("%w: temperature 超出范围 %.1f（有效范围 0.0~2.0）", ErrInvalidModel, m.Temperature)
	}
	if m.TopP < 0.0 || m.TopP > 1.0 {
		return fmt.Errorf("%w: top_p 超出范围 %.1f（有效范围 0.0~1.0）", ErrInvalidModel, m.TopP)
	}
	if m.FrequencyPenalty < -2.0 || m.FrequencyPenalty > 2.0 {
		return fmt.Errorf("%w: frequency_penalty 超出范围 %.1f（有效范围 -2.0~2.0）", ErrInvalidModel, m.FrequencyPenalty)
	}
	if m.PresencePenalty < -2.0 || m.PresencePenalty > 2.0 {
		return fmt.Errorf("%w: presence_penalty 超出范围 %.1f（有效范围 -2.0~2.0）", ErrInvalidModel, m.PresencePenalty)
	}
	return nil
}

// MaskAPIKey 返回脱敏后的 API Key：只保留尾 4 位；
// ${ENV_VAR} 环境变量引用不是机密，原样返回。
func MaskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if strings.HasPrefix(key, "${") && strings.HasSuffix(key, "}") {
		return key
	}
	if len(key) <= 8 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}
