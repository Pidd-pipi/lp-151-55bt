package repository

import (
	"errors"
	"fmt"
	"time"

	"github.com/gbtreehole/backend/internal/constants"
	"github.com/gbtreehole/backend/internal/model"
	"gorm.io/gorm"
)

type ReviewQueueRepository interface {
	Create(item *model.ReviewQueue) error
	Update(item *model.ReviewQueue) error
	FindByID(id uint) (*model.ReviewQueue, error)
	List(page, pageSize int, status int) ([]model.ReviewQueue, int64, error)
	WithdrawPending(targetType string, targetID uint) (int64, error)
}

type reviewQueueRepository struct {
	db *gorm.DB
}

func NewReviewQueueRepository(db *gorm.DB) ReviewQueueRepository {
	return &reviewQueueRepository{db: db}
}

func (r *reviewQueueRepository) Create(item *model.ReviewQueue) error {
	if err := r.db.Create(item).Error; err != nil {
		return fmt.Errorf("create review queue: %w", err)
	}
	return nil
}

func (r *reviewQueueRepository) Update(item *model.ReviewQueue) error {
	if err := r.db.Save(item).Error; err != nil {
		return fmt.Errorf("update review queue: %w", err)
	}
	return nil
}

func (r *reviewQueueRepository) FindByID(id uint) (*model.ReviewQueue, error) {
	var item model.ReviewQueue
	if err := r.db.First(&item, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find review queue by id: %w", err)
	}
	return &item, nil
}

func (r *reviewQueueRepository) List(page, pageSize int, status int) ([]model.ReviewQueue, int64, error) {
	var items []model.ReviewQueue
	var total int64
	q := r.db.Model(&model.ReviewQueue{})
	if status > 0 {
		q = q.Where("status = ?", status)
	}
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("count review queue: %w", err)
	}
	if err := q.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error; err != nil {
		return nil, 0, fmt.Errorf("list review queue: %w", err)
	}
	return items, total, nil
}

// WithdrawPending 将目标内容仍处于待审状态的审核条目标记为作者撤回，并返回受影响行数。
// 已审结（放行/屏蔽）的条目保留历史，不再随撤回变动。
func (r *reviewQueueRepository) WithdrawPending(targetType string, targetID uint) (int64, error) {
	result := r.db.Model(&model.ReviewQueue{}).
		Where("target_type = ? AND target_id = ? AND status = ?", targetType, targetID, constants.ReviewStatusPending).
		Updates(map[string]any{
			"status":     constants.ReviewStatusWithdrawn,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return 0, fmt.Errorf("withdraw pending review: %w", result.Error)
	}
	return result.RowsAffected, nil
}
