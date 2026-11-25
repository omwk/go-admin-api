package gateway

import (
	_ "embed"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

// OpenAPI is the separate public contract for status calls and wallet operations.
//
//go:embed openapi.yaml
var OpenAPI []byte

// RegisterOpenAPIRoute publishes the public contract with the deployment's origin.
func RegisterOpenAPIRoute(router *gin.Engine, publicBaseURL string) error {
	var document map[string]yaml.Node
	if err := yaml.Unmarshal(OpenAPI, &document); err != nil {
		return fmt.Errorf("parse gateway OpenAPI: %w", err)
	}
	var servers yaml.Node
	if err := servers.Encode([]map[string]string{{"url": publicBaseURL}}); err != nil {
		return fmt.Errorf("encode gateway OpenAPI servers: %w", err)
	}
	document["servers"] = servers
	body, err := yaml.Marshal(document)
	if err != nil {
		return fmt.Errorf("render gateway OpenAPI: %w", err)
	}
	router.GET("/openapi/gateway.yaml", func(c *gin.Context) {
		c.Data(http.StatusOK, "application/yaml; charset=utf-8", body)
	})
	return nil
}
