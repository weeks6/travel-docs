// Package web serves the browser UI.
package web

import (
	"embed"
	"net/http"

	"github.com/labstack/echo/v5"
)

//go:embed static
var staticFS embed.FS

// Register serves the UI at "/" and its assets under "/static/".
func Register(e *echo.Echo) {
	index, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		panic(err)
	}
	e.GET("/", func(c *echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-cache")
		return c.Blob(http.StatusOK, echo.MIMETextHTMLCharsetUTF8, index)
	})
	e.StaticFS("/static", echo.MustSubFS(staticFS, "static"))
}
