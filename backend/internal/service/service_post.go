package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gbtreehole/backend/internal/constants"
	"github.com/gbtreehole/backend/internal/model"
	"github.com/gbtreehole/backend/internal/repository"
)

var (
	ErrPostNotFound    = errors.New("post not found")
	ErrPostNotOwner    = errors.New("post not owned by identity")
	ErrPostWithdrawn   = errors.New("post already withdrawn")
	ErrContentUnavail  = errors.New("content not available")
	ErrCommentNotOwner = errors.New("comment not owned by identity")
)

type PostService interface {
	Create(identityID uint, title, content string, images []string, tagNames []string) (*model.Post, []string, bool, error)
	GetByID(id uint) (*model.Post, error)
	List(page, pageSize int, tagID uint, featured bool) ([]model.Post, int64, error)
	ListHot(limit int) ([]model.Post, error)
	ListFeatured(limit int) ([]model.Post, error)
	DailyFeatured(limit int) ([]model.Post, error)
	IncrementView(id uint) error
	SetFeatured(id uint, featured bool) error
	// Withdraw 作者撤回自己的帖子：退出列表与精选、详情不可再访问，并撤下审核条目。
	Withdraw(id, identityID uint) error
}

type postService struct {
	posts     repository.PostRepository
	tags      TagService
	sensitive SensitiveWordService
	review    ReviewService
	logger    *slog.Logger
}

func NewPostService(posts repository.PostRepository, tags TagService, sensitive SensitiveWordService, review ReviewService, logger *slog.Logger) PostService {
	return &postService{posts: posts, tags: tags, sensitive: sensitive, review: review, logger: logger}
}

func (s *postService) Create(identityID uint, title, content string, images []string, tagNames []string) (*model.Post, []string, bool, error) {
	hits, blocked := s.sensitive.Detect(title + " " + content)
	post := &model.Post{
		IdentityID: identityID,
		Title:      title,
		Content:    content,
		Status:     constants.PostStatusPublished,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if len(images) > 0 {
		imgBytes, err := json.Marshal(images)
		if err != nil {
			return nil, hits, blocked, fmt.Errorf("marshal images: %w", err)
		}
		post.Images = string(imgBytes)
	}
	if blocked {
		post.Status = constants.PostStatusPending
	}
	var tags []model.Tag
	if len(tagNames) > 0 {
		for _, name := range tagNames {
			tag, err := s.tags.GetOrCreate(name)
			if err != nil {
				return nil, hits, blocked, err
			}
			tags = append(tags, *tag)
		}
	}
	post.Tags = tags
	if err := s.posts.Create(post); err != nil {
		return nil, hits, blocked, err
	}
	for _, tag := range tags {
		if err := s.tags.IncCount(tag.ID); err != nil {
			s.logger.Error("increment tag count", "error", err)
		}
	}
	if blocked {
		if err := s.review.Enqueue("post", post.ID, title+" "+content, hits); err != nil {
			s.logger.Error("enqueue post review", "error", err)
		}
	}
	return post, hits, blocked, nil
}

func (s *postService) GetByID(id uint) (*model.Post, error) {
	post, err := s.posts.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrPostNotFound
		}
		return nil, err
	}
	// 撤回（以及待审/屏蔽）的帖子详情不再可访问，对所有访问者一视同仁。
	if post.Status != constants.PostStatusPublished {
		return nil, ErrPostNotFound
	}
	return post, nil
}

func (s *postService) List(page, pageSize int, tagID uint, featured bool) ([]model.Post, int64, error) {
	return s.posts.List(page, pageSize, constants.PostStatusPublished, featured, tagID)
}

func (s *postService) ListHot(limit int) ([]model.Post, error) {
	return s.posts.ListHot(limit)
}

func (s *postService) ListFeatured(limit int) ([]model.Post, error) {
	return s.posts.ListFeatured(limit)
}

// DailyFeatured 结合手动精选与热度自动筛选今日内容。
func (s *postService) DailyFeatured(limit int) ([]model.Post, error) {
	featured, err := s.posts.ListFeatured(limit)
	if err != nil {
		return nil, err
	}
	if len(featured) >= limit {
		return featured, nil
	}
	hot, err := s.posts.ListHot(limit * 2)
	if err != nil {
		return nil, err
	}
	seen := make(map[uint]bool)
	for _, p := range featured {
		seen[p.ID] = true
	}
	result := featured
	for _, p := range hot {
		if seen[p.ID] {
			continue
		}
		if len(result) >= limit {
			break
		}
		result = append(result, p)
	}
	return result, nil
}

func (s *postService) IncrementView(id uint) error {
	return s.posts.IncrementView(id)
}

func (s *postService) SetFeatured(id uint, featured bool) error {
	post, err := s.posts.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrPostNotFound
		}
		return err
	}
	// 已撤回的帖子不能再被手动精选；取消精选对它无意义但无害，放行即可。
	if featured && post.Status == constants.PostStatusWithdrawn {
		return ErrPostNotFound
	}
	post.IsFeatured = featured
	if featured {
		now := time.Now()
		post.FeaturedAt = &now
	} else {
		post.FeaturedAt = nil
	}
	post.UpdatedAt = time.Now()
	return s.posts.Update(post)
}

// Withdraw 作者撤回自己的帖子。
// 非作者拿到编号也不能撤回；已经撤回的重复操作返回 ErrPostWithdrawn。
func (s *postService) Withdraw(id, identityID uint) error {
	post, err := s.posts.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrPostNotFound
		}
		return err
	}
	if post.IdentityID != identityID {
		return ErrPostNotOwner
	}
	if post.Status == constants.PostStatusWithdrawn {
		return ErrPostWithdrawn
	}
	if _, err := s.posts.Withdraw(id); err != nil {
		return err
	}
	if err := s.review.WithdrawPending("post", id); err != nil {
		s.logger.Error("withdraw post review queue item", "postId", id, "error", err)
	}
	return nil
}
