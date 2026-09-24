package service

import (
	"errors"
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

type withdrawFixture struct {
	db       *gorm.DB
	posts    PostService
	comments CommentService
	likes    LikeService
	review   ReviewService
	authorID uint
	otherID  uint
}

func newWithdrawFixture(t *testing.T) *withdrawFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(model.AllModels()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	identityRepo := repository.NewIdentityRepository(db)
	postRepo := repository.NewPostRepository(db)
	commentRepo := repository.NewCommentRepository(db)
	tagRepo := repository.NewTagRepository(db)
	likeRepo := repository.NewLikeRepository(db)
	sensitiveRepo := repository.NewSensitiveWordRepository(db)
	reviewRepo := repository.NewReviewQueueRepository(db)

	for _, key := range []string{"author-key", "other-key"} {
		if err := identityRepo.Create(&model.UserIdentity{
			IdentityKey: key, Nickname: key, Avatar: "a.png",
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("create identity %s: %v", key, err)
		}
	}

	tagService := NewTagService(tagRepo)
	sensitiveService := NewSensitiveWordService(sensitiveRepo)
	reviewService := NewReviewService(reviewRepo, postRepo, commentRepo, logger)
	postService := NewPostService(postRepo, tagService, sensitiveService, reviewService, logger)
	commentService := NewCommentService(commentRepo, postRepo, sensitiveService, reviewService, logger)
	likeService := NewLikeService(likeRepo, postRepo, commentRepo, logger)

	return &withdrawFixture{
		db:       db,
		posts:    postService,
		comments: commentService,
		likes:    likeService,
		review:   reviewService,
		authorID: 1,
		otherID:  2,
	}
}

func (f *withdrawFixture) mustPublishedPost(t *testing.T, identityID uint) *model.Post {
	t.Helper()
	post, _, blocked, err := f.posts.Create(identityID, "标题", "正常内容", nil, []string{"生活趣事"})
	if err != nil || blocked {
		t.Fatalf("create post: err=%v blocked=%v", err, blocked)
	}
	return post
}

// 撤回帖子：列表消失、详情不可访问、重复撤回与他人撤回被拒、审核队列撤下。
func TestPostWithdraw(t *testing.T) {
	f := newWithdrawFixture(t)
	post := f.mustPublishedPost(t, f.authorID)

	// 最新/标签列表中可见
	if posts, total, err := f.posts.List(1, 10, 0, false); err != nil || total != 1 || len(posts) != 1 {
		t.Fatalf("list before withdraw: posts=%d total=%d err=%v", len(posts), total, err)
	}
	// 详情可访问
	if _, err := f.posts.GetByID(post.ID); err != nil {
		t.Fatalf("get post before withdraw: %v", err)
	}

	// 他人拿到编号也不能替作者撤回
	if err := f.posts.Withdraw(f.otherID, post.ID); !errors.Is(err, ErrWithdrawForbidden) {
		t.Fatalf("expected ErrWithdrawForbidden, got %v", err)
	}

	// 作者撤回
	if err := f.posts.Withdraw(f.authorID, post.ID); err != nil {
		t.Fatalf("withdraw post: %v", err)
	}

	// 最新、热度、精选、标签列表均不再包含
	if posts, total, err := f.posts.List(1, 10, 0, false); err != nil || total != 0 || len(posts) != 0 {
		t.Fatalf("latest list after withdraw: posts=%d total=%d err=%v", len(posts), total, err)
	}
	if _, err := f.posts.GetByID(post.ID); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("expected ErrPostNotFound after withdraw, got %v", err)
	}

	// 重复撤回提示已处理
	if err := f.posts.Withdraw(f.authorID, post.ID); !errors.Is(err, ErrAlreadyWithdrawn) {
		t.Fatalf("expected ErrAlreadyWithdrawn, got %v", err)
	}

	// 撤回不存在的帖子
	if err := f.posts.Withdraw(f.authorID, 9999); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("expected ErrPostNotFound, got %v", err)
	}
}

// 已撤回帖子不能再点赞；撤回前已存在的点赞仍可取消。
func TestLikeWithdrawnPost(t *testing.T) {
	f := newWithdrawFixture(t)
	post := f.mustPublishedPost(t, f.authorID)

	// 撤回前已点赞的用户仍可取消点赞
	if _, _, err := f.likes.Toggle(f.otherID, "post", post.ID); err != nil {
		t.Fatalf("like before withdraw: %v", err)
	}
	if err := f.posts.Withdraw(f.authorID, post.ID); err != nil {
		t.Fatalf("withdraw post: %v", err)
	}
	if _, _, err := f.likes.Toggle(f.otherID, "post", post.ID); err != nil {
		t.Fatalf("unlike after withdraw should be allowed: %v", err)
	}
	// 未点赞过的用户不能再点赞
	if _, _, err := f.likes.Toggle(f.otherID, "post", post.ID); !errors.Is(err, ErrTargetWithdrawn) {
		t.Fatalf("expected ErrTargetWithdrawn, got %v", err)
	}
}

// 撤回评论：楼层消失、帖子评论数减少、重复撤回与他人撤回被拒、已撤回评论不能点赞。
func TestCommentWithdraw(t *testing.T) {
	f := newWithdrawFixture(t)
	post := f.mustPublishedPost(t, f.authorID)

	comment, _, blocked, err := f.comments.Create(f.otherID, post.ID, "正常评论")
	if err != nil || blocked {
		t.Fatalf("create comment: err=%v blocked=%v", err, blocked)
	}
	comments, total, err := f.comments.ListByPostID(post.ID, 1, 10)
	if err != nil || total != 1 || len(comments) != 1 {
		t.Fatalf("list comments before withdraw: len=%d total=%d err=%v", len(comments), total, err)
	}
	updated, err := f.posts.GetByID(post.ID)
	if err != nil {
		t.Fatalf("get post: %v", err)
	}
	if updated.CommentCount != 1 {
		t.Fatalf("comment count before withdraw = %d, want 1", updated.CommentCount)
	}

	// 非作者不能撤回
	if err := f.comments.Withdraw(f.authorID, comment.ID); !errors.Is(err, ErrWithdrawForbidden) {
		t.Fatalf("expected ErrWithdrawForbidden, got %v", err)
	}

	if err := f.comments.Withdraw(f.otherID, comment.ID); err != nil {
		t.Fatalf("withdraw comment: %v", err)
	}

	if _, total, err := f.comments.ListByPostID(post.ID, 1, 10); err != nil || total != 0 {
		t.Fatalf("comments after withdraw: total=%d err=%v", total, err)
	}
	updated, err = f.posts.GetByID(post.ID)
	if err != nil {
		t.Fatalf("get post after withdraw: %v", err)
	}
	if updated.CommentCount != 0 {
		t.Fatalf("comment count after withdraw = %d, want 0", updated.CommentCount)
	}

	if err := f.comments.Withdraw(f.otherID, comment.ID); !errors.Is(err, ErrAlreadyWithdrawn) {
		t.Fatalf("expected ErrAlreadyWithdrawn, got %v", err)
	}

	if _, _, err := f.likes.Toggle(f.authorID, "comment", comment.ID); !errors.Is(err, ErrTargetWithdrawn) {
		t.Fatalf("expected ErrTargetWithdrawn, got %v", err)
	}
}

// 待审核评论从未计入帖子评论数，撤回时不应递减。
func TestWithdrawPendingCommentKeepsCount(t *testing.T) {
	f := newWithdrawFixture(t)
	post := f.mustPublishedPost(t, f.authorID)

	if err := f.db.Create(&model.SensitiveWord{Word: "赌博", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("create sensitive word: %v", err)
	}
	_, _, blocked, err := f.comments.Create(f.otherID, post.ID, "这里赌博了")
	if err != nil || !blocked {
		t.Fatalf("create pending comment: err=%v blocked=%v", err, blocked)
	}

	var pending model.Comment
	if err := f.db.Where("post_id = ? AND status = ?", post.ID, constants.CommentStatusPending).First(&pending).Error; err != nil {
		t.Fatalf("find pending comment: %v", err)
	}
	if err := f.comments.Withdraw(f.otherID, pending.ID); err != nil {
		t.Fatalf("withdraw pending comment: %v", err)
	}
	var postModel model.Post
	if err := f.db.First(&postModel, post.ID).Error; err != nil {
		t.Fatalf("load post: %v", err)
	}
	if postModel.CommentCount != 0 {
		t.Fatalf("comment count = %d, want 0 (pending comment never counted)", postModel.CommentCount)
	}

	// 审核队列中的待审条目应被撤下
	_, total, err := f.review.List(1, 10, constants.ReviewStatusPending)
	if err != nil {
		t.Fatalf("list pending reviews: %v", err)
	}
	if total != 0 {
		t.Fatalf("pending review total = %d, want 0", total)
	}
}

// 撤回待审核帖子时，审核队列中的对应条目一并撤下。
func TestWithdrawPendingPostPullsReview(t *testing.T) {
	f := newWithdrawFixture(t)
	if err := f.db.Create(&model.SensitiveWord{Word: "赌博", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("create sensitive word: %v", err)
	}
	post, _, blocked, err := f.posts.Create(f.authorID, "赌博标题", "赌博内容", nil, nil)
	if err != nil || !blocked {
		t.Fatalf("create pending post: err=%v blocked=%v", err, blocked)
	}
	if _, total, err := f.review.List(1, 10, constants.ReviewStatusPending); err != nil || total != 1 {
		t.Fatalf("pending reviews before withdraw: total=%d err=%v", total, err)
	}
	if err := f.posts.Withdraw(f.authorID, post.ID); err != nil {
		t.Fatalf("withdraw pending post: %v", err)
	}
	if _, total, err := f.review.List(1, 10, constants.ReviewStatusPending); err != nil || total != 0 {
		t.Fatalf("pending reviews after withdraw: total=%d err=%v", total, err)
	}
	items, total, err := f.review.List(1, 10, constants.ReviewStatusWithdrawn)
	if err != nil {
		t.Fatalf("list withdrawn reviews: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("withdrawn reviews = %d/%d, want 1", len(items), total)
	}
}
