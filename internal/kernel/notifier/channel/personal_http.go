package channel

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/omwk/go-admin-api/internal/kernel/errs"
	"github.com/omwk/go-admin-api/internal/kernel/httpx"
	"github.com/omwk/go-admin-api/internal/kernel/idem"
)

// PersonalManagement manages destinations within the authenticated user's boundary.
type PersonalManagement interface {
	ListPersonalChannels(context.Context, Actor) ([]PersonalChannelView, error)
	SavePersonalChannel(context.Context, Actor, string, PersonalChannelInput, *idem.Operation) (idem.Result, error)
	DeletePersonalChannel(context.Context, Actor, string, *idem.Operation) (idem.Result, error)
	TestPersonalChannel(context.Context, Actor, string, *idem.Operation) (idem.Result, error)
}

// RegisterPersonal installs self-service endpoints without administrator permissions.
func RegisterPersonal(router *gin.Engine, m PersonalManagement, a Access) {
	g := router.Group("/api/v1/auth/notification-channels", a.Authenticate...)
	g.Use(broadcastOperation(a))
	g.GET("", func(c *gin.Context) {
		rows, err := m.ListPersonalChannels(c.Request.Context(), a.Actor(c))
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, gin.H{"channels": rows})
	})
	g.PUT("/:channel", func(c *gin.Context) {
		var in PersonalChannelInput
		if c.ShouldBindJSON(&in) != nil {
			httpx.Fail(c, errs.New(10001, 400, "个人渠道参数无效"))
			return
		}
		result, err := m.SavePersonalChannel(c.Request.Context(), a.Actor(c), c.Param("channel"), in, personalOperation(c))
		personalResult(c, result, err)
	})
	g.DELETE("/:channel", func(c *gin.Context) {
		result, err := m.DeletePersonalChannel(c.Request.Context(), a.Actor(c), c.Param("channel"), personalOperation(c))
		personalResult(c, result, err)
	})
	g.POST("/:channel/test", func(c *gin.Context) {
		result, err := m.TestPersonalChannel(c.Request.Context(), a.Actor(c), c.Param("channel"), personalOperation(c))
		personalResult(c, result, err)
	})
}

func personalOperation(c *gin.Context) *idem.Operation {
	value, _ := c.Get("notification_operation")
	op, _ := value.(*idem.Operation)
	return op
}

func personalResult(c *gin.Context, result idem.Result, err error) {
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if result.Status == 204 {
		httpx.NoContent(c)
		return
	}
	var out any
	if err := decodeResult(result.Body, &out); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, out)
}
