// Package identity defines the identity module boundary.
package identity

import "github.com/gin-gonic/gin"

// AdminActor returns the verified management identity attached by RequireActor or RequirePerm.
func AdminActor(c *gin.Context) Overview { return c.MustGet(actorKey).(Overview) }
