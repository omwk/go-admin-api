package shop

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
	"github.com/omwk/go-admin-api/internal/modules/identity"
)

const multipartBodyLimit int64 = MaxFileSize + (1 << 20)

// FileRoute describes one authenticated file operation.
type FileRoute struct{ Method, Path, Permission string }

// FileRoutes returns the file upload and download route manifest.
func FileRoutes() []FileRoute {
	return []FileRoute{{"POST", "/files", ""}, {"GET", "/files/:id", ""}}
}

// RegisterFileRoutes installs authenticated file upload and controlled download routes.
func RegisterFileRoutes(router *gin.Engine, auth *identity.AuthService, profiles *identity.ProfileService, service *FileService) error {
	if router == nil || auth == nil || profiles == nil || service == nil {
		return errors.New("file route dependencies are required")
	}
	actor, err := identity.RequireActor(profiles)
	if err != nil {
		return err
	}
	group := router.Group("/api/v1/admin", identity.JWTAuth(auth), actor)
	group.POST("/files", func(c *gin.Context) {
		a := identity.AdminActor(c)
		if c.Request.ContentLength > multipartBodyLimit {
			httpx.Fail(c, tooLargeOrInvalid())
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, multipartBodyLimit)
		}
		part, filename, declaredMIME, purpose, err := multipartFile(c.Request)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				httpx.Fail(c, tooLargeOrInvalid())
				return
			}
			httpx.Fail(c, invalidFile())
			return
		}
		defer part.Close()
		input := FileInput{TenantID: a.User.TenantID, UserID: a.User.ID, OriginalName: filename, DeclaredMIME: declaredMIME, Purpose: purpose, Reader: part, IdempotencyKey: c.GetHeader("Idempotency-Key"), Serialize: func(status int, data any) (idem.Result, error) { return httpx.Serialize(c, status, data) }}
		view, result, err := service.Upload(c.Request.Context(), input)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		if result.Status != 0 {
			httpx.WriteResult(c, result)
			return
		}
		httpx.Created(c, view)
	})
	group.GET("/files/:id", func(c *gin.Context) {
		a := identity.AdminActor(c)
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			httpx.Fail(c, invalidFile())
			return
		}
		row, body, _, size, err := service.Get(c.Request.Context(), a.User.TenantID, id)
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		defer body.Close()
		c.Header("Content-Type", row.MIME)
		if size >= 0 {
			c.Header("Content-Length", strconv.FormatInt(size, 10))
		}
		c.Header("Content-Disposition", `attachment; filename="`+escapeFilename(row.OriginalName)+`"`)
		if _, err := io.Copy(c.Writer, body); err != nil {
			return
		}
	})
	return nil
}

func escapeFilename(name string) string {
	return strings.NewReplacer("\"", "_", "\r", "_", "\n", "_").Replace(name)
}

func multipartFile(request *http.Request) (io.ReadCloser, string, string, string, error) {
	reader, err := request.MultipartReader()
	if err != nil {
		return nil, "", "", "", err
	}
	var purpose string
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", "", "", err
		}
		if part.FormName() == "purpose" {
			value, err := io.ReadAll(io.LimitReader(part, 128))
			part.Close()
			if err != nil {
				return nil, "", "", "", err
			}
			purpose = string(value)
			continue
		}
		if part.FormName() == "file" && part.FileName() != "" {
			return part, part.FileName(), part.Header.Get("Content-Type"), purpose, nil
		}
		part.Close()
	}
	return nil, "", "", "", errors.New("file part is required")
}
