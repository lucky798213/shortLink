package httpserver

import (
	_ "embed"
	"net/http"

	"github.com/gin-gonic/gin"
)

// 下列资源会随 Web 服务一起编译，部署时不需要额外复制前端文件。
//
//go:embed web/index.html
var frontendIndex []byte

//go:embed web/styles.css
var frontendStyles []byte

//go:embed web/app.js
var frontendScript []byte

//go:embed web/favicon.svg
var frontendFavicon []byte

// registerFrontendRoutes 注册首页和内嵌静态资源路由。
func registerFrontendRoutes(router *gin.Engine) {
	router.GET("/", serveFrontendAsset(frontendIndex, "text/html; charset=utf-8", false))
	router.GET("/assets/styles.css", serveFrontendAsset(frontendStyles, "text/css; charset=utf-8", true))
	router.GET("/assets/app.js", serveFrontendAsset(frontendScript, "text/javascript; charset=utf-8", true))
	router.GET("/assets/favicon.svg", serveFrontendAsset(frontendFavicon, "image/svg+xml", true))
}

// serveFrontendAsset 返回带安全响应头的内嵌前端资源。
func serveFrontendAsset(content []byte, contentType string, cacheable bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
		c.Header("X-Content-Type-Options", "nosniff")
		if cacheable {
			c.Header("Cache-Control", "public, max-age=3600")
		} else {
			c.Header("Cache-Control", "no-cache")
		}
		c.Data(http.StatusOK, contentType, content)
	}
}
