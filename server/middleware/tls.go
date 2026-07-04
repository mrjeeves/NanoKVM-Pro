package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/unrolled/secure"
)

func Tls() gin.HandlerFunc {
	secureMiddleware := secure.New(secure.Options{
		SSLRedirect:          false,
		STSSeconds:           31536000,
		STSIncludeSubdomains: true,
		FrameDeny:            true,
		ContentTypeNosniff:   true,
	})

	secureFunc := func(c *gin.Context) {
		// Requests tunneled in over the AllMyStuff mesh "sites" plane are plaintext
		// in-process HTTP — the mesh already authenticates and secures the
		// transport. The HTTPS-hardening headers below are meant for the device's
		// own HTTPS LAN serving and BREAK the tunneled web UI: the viewer maps the
		// site to http://localhost:<port>, and HSTS on that origin upgrades the
		// SPA's asset requests to https, which the plaintext tunnel can't serve —
		// so the page loads blank (X-Frame-Options DENY / nosniff are likewise
		// inappropriate for the tunnel). Skip them for mesh-tunneled requests,
		// matching the NanoKVM, which serves plain http and sends none of these.
		if isMeshAuthed(c.Request) {
			c.Next()
			return
		}

		err := secureMiddleware.Process(c.Writer, c.Request)
		if err != nil {
			c.Abort()
			return
		}

		// Check if redirect response was set
		if status := c.Writer.Status(); status >= 300 && status < 400 {
			c.Abort()
		}
	}

	return secureFunc
}
