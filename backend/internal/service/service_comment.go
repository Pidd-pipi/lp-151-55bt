package service

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/gbtreehole/backend/internal/constants"
	"github.com/gbtreehole/backend/internal/model"
	"github.com/gbtreehole/backend/internal/repository"
)

var ErrCommentNotFound = errors.New("comment not found")

type CommentService interface {
	Create(identityID, postID uint, content string) (*model.Comment, []string, bool, error)
	ListByPostID(postID uint, page, pageSize int) ([]model.Comment, int64, error)
	Withdraw(identityID, commentID uint) error
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
		return nil, nil, false, fmt.Errorf("post not published")
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
	return s.comments.ListByPostID(postID, page, pageSize, constants.CommentStatusPublished)
}

// Withdraw 作者撤回评论：从楼层消失、帖子评论数同步减少，并撤下审核队列中的对应条目。
func (s *commentService) Withdraw(identityID, commentID uint) error {
	comment, err := s.comments.FindByID(commentID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return ErrCommentNotFound
		}
		return err
	}
	if comment.IdentityID != identityID {
		return ErrWithdrawForbidden
	}
	if comment.Status == constants.CommentStatusWithdrawn {
		return ErrAlreadyWithdrawn
	}
	// 待审核评论从未计入帖子评论数，撤回时不需要再递减。
	wasPublished := comment.Status == constants.CommentStatusPublished
	comment.Status = constants.CommentStatusWithdrawn
	comment.UpdatedAt = time.Now()
	if err := s.comments.Update(comment); err != nil {
		return fmt.Errorf("withdraw comment %d: %w", commentID, err)
	}
	if wasPublished {
		post, err := s.posts.FindByID(comment.PostID)
		if err != nil {
			if !errors.Is(err, repository.ErrNotFound) {
				return fmt.Errorf("load post for comment withdraw: %w", err)
			}
		} else {
			post.CommentCount--
			if post.CommentCount < 0 {
				post.CommentCount = 0
			}
			post.UpdatedAt = time.Now()
			if err := s.posts.Update(post); err != nil {
				return fmt.Errorf("decrease post comment count: %w", err)
			}
		}
	}
	if err := s.review.WithdrawByTarget("comment", commentID); err != nil {
		s.logger.Error("withdraw comment review item", "commentId", commentID, "error", err)
	}
	return nil
}
