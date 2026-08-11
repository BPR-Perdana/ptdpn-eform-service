package repository

import (
	"context"
	"errors"
	"fmt"

	"github.com/cappyHoding/ptdpn-eform-service/internal/model"
	"gorm.io/gorm"
)

// ConfigRepository defines operations for system configuration.
type ConfigRepository interface {
	List(ctx context.Context) ([]model.SystemConfig, error)
	FindPublic(ctx context.Context) ([]model.SystemConfig, error)
	FindByKey(ctx context.Context, key string) (*model.SystemConfig, error)
	Upsert(ctx context.Context, cfg *model.SystemConfig) error
}

type configRepository struct {
	db *gorm.DB
}

func NewConfigRepository(db *gorm.DB) ConfigRepository {
	return &configRepository{db: db}
}

func (r *configRepository) List(ctx context.Context) ([]model.SystemConfig, error) {
	var configs []model.SystemConfig
	err := r.db.WithContext(ctx).
		Where("deleted_at IS NULL").
		Order("config_key ASC").
		Find(&configs).Error
	if err != nil {
		return nil, fmt.Errorf("list config failed: %w", err)
	}
	return configs, nil
}

func (r *configRepository) FindPublic(ctx context.Context) ([]model.SystemConfig, error) {
	var configs []model.SystemConfig
	err := r.db.WithContext(ctx).
		Where("is_public = ? AND deleted_at IS NULL", 1).
		Order("config_key ASC").
		Find(&configs).Error
	if err != nil {
		return nil, fmt.Errorf("find public config failed: %w", err)
	}
	return configs, nil
}

func (r *configRepository) FindByKey(ctx context.Context, key string) (*model.SystemConfig, error) {
	var cfg model.SystemConfig
	err := r.db.WithContext(ctx).
		Where("config_key = ? AND deleted_at IS NULL", key).
		First(&cfg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find config failed: %w", err)
	}
	return &cfg, nil
}

func (r *configRepository) Upsert(ctx context.Context, cfg *model.SystemConfig) error {
	err := r.db.WithContext(ctx).
		Where("config_key = ?", cfg.ConfigKey).
		Assign(model.SystemConfig{
			ConfigValue: cfg.ConfigValue,
			UpdatedBy:   cfg.UpdatedBy,
		}).
		FirstOrCreate(cfg).Error
	if err != nil {
		return fmt.Errorf("upsert config failed: %w", err)
	}
	// If it already existed, update the value
	return r.db.WithContext(ctx).
		Model(&model.SystemConfig{}).
		Where("config_key = ?", cfg.ConfigKey).
		Updates(map[string]interface{}{
			"config_value": cfg.ConfigValue,
			"updated_by":   cfg.UpdatedBy,
		}).Error
}
