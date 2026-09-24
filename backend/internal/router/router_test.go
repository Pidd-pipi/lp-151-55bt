package router

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/gbtreehole/backend/internal/handler"
	"github.com/gbtreehole/backend/internal/middleware"
	"github.com/gbtreehole/backend/internal/model"
	"github.com/gbtreehole/backend/internal/repository"
	"github.com/gbtreehole/backend/internal/service"
)

type testServer struct {
	engine    *gin.Engine
	authorTok string
	otherTok  string
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()
	gin.SetMode(gin.TestMode)
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
			t.Fatalf("create identity: %v", err)
		}
	}

	tokenService := service.NewTokenService("test-secret", 60)
	identityService := service.NewIdentityService(identityRepo, tokenService, logger)
	tagService := service.NewTagService(tagRepo)
	sensitiveService := service.NewSensitiveWordService(sensitiveRepo)
	reviewService := service.NewReviewService(reviewRepo, postRepo, commentRepo, logger)
	postService := service.NewPostService(postRepo, tagService, sensitiveService, reviewService, logger)
	commentService := service.NewCommentService(commentRepo, postRepo, sensitiveService, reviewService, logger)
	likeService := service.NewLikeService(likeRepo, postRepo, commentRepo, logger)

	authHandler := handler.NewAuthHandler(identityService, logger)
	postHandler := handler.NewPostHandler(postService, likeService, logger)
	commentHandler := handler.NewCommentHandler(commentService, likeService, logger)
	tagHandler := handler.NewTagHandler(tagService, logger)
	likeHandler := handler.NewLikeHandler(likeService, logger)
	adminHandler := handler.NewAdminHandler(reviewService, postService, sensitiveService, tagService, logger)

	identityMW := middleware.NewIdentityAuthMiddleware(tokenService)
	sensitiveMW := middleware.NewSensitiveWordMiddleware(sensitiveService, logger)

	engine := New(logger, authHandler, postHandler, commentHandler, tagHandler, likeHandler, adminHandler, identityMW, sensitiveMW)

	authorTok, err := tokenService.Sign(1, "author-key")
	if err != nil {
		t.Fatalf("sign author token: %v", err)
	}
	otherTok, err := tokenService.Sign(2, "other-key")
	if err != nil {
		t.Fatalf("sign other token: %v", err)
	}
	return &testServer{engine: engine, authorTok: authorTok, otherTok: otherTok}
}

func (s *testServer) do(t *testing.T, method, path, token, body string) (int, map[string]any) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	var parsed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode response %s %s: %v body=%s", method, path, err, rec.Body.String())
	}
	return rec.Code, parsed
}

func (s *testServer) createPost(t *testing.T) uint {
	t.Helper()
	code, resp := s.do(t, http.MethodPost, "/api/v1/posts", s.authorTok, `{"title":"测试","content":"今天心情不错"}`)
	if code != http.StatusOK {
		t.Fatalf("create post status = %d, resp=%v", code, resp)
	}
	post := resp["data"].(map[string]any)["post"].(map[string]any)
	return uint(post["id"].(float64))
}

func TestWithdrawPostHTTP(t *testing.T) {
	s := newTestServer(t)
	postID := s.createPost(t)
	withdrawPath := fmt.Sprintf("/api/v1/posts/%d/withdraw", postID)

	// 未登录不能撤回
	if code, _ := s.do(t, http.MethodPost, withdrawPath, "", ""); code != http.StatusUnauthorized {
		t.Fatalf("withdraw without token: status = %d, want 401", code)
	}
	// 别人拿到编号不能替作者撤回
	if code, _ := s.do(t, http.MethodPost, withdrawPath, s.otherTok, ""); code != http.StatusForbidden {
		t.Fatalf("withdraw by other: status = %d, want 403", code)
	}
	// 作者撤回成功
	if code, _ := s.do(t, http.MethodPost, withdrawPath, s.authorTok, ""); code != http.StatusOK {
		t.Fatalf("withdraw by author: status = %d, want 200", code)
	}
	// 详情不再能访问
	if code, _ := s.do(t, http.MethodGet, fmt.Sprintf("/api/v1/posts/%d", postID), "", ""); code != http.StatusNotFound {
		t.Fatalf("get withdrawn post: status = %d, want 404", code)
	}
	// 最新列表不再包含
	code, resp := s.do(t, http.MethodGet, "/api/v1/posts?page=1&page_size=20", "", "")
	if code != http.StatusOK {
		t.Fatalf("list posts: status = %d", code)
	}
	if total := resp["data"].(map[string]any)["total"].(float64); total != 0 {
		t.Fatalf("latest list total = %v, want 0", total)
	}
	// 重复撤回提示已经处理
	if code, _ := s.do(t, http.MethodPost, withdrawPath, s.authorTok, ""); code != http.StatusConflict {
		t.Fatalf("repeat withdraw: status = %d, want 409", code)
	}
	// 已撤回帖子不能再点赞
	likeBody := fmt.Sprintf(`{"targetType":"post","targetId":%d}`, postID)
	if code, _ := s.do(t, http.MethodPost, "/api/v1/likes/toggle", s.otherTok, likeBody); code != http.StatusConflict {
		t.Fatalf("like withdrawn post: status = %d, want 409", code)
	}
}

func TestWithdrawCommentHTTP(t *testing.T) {
	s := newTestServer(t)
	postID := s.createPost(t)

	// 另一个身份发表评论
	code, resp := s.do(t, http.MethodPost, "/api/v1/comments", s.otherTok, fmt.Sprintf(`{"postId":%d,"content":"同感"}`, postID))
	if code != http.StatusOK {
		t.Fatalf("create comment: status = %d, resp=%v", code, resp)
	}
	comment := resp["data"].(map[string]any)["comment"].(map[string]any)
	commentID := uint(comment["id"].(float64))
	withdrawPath := fmt.Sprintf("/api/v1/comments/%d/withdraw", commentID)

	// 帖子作者但不是评论作者，不能撤回
	if code, _ := s.do(t, http.MethodPost, withdrawPath, s.authorTok, ""); code != http.StatusForbidden {
		t.Fatalf("withdraw comment by non-author: status = %d, want 403", code)
	}
	// 评论作者撤回
	if code, _ := s.do(t, http.MethodPost, withdrawPath, s.otherTok, ""); code != http.StatusOK {
		t.Fatalf("withdraw comment: status = %d, want 200", code)
	}
	// 楼层消失
	code, resp = s.do(t, http.MethodGet, fmt.Sprintf("/api/v1/posts/%d/comments?page=1&page_size=20", postID), "", "")
	if code != http.StatusOK {
		t.Fatalf("list comments: status = %d", code)
	}
	if total := resp["data"].(map[string]any)["total"].(float64); total != 0 {
		t.Fatalf("comments total = %v, want 0", total)
	}
	// 帖子评论数一并减少
	code, resp = s.do(t, http.MethodGet, fmt.Sprintf("/api/v1/posts/%d", postID), "", "")
	if code != http.StatusOK {
		t.Fatalf("get post: status = %d", code)
	}
	if count := resp["data"].(map[string]any)["commentCount"].(float64); count != 0 {
		t.Fatalf("commentCount = %v, want 0", count)
	}
	// 重复撤回提示已经处理
	if code, _ := s.do(t, http.MethodPost, withdrawPath, s.otherTok, ""); code != http.StatusConflict {
		t.Fatalf("repeat withdraw comment: status = %d, want 409", code)
	}
	// 已撤回评论不能再点赞
	likeBody := fmt.Sprintf(`{"targetType":"comment","targetId":%d}`, commentID)
	if code, _ := s.do(t, http.MethodPost, "/api/v1/likes/toggle", s.authorTok, likeBody); code != http.StatusConflict {
		t.Fatalf("like withdrawn comment: status = %d, want 409", code)
	}
}
