package service

import (
	"errors"

	"log/slog"

	"github.com/gbtreehole/backend/internal/constants"
	"github.com/gbtreehole/backend/internal/model"
	"github.com/gbtreehole/backend/internal/repository"
)

type LikeService interface {
	Toggle(identityID uint, targetType string, targetID uint) (bool, int64, error)
	IsLiked(identityID uint, targetType string, targetIDs []uint) (map[uint]bool, error)
}

type likeService struct {
	likes    repository.LikeRepository
	posts    repository.PostRepository
	comments repository.CommentRepository
	logger   *slog.Logger
}

func NewLikeService(likes repository.LikeRepository, posts repository.PostRepository, comments repository.CommentRepository, logger *slog.Logger) LikeService {
	return &likeService{likes: likes, posts: posts, comments: comments, logger: logger}
}

func (s *likeService) Toggle(identityID uint, targetType string, targetID uint) (bool, int64, error) {
	existing, err := s.likes.Find(identityID, targetType, targetID)
	if err == nil {
		if err := s.likes.Delete(existing.ID); err != nil {
			return false, 0, err
		}
		s.adjustCount(targetType, targetID, -1)
		count, _ := s.likes.CountByTarget(targetType, targetID)
		return false, count, nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return false, 0, err
	}
	// 已撤回的内容不能再点赞。
	if withdrawn, err := s.isTargetWithdrawn(targetType, targetID); err != nil {
		return false, 0, err
	} else if withdrawn {
		return false, 0, ErrTargetWithdrawn
	}
	like := &model.Like{IdentityID: identityID, TargetType: targetType, TargetID: targetID}
	if err := s.likes.Create(like); err != nil {
		return false, 0, err
	}
	s.adjustCount(targetType, targetID, 1)
	count, err := s.likes.CountByTarget(targetType, targetID)
	if err != nil {
		return false, 0, err
	}
	return true, count, nil
}

// isTargetWithdrawn 判断目标帖子/评论是否已撤回；目标不存在时不允许点赞。
func (s *likeService) isTargetWithdrawn(targetType string, targetID uint) (bool, error) {
	switch targetType {
	case "post":
		post, err := s.posts.FindByID(targetID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return true, nil
			}
			return false, err
		}
		return post.Status == constants.PostStatusWithdrawn, nil
	case "comment":
		comment, err := s.comments.FindByID(targetID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return true, nil
			}
			return false, err
		}
		return comment.Status == constants.CommentStatusWithdrawn, nil
	default:
		return false, nil
	}
}

func (s *likeService) adjustCount(targetType string, targetID uint, delta int) {
	switch targetType {
	case "post":
		post, err := s.posts.FindByID(targetID)
		if err != nil {
			s.logger.Error("adjust post like count", "error", err)
			return
		}
		post.LikeCount += delta
		if post.LikeCount < 0 {
			post.LikeCount = 0
		}
		if err := s.posts.Update(post); err != nil {
			s.logger.Error("update post like count", "error", err)
		}
	case "comment":
		comment, err := s.comments.FindByID(targetID)
		if err != nil {
			s.logger.Error("adjust comment like count", "error", err)
			return
		}
		comment.LikeCount += delta
		if comment.LikeCount < 0 {
			comment.LikeCount = 0
		}
		if err := s.comments.Update(comment); err != nil {
			s.logger.Error("update comment like count", "error", err)
		}
	}
}

func (s *likeService) IsLiked(identityID uint, targetType string, targetIDs []uint) (map[uint]bool, error) {
	return s.likes.IsLiked(identityID, targetType, targetIDs)
}
