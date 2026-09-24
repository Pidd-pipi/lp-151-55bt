package service

import (
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/gbtreehole/backend/internal/constants"
	"github.com/gbtreehole/backend/internal/model"
	"github.com/gbtreehole/backend/internal/repository"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newWithdrawDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&model.UserIdentity{}, &model.Tag{}, &model.PostTag{}, &model.Post{},
		&model.Comment{}, &model.Like{}, &model.ReviewQueue{}, &model.SensitiveWord{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func newWithdrawServices(t *testing.T) (*gorm.DB, PostService, CommentService, LikeService, repository.PostRepository, repository.CommentRepository, repository.ReviewQueueRepository) {
	t.Helper()
	db := newWithdrawDB(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	postRepo := repository.NewPostRepository(db)
	commentRepo := repository.NewCommentRepository(db)
	tagRepo := repository.NewTagRepository(db)
	likeRepo := repository.NewLikeRepository(db)
	sensitiveRepo := repository.NewSensitiveWordRepository(db)
	reviewRepo := repository.NewReviewQueueRepository(db)

	tagSvc := NewTagService(tagRepo)
	sensitiveSvc := NewSensitiveWordService(sensitiveRepo)
	reviewSvc := NewReviewService(reviewRepo, postRepo, commentRepo, logger)
	postSvc := NewPostService(postRepo, tagSvc, sensitiveSvc, reviewSvc, logger)
	commentSvc := NewCommentService(commentRepo, postRepo, sensitiveSvc, reviewSvc, logger)
	likeSvc := NewLikeService(likeRepo, postRepo, commentRepo, logger)
	return db, postSvc, commentSvc, likeSvc, postRepo, commentRepo, reviewRepo
}

func createIdentity(t *testing.T, db *gorm.DB, key string) *model.UserIdentity {
	t.Helper()
	identity := &model.UserIdentity{IdentityKey: key, Nickname: "匿名-" + key, Avatar: "", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(identity).Error; err != nil {
		t.Fatalf("create identity: %v", err)
	}
	return identity
}

func createPostRow(t *testing.T, db *gorm.DB, identityID uint, status, commentCount int) *model.Post {
	t.Helper()
	post := &model.Post{
		IdentityID:   identityID,
		Content:      "树洞内容",
		Status:       status,
		CommentCount: commentCount,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := db.Create(post).Error; err != nil {
		t.Fatalf("create post: %v", err)
	}
	return post
}

func TestWithdrawPost(t *testing.T) {
	db, postSvc, _, _, postRepo, _, reviewRepo := newWithdrawServices(t)

	owner := createIdentity(t, db, "owner")
	other := createIdentity(t, db, "other")
	post := createPostRow(t, db, owner.ID, constants.PostStatusPublished, 0)
	featuredAt := time.Now()
	post.IsFeatured = true
	post.FeaturedAt = &featuredAt
	if err := postRepo.Update(post); err != nil {
		t.Fatalf("mark featured: %v", err)
	}

	// 别人拿到编号也不能替作者撤回。
	if err := postSvc.Withdraw(post.ID, other.ID); err != ErrPostNotOwner {
		t.Fatalf("expected ErrPostNotOwner, got %v", err)
	}

	// 作者本人可以撤回。
	if err := postSvc.Withdraw(post.ID, owner.ID); err != nil {
		t.Fatalf("withdraw post: %v", err)
	}

	// 重复操作提示已经处理。
	if err := postSvc.Withdraw(post.ID, owner.ID); err != ErrPostWithdrawn {
		t.Fatalf("expected ErrPostWithdrawn, got %v", err)
	}

	got, err := postRepo.FindByID(post.ID)
	if err != nil {
		t.Fatalf("find post: %v", err)
	}
	if got.Status != constants.PostStatusWithdrawn {
		t.Fatalf("status = %d, want withdrawn", got.Status)
	}
	if got.IsFeatured {
		t.Fatalf("withdrawn post should leave featured list")
	}

	// 详情不再能访问。
	if _, err := postSvc.GetByID(post.ID); err != ErrPostNotFound {
		t.Fatalf("expected ErrPostNotFound after withdraw, got %v", err)
	}

	// 退出最新列表（热度/精选列表在仓储层同样以 status = 1 过滤）。
	posts, total, err := postSvc.List(1, 10, 0, false)
	if err != nil {
		t.Fatalf("list posts: %v", err)
	}
	if total != 0 || len(posts) != 0 {
		t.Fatalf("withdrawn post still in latest list: total=%d len=%d", total, len(posts))
	}
	if featured, err := postSvc.ListFeatured(10); err != nil || len(featured) != 0 {
		t.Fatalf("withdrawn post still in featured list: %v %v", featured, err)
	}

	// 审核队列里仍待审的对应条目一并撤下。
	pending := &model.Post{IdentityID: owner.ID, Content: "待审帖", Status: constants.PostStatusPending, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := db.Create(pending).Error; err != nil {
		t.Fatalf("create pending post: %v", err)
	}
	item := &model.ReviewQueue{TargetType: "post", TargetID: pending.ID, Status: constants.ReviewStatusPending}
	if err := reviewRepo.Create(item); err != nil {
		t.Fatalf("create review item: %v", err)
	}
	if err := postSvc.Withdraw(pending.ID, owner.ID); err != nil {
		t.Fatalf("withdraw pending post: %v", err)
	}
	updated, err := reviewRepo.FindByID(item.ID)
	if err != nil {
		t.Fatalf("find review item: %v", err)
	}
	if updated.Status != constants.ReviewStatusWithdrawn {
		t.Fatalf("review status = %d, want withdrawn", updated.Status)
	}
}

func TestWithdrawComment(t *testing.T) {
	db, _, commentSvc, likeSvc, postRepo, commentRepo, reviewRepo := newWithdrawServices(t)

	owner := createIdentity(t, db, "c-owner")
	other := createIdentity(t, db, "c-other")
	post := createPostRow(t, db, other.ID, constants.PostStatusPublished, 1)
	comment := &model.Comment{
		PostID:     post.ID,
		IdentityID: owner.ID,
		Content:    "我的评论",
		Status:     constants.CommentStatusPublished,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	if err := db.Create(comment).Error; err != nil {
		t.Fatalf("create comment: %v", err)
	}

	// 非作者不能撤回。
	if err := commentSvc.Withdraw(comment.ID, other.ID); err != ErrCommentNotOwner {
		t.Fatalf("expected ErrCommentNotOwner, got %v", err)
	}

	// 已撤回内容不能再点赞（撤回前先校验可用目标的反向情形）。
	if _, _, err := likeSvc.Toggle(other.ID, "comment", comment.ID); err != nil {
		t.Fatalf("like published comment before withdraw should work: %v", err)
	}

	if err := commentSvc.Withdraw(comment.ID, owner.ID); err != nil {
		t.Fatalf("withdraw comment: %v", err)
	}

	// 重复撤回。
	if err := commentSvc.Withdraw(comment.ID, owner.ID); err != ErrCommentWithdrawn {
		t.Fatalf("expected ErrCommentWithdrawn, got %v", err)
	}

	// 评论从楼层消失。
	list, total, err := commentSvc.ListByPostID(post.ID, 1, 10)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	if total != 0 || len(list) != 0 {
		t.Fatalf("withdrawn comment still on floor: total=%d len=%d", total, len(list))
	}

	// 帖子评论数一并减少。
	updatedPost, err := postRepo.FindByID(post.ID)
	if err != nil {
		t.Fatalf("find post: %v", err)
	}
	if updatedPost.CommentCount != 0 {
		t.Fatalf("comment count = %d, want 0", updatedPost.CommentCount)
	}

	// 已撤回评论不能再点赞。
	if _, _, err := likeSvc.Toggle(other.ID, "comment", comment.ID); err != ErrContentUnavail {
		t.Fatalf("expected ErrContentUnavail, got %v", err)
	}

	// 撤回评论撤下对应待审条目；待审评论未计入评论数，撤回不应让计数变负。
	pendingComment := &model.Comment{
		PostID: post.ID, IdentityID: owner.ID, Content: "待审评论",
		Status: constants.CommentStatusPending, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	if err := db.Create(pendingComment).Error; err != nil {
		t.Fatalf("create pending comment: %v", err)
	}
	pendingItem := &model.ReviewQueue{TargetType: "comment", TargetID: pendingComment.ID, Status: constants.ReviewStatusPending}
	if err := reviewRepo.Create(pendingItem); err != nil {
		t.Fatalf("create review item: %v", err)
	}
	if err := commentSvc.Withdraw(pendingComment.ID, owner.ID); err != nil {
		t.Fatalf("withdraw pending comment: %v", err)
	}
	updatedItem, err := reviewRepo.FindByID(pendingItem.ID)
	if err != nil {
		t.Fatalf("find review item: %v", err)
	}
	if updatedItem.Status != constants.ReviewStatusWithdrawn {
		t.Fatalf("review status = %d, want withdrawn", updatedItem.Status)
	}
	gotComment, err := commentRepo.FindByID(pendingComment.ID)
	if err != nil {
		t.Fatalf("find pending comment: %v", err)
	}
	if gotComment.Status != constants.CommentStatusWithdrawn {
		t.Fatalf("pending comment status = %d, want withdrawn", gotComment.Status)
	}
	updatedPost, _ = postRepo.FindByID(post.ID)
	if updatedPost.CommentCount != 0 {
		t.Fatalf("comment count = %d, pending withdraw must not change count", updatedPost.CommentCount)
	}
}

func TestCannotLikeWithdrawnPost(t *testing.T) {
	db, postSvc, _, likeSvc, _, _, _ := newWithdrawServices(t)

	owner := createIdentity(t, db, "like-owner")
	other := createIdentity(t, db, "like-other")
	post := createPostRow(t, db, owner.ID, constants.PostStatusPublished, 0)
	if err := postSvc.Withdraw(post.ID, owner.ID); err != nil {
		t.Fatalf("withdraw post: %v", err)
	}
	if _, _, err := likeSvc.Toggle(other.ID, "post", post.ID); err != ErrContentUnavail {
		t.Fatalf("expected ErrContentUnavail, got %v", err)
	}
}
