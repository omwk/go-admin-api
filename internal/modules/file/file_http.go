package file

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
	"github.com/omwk/go-admin-api/internal/modules/identity/permission"
)

const multipartBodyLimit int64 = MaxFileSize + (1 << 20)

// Route describes one authorized file operation.
type Route struct{ Method, Path, Permission string }

// Routes returns the file library route manifest.
func Routes() []Route {
	return []Route{{"POST", "/files", permission.SystemFileManage}, {"GET", "/files", permission.SystemFileManage}, {"GET", "/files/:id", permission.SystemFileManage}, {"GET", "/files/:id/url", permission.SystemFileManage}, {"DELETE", "/files/:id", permission.SystemFileManage}}
}

// RegisterFileRoutes installs tenant-scoped file management routes.
func RegisterFileRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *Service) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("file route dependencies are required")
	}
	guard, err := identity.RequirePerm(profiles, permission.SystemFileManage)
	if err != nil {
		return err
	}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth), guard)
	group.POST("/files", uploadHandler(service))
	group.GET("/files", listHandler(service))
	group.GET("/files/:id", downloadHandler(service))
	group.GET("/files/:id/url", fileActionHandler(service, false))
	group.DELETE("/files/:id", fileActionHandler(service, true))
	return nil
}

func uploadHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > multipartBodyLimit {
			httpx.Fail(c, tooLargeOrInvalid())
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, multipartBodyLimit)
		defer func() {
			if c.Request.MultipartForm != nil {
				if err := c.Request.MultipartForm.RemoveAll(); err != nil {
					if attached := c.Error(errors.New("multipart cleanup failed")); attached != nil {
						c.Abort()
					}
				}
			}
		}()
		part, filename, declared, purpose, err := multipartFile(c.Request)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				httpx.Fail(c, tooLargeOrInvalid())
			} else {
				httpx.Fail(c, invalidFile())
			}
			return
		}
		defer part.Close()
		a := identity.AdminActor(c)
		input := Input{TenantID: a.User.TenantID, UserID: a.User.ID, OriginalName: filename, DeclaredMIME: declared, Purpose: purpose, Reader: part, IdempotencyKey: c.GetHeader("Idempotency-Key"), Serialize: func(status int, data any) (idem.Result, error) { return httpx.Serialize(c, status, data) }}
		view, result, err := service.Upload(c.Request.Context(), input)
		if err != nil {
			httpx.Fail(c, err)
		} else if result.Status != 0 {
			httpx.WriteResult(c, result)
		} else {
			httpx.Created(c, view)
		}
	}
}

func multipartFile(request *http.Request) (io.ReadCloser, string, string, string, error) {
	if err := request.ParseMultipartForm(512 << 10); err != nil {
		return nil, "", "", "", err
	}
	if request.MultipartForm == nil || len(request.MultipartForm.File["file"]) != 1 {
		return nil, "", "", "", invalidFile()
	}
	header := request.MultipartForm.File["file"][0]
	if header.Size > MaxFileSize {
		return nil, "", "", "", &http.MaxBytesError{Limit: MaxFileSize}
	}
	file, err := header.Open()
	return file, header.Filename, header.Header.Get("Content-Type"), request.FormValue("purpose"), err
}

func listHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		page, size := 1, 20
		for key, target := range map[string]*int{"page": &page, "page_size": &size} {
			if raw, exists := c.GetQuery(key); exists {
				value, err := strconv.Atoi(raw)
				if err != nil || value < 1 {
					httpx.Fail(c, invalidFile())
					return
				}
				*target = value
			}
		}
		value, err := service.List(c.Request.Context(), identity.AdminActor(c).User.TenantID, c.Query("purpose"), c.Query("keyword"), page, min(size, 100))
		if err != nil {
			httpx.Fail(c, err)
		} else {
			httpx.OK(c, value)
		}
	}
}

func fileID(c *gin.Context) (int64, error) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, invalidFile()
	}
	return id, nil
}

func downloadHandler(service *Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := fileID(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		row, body, _, size, err := service.Get(c.Request.Context(), identity.AdminActor(c).User.TenantID, id)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		defer body.Close()
		c.Header("Content-Type", row.MIME)
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Cache-Control", "no-store")
		if size >= 0 {
			c.Header("Content-Length", strconv.FormatInt(size, 10))
		}
		c.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": row.OriginalName}))
		if _, err := io.Copy(c.Writer, body); err != nil {
			if attached := c.Error(errors.New("file stream interrupted")); attached != nil {
				c.Abort()
			}
		}
	}
}

func fileActionHandler(service *Service, deleting bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := fileID(c)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		a := identity.AdminActor(c)
		if deleting {
			err = service.DeleteRequest(c.Request.Context(), a.User.TenantID, a.User.ID, id, c.GetHeader("Idempotency-Key"))
			if err == nil {
				httpx.NoContent(c)
			}
		} else {
			var signed string
			signed, err = service.URL(c.Request.Context(), a.User.TenantID, id)
			if err == nil {
				c.Header("Cache-Control", "no-store")
				httpx.OK(c, map[string]any{"url": signed, "expires_in": 300})
			}
		}
		if err != nil {
			httpx.Fail(c, err)
		}
	}
}
