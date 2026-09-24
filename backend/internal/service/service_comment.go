package service

import (
	"errors"
	"log/slog"
	"time"

	"github.com/gbtreehole/backend/internal/constants"
	"github.com/gbtreehole/backend/internal/model"
	"github.com/gbtreehole/backend/internal/repository"
)

var (
	ErrCommentNotFound  = errors.New("comment not found")
	ErrCommentWithdrawn = errors.New("comment already withdrawn")
)

type CommentService interface {
	Create(identityID, postID uint, content string) (*model.Comment, []string, bool, error)
	ListByPostID(postID uint, page, pageSize int) ([]model.Comment, int64, error)
	// Withdraw 作者撤回自己的评论：楼层消失、帖子评论数减少、审核条目录下。
	Withdraw(id, identityID uint) error
}

type commentService struct {
	comments  repository.CommentRepository
	posts     repository.PostRepository
	sensitive SensitiveWordService
	review    ReviewService
	logger    *slog.Logger
}

func NewCommentService(comments repository.CommentRepository, posts repository.PostRepository, sensitive SensitiveWordService, review ReviewService, logger *slog.Logger) CommentService {
	return &commentService{comments: comments, posts: posts, sensitive: sensitive, review: review, logger: logger}
}

func (s *commentService) Create(identityID, postID uint, content string) (*model.Comment, []string, bool, error) {
	post, err := s.posts.FindByID(postID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, nil, false, ErrPostNotFound
		}
		return nil, nil, false, err
	}
	if post.Status != constants.PostStatusPublished {
		return nil, nil, false, ErrPostNotFound
	}
	hits, blocked := s.sensitive.Detect(content)
	comment := &model.Comment{
		PostID:     postID,
		IdentityID: identityID,
		Content:    content,
		Status:     constants.CommentStatusPublished,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if blocked {
		comment.Status = constants.CommentStatusPending
	}
	if err := s.comments.Create(comment); err != nil {
		return nil, hits, blocked, err
	}
	if !blocked {
		post.CommentCount++
		post.UpdatedAt = time.Now()
		if err := s.posts.Update(post); err != nil {
			s.logger.Error("update post comment count", "error", err)
		}
	} else {
		if err := s.review.Enqueue("comment", comment.ID, content, hits); err != nil {
			s.logger.Error("enqueue comment review", "error", err)
		}
	}
	return comment, hits, blocked, nil
}

func (s *commentService) ListByPostID(postID uint, page, pageSize int) ([]model.Comment, int64, error) {
	post, err := s.posts.FindByID(postID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, 0, ErrPostNotFound
		}
		return nil, 0, err
	}
	if post.Status != constants.PostStatusPublished {
		return nil, 0, ErrPostNotFound
	}
	return s.comments.ListByPostID(postID, page, pageSize, constants.CommentStatusPublished)
}

// Withdraw 作者撤回自己的评论。
// 非作者拿到编号也不能撤回；已经撤回的重复操作返回 ErrCommentWithdrawn。
// 只有已上架的评论计入过帖子评论数，撤回时才递减，待审评论直接撤下即可。
func (s *commentService) Withdraw(id, identityID uint) error {
	comment, err := s.comments.FindByID(id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrCommentNotFound
		}
		return err
	}
	if comment.IdentityID != identityID {
		return ErrCommentNotOwner
	}
	if comment.Status == constants.CommentStatusWithdrawn {
		return ErrCommentWithdrawn
	}
	wasPublished := comment.Status == constants.CommentStatusPublished
	if _, err := s.comments.Withdraw(id); err != nil {
		return err
	}
	if wasPublished {
		if err := s.posts.DecrementCommentCount(comment.PostID); err != nil {
			s.logger.Error("decrement post comment count on withdraw", "commentId", id, "error", err)
		}
	}
	if err := s.review.WithdrawPending("comment", id); err != nil {
		s.logger.Error("withdraw comment review queue item", "commentId", id, "error", err)
	}
	return nil
}
