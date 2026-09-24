package handler_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/gbtreehole/backend/internal/handler"
	"github.com/gbtreehole/backend/internal/middleware"
	"github.com/gbtreehole/backend/internal/model"
	"github.com/gbtreehole/backend/internal/repository"
	"github.com/gbtreehole/backend/internal/router"
	"github.com/gbtreehole/backend/internal/service"
)

type withdrawTestEnv struct {
	router   http.Handler
	tokens   map[string]string
	identity map[string]uint
}

func setupWithdrawRouter(t *testing.T) *withdrawTestEnv {
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

	tokenSvc := service.NewTokenService("test-secret", 60)
	identitySvc := service.NewIdentityService(identityRepo, tokenSvc, logger)
	tagSvc := service.NewTagService(tagRepo)
	sensitiveSvc := service.NewSensitiveWordService(sensitiveRepo)
	for _, word := range []string{"赌博", "违禁"} {
		if _, err := sensitiveSvc.Create(word); err != nil {
			t.Fatalf("seed sensitive word: %v", err)
		}
	}
	reviewSvc := service.NewReviewService(reviewRepo, postRepo, commentRepo, logger)
	postSvc := service.NewPostService(postRepo, tagSvc, sensitiveSvc, reviewSvc, logger)
	commentSvc := service.NewCommentService(commentRepo, postRepo, sensitiveSvc, reviewSvc, logger)
	likeSvc := service.NewLikeService(likeRepo, postRepo, commentRepo, logger)

	authH := handler.NewAuthHandler(identitySvc, logger)
	postH := handler.NewPostHandler(postSvc, likeSvc, logger)
	commentH := handler.NewCommentHandler(commentSvc, likeSvc, logger)
	tagH := handler.NewTagHandler(tagSvc, logger)
	likeH := handler.NewLikeHandler(likeSvc, logger)
	adminH := handler.NewAdminHandler(reviewSvc, postSvc, sensitiveSvc, tagSvc, logger)

	identityMW := middleware.NewIdentityAuthMiddleware(tokenSvc)
	sensitiveMW := middleware.NewSensitiveWordMiddleware(sensitiveSvc, logger)

	env := &withdrawTestEnv{
		router:   router.New(logger, authH, postH, commentH, tagH, likeH, adminH, identityMW, sensitiveMW),
		tokens:   map[string]string{},
		identity: map[string]uint{},
	}
	for _, name := range []string{"owner", "other"} {
		token, id := env.createIdentity(t, name)
		env.tokens[name] = token
		env.identity[name] = id
	}
	return env
}

func (e *withdrawTestEnv) createIdentity(t *testing.T, name string) (string, uint) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"nickname": name})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/identities", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	e.router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("create identity %s: status=%d body=%s", name, w.Code, w.Body.String())
	}
	var resp struct {
		Data struct {
			Token    string `json:"token"`
			Identity struct {
				ID uint `json:"id"`
			} `json:"identity"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode identity: %v", err)
	}
	return resp.Data.Token, resp.Data.Identity.ID
}

func (e *withdrawTestEnv) doJSON(t *testing.T, method, path, token string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if payload != nil {
		raw, _ := json.Marshal(payload)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.router.ServeHTTP(w, req)
	return w
}

func uintPath(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

// TestWithdrawEndToEnd 通过 HTTP 路由验证帖子/评论撤回的完整行为。
func TestWithdrawEndToEnd(t *testing.T) {
	env := setupWithdrawRouter(t)
	ownerToken := env.tokens["owner"]
	otherToken := env.tokens["other"]

	// 作者发帖。
	w := env.doJSON(t, http.MethodPost, "/api/v1/posts", ownerToken, map[string]any{
		"content": "我今天想安静地撤回这条树洞",
		"tags":    []string{"情感树洞"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create post: %s", w.Body.String())
	}
	var created struct {
		Data struct {
			Post struct {
				ID uint `json:"id"`
			} `json:"post"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	postID := created.Data.Post.ID
	if postID == 0 {
		t.Fatalf("missing post id: %s", w.Body.String())
	}

	// 两人各发一条评论。
	var commentIDs []uint
	for _, token := range []string{ownerToken, otherToken} {
		cw := env.doJSON(t, http.MethodPost, "/api/v1/comments", token, map[string]any{
			"postId":  postID,
			"content": "评论-" + token[:6],
		})
		if cw.Code != http.StatusOK {
			t.Fatalf("create comment: %s", cw.Body.String())
		}
		var cr struct {
			Data struct {
				Comment struct {
					ID uint `json:"id"`
				} `json:"comment"`
			} `json:"data"`
		}
		json.Unmarshal(cw.Body.Bytes(), &cr)
		commentIDs = append(commentIDs, cr.Data.Comment.ID)
	}

	// 未登录不能撤回。
	if w := env.doJSON(t, http.MethodDelete, "/api/v1/posts/"+uintPath(postID)+"/withdraw", "", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous withdraw status = %d, want 401", w.Code)
	}

	// 别人拿到编号不能替作者撤回。
	if w := env.doJSON(t, http.MethodDelete, "/api/v1/posts/"+uintPath(postID)+"/withdraw", otherToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("other withdraw post status = %d, want 403, body=%s", w.Code, w.Body.String())
	}

	// 非作者不能撤回别人的评论。
	if w := env.doJSON(t, http.MethodDelete, "/api/v1/comments/"+uintPath(commentIDs[0])+"/withdraw", otherToken, nil); w.Code != http.StatusForbidden {
		t.Fatalf("other withdraw comment status = %d, want 403", w.Code)
	}

	// 先给帖子点赞，撤回后再尝试点赞应失败。
	if w := env.doJSON(t, http.MethodPost, "/api/v1/likes/toggle", otherToken, map[string]any{
		"targetType": "post", "targetId": postID,
	}); w.Code != http.StatusOK {
		t.Fatalf("like post: %s", w.Body.String())
	}

	// 作者撤回自己的评论：楼层消失、帖子评论数减少。
	if w := env.doJSON(t, http.MethodDelete, "/api/v1/comments/"+uintPath(commentIDs[0])+"/withdraw", ownerToken, nil); w.Code != http.StatusOK {
		t.Fatalf("owner withdraw comment status = %d body=%s", w.Code, w.Body.String())
	}
	// 重复操作 -> 409。
	if w := env.doJSON(t, http.MethodDelete, "/api/v1/comments/"+uintPath(commentIDs[0])+"/withdraw", ownerToken, nil); w.Code != http.StatusConflict {
		t.Fatalf("duplicate comment withdraw status = %d, want 409", w.Code)
	}
	lw := env.doJSON(t, http.MethodGet, "/api/v1/posts/"+uintPath(postID)+"/comments?page=1&page_size=20", "", nil)
	var listResp struct {
		Data struct {
			Total int64 `json:"total"`
		} `json:"data"`
	}
	json.Unmarshal(lw.Body.Bytes(), &listResp)
	if listResp.Data.Total != 1 {
		t.Fatalf("comment total after withdraw = %d, want 1", listResp.Data.Total)
	}
	// 已撤回评论不能再点赞。
	if w := env.doJSON(t, http.MethodPost, "/api/v1/likes/toggle", otherToken, map[string]any{
		"targetType": "comment", "targetId": commentIDs[0],
	}); w.Code != http.StatusNotFound {
		t.Fatalf("like withdrawn comment status = %d, want 404", w.Code)
	}

	// 作者撤回自己的帖子。
	if w := env.doJSON(t, http.MethodDelete, "/api/v1/posts/"+uintPath(postID)+"/withdraw", ownerToken, nil); w.Code != http.StatusOK {
		t.Fatalf("owner withdraw post status = %d body=%s", w.Code, w.Body.String())
	}
	// 重复操作 -> 409。
	if w := env.doJSON(t, http.MethodDelete, "/api/v1/posts/"+uintPath(postID)+"/withdraw", ownerToken, nil); w.Code != http.StatusConflict {
		t.Fatalf("duplicate post withdraw status = %d, want 409", w.Code)
	}

	// 详情不再能访问，评论列表同样不可访问。
	if w := env.doJSON(t, http.MethodGet, "/api/v1/posts/"+uintPath(postID), otherToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("get withdrawn post status = %d, want 404", w.Code)
	}
	if w := env.doJSON(t, http.MethodGet, "/api/v1/posts/"+uintPath(postID)+"/comments", otherToken, nil); w.Code != http.StatusNotFound {
		t.Fatalf("comments of withdrawn post status = %d, want 404", w.Code)
	}
	// 退出最新、热度、精选、标签列表。
	for _, path := range []string{
		"/api/v1/posts?page=1&page_size=20",
		"/api/v1/posts/hot",
		"/api/v1/posts/featured",
		"/api/v1/posts?page=1&page_size=20&tag_id=1",
	} {
		w := env.doJSON(t, http.MethodGet, path, "", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("list %s status = %d body=%s", path, w.Code, w.Body.String())
		}
		if bytes.Contains(w.Body.Bytes(), []byte("我今天想安静地撤回这条树洞")) {
			t.Fatalf("withdrawn post still listed at %s", path)
		}
	}
	// 已撤回帖子不能再点赞。
	if w := env.doJSON(t, http.MethodPost, "/api/v1/likes/toggle", otherToken, map[string]any{
		"targetType": "post", "targetId": postID,
	}); w.Code != http.StatusNotFound {
		t.Fatalf("like withdrawn post status = %d, want 404", w.Code)
	}
}

// TestWithdrawReviewItemRemoved 撤回待审内容时审核队列对应条目被撤下。
func TestWithdrawReviewItemRemoved(t *testing.T) {
	env := setupWithdrawRouter(t)
	ownerToken := env.tokens["owner"]

	// 命中敏感词的帖子进入待审队列。
	w := env.doJSON(t, http.MethodPost, "/api/v1/posts", ownerToken, map[string]any{
		"content": "这里包含赌博违禁内容",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("create blocked post: %s", w.Body.String())
	}
	var created struct {
		Data struct {
			Blocked bool `json:"blocked"`
			Post    struct {
				ID     uint `json:"id"`
				Status int  `json:"status"`
			} `json:"post"`
		} `json:"data"`
	}
	json.Unmarshal(w.Body.Bytes(), &created)
	if !created.Data.Blocked {
		t.Fatalf("post should be blocked into review queue")
	}
	postID := created.Data.Post.ID

	// 待审队列里有这条。
	rw := env.doJSON(t, http.MethodGet, "/api/v1/admin/reviews?page=1&page_size=50&status=1", ownerToken, nil)
	if !bytes.Contains(rw.Body.Bytes(), []byte("赌博")) {
		t.Fatalf("review queue missing item: %s", rw.Body.String())
	}

	// 作者撤回。
	if w := env.doJSON(t, http.MethodDelete, "/api/v1/posts/"+uintPath(postID)+"/withdraw", ownerToken, nil); w.Code != http.StatusOK {
		t.Fatalf("withdraw pending post: %s", w.Body.String())
	}

	// 待审队列里不再出现，已撤回分类里能查到。
	rw = env.doJSON(t, http.MethodGet, "/api/v1/admin/reviews?page=1&page_size=50&status=1", ownerToken, nil)
	if bytes.Contains(rw.Body.Bytes(), []byte("赌博")) {
		t.Fatalf("withdrawn item still pending in review queue")
	}
	rw = env.doJSON(t, http.MethodGet, "/api/v1/admin/reviews?page=1&page_size=50&status=4", ownerToken, nil)
	if !bytes.Contains(rw.Body.Bytes(), []byte("赌博")) {
		t.Fatalf("withdrawn item missing from withdrawn tab: %s", rw.Body.String())
	}
}
