package file

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
)

// AvatarRoutes declares the authenticated write and anonymous image read.
func AvatarRoutes() []Route {
	return []Route{{"POST", "/api/v1/auth/profile/avatar", ""}, {"GET", "/api/v1/avatars/:user_id", ""}}
}

// RegisterAvatarRoutes does not require administrative file permissions or a public bucket.
func RegisterAvatarRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *Service, accounts AvatarProfiles) error {
	if router == nil || auth == nil || profiles == nil || service == nil || accounts.Save == nil {
		return errors.New("avatar dependencies required")
	}
	guard, err := identity.RequireActor(profiles)
	if err != nil {
		return err
	}
	router.POST("/api/v1/auth/profile/avatar", identity.JWTAuth(auth), guard, avatarUploadHandler(service, accounts))
	router.GET("/api/v1/avatars/:user_id", avatarReadHandler(service))
	return nil
}

func avatarUploadHandler(service *Service, profiles AvatarProfiles) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxAvatarSize+(64<<10))
		defer cleanMultipart(c)
		part, name, declared, size, err := multipartFile(c.Request, MaxAvatarSize)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				httpx.Fail(c, avatarTooLarge())
			} else {
				httpx.Fail(c, invalidAvatar())
			}
			return
		}
		defer part.Close()
		a := identity.AdminActor(c)
		input := Input{TenantID: a.User.TenantID, UserID: a.User.ID, OriginalName: name, DeclaredMIME: declared, Reader: part, SizeBytes: size, IdempotencyKey: c.GetHeader("Idempotency-Key"), Serialize: func(status int, value any) (idem.Result, error) { return httpx.Serialize(c, status, value) }}
		view, result, err := service.UploadAvatar(c.Request.Context(), input, profiles)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if profiles.Invalidate != nil && profiles.Invalidate(c.Request.Context(), a.User.TenantID, a.User.ID) {
			c.Header("X-Authz-Invalidation", "pending")
		}
		if result.Status != 0 {
			httpx.WriteResult(c, result)
		} else {
			httpx.Created(c, view)
		}
	}
}

func avatarReadHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		user, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
		if err != nil || user <= 0 {
			httpx.Fail(c, ErrFileNotFound)
			return
		}
		body, size, redirect, err := service.Avatar(c.Request.Context(), user)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		c.Header("Cache-Control", "public, max-age=3600")
		if redirect != "" {
			c.Redirect(http.StatusFound, redirect)
			return
		}
		defer body.Close()
		c.Header("Content-Type", "image/jpeg")
		c.Header("X-Content-Type-Options", "nosniff")
		if size >= 0 {
			c.Header("Content-Length", strconv.FormatInt(size, 10))
		}
		if _, err := io.Copy(c.Writer, body); err != nil {
			if attached := c.Error(errors.New("avatar stream interrupted")); attached != nil {
				c.Abort()
			}
		}
	}
}

func cleanMultipart(c *gin.Context) {
	if c.Request.MultipartForm != nil {
		if err := c.Request.MultipartForm.RemoveAll(); err != nil {
			if attached := c.Error(errors.New("multipart cleanup failed")); attached != nil {
				c.Abort()
			}
		}
	}
}
