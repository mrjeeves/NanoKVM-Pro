package middleware

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/config"
)

type Token struct {
	Username string `json:"username"`
	jwt.RegisteredClaims
}

// meshAuthKeyType is a private context-key type so the mesh-auth marker can't
// collide with any other context value.
type meshAuthKeyType struct{}

// MeshAuthKey is the request-context key set on a request that arrived over the
// AllMyStuff mesh "sites" tunnel. Mesh roster membership replaces the KVM login
// for these requests, so the token check below treats them as authenticated.
var MeshAuthKey = meshAuthKeyType{}

// WithMeshAuth returns a copy of r whose context is marked mesh-authenticated.
// The mesh site-tunnel HTTP handler wraps every tunneled request with this so
// the in-process gin engine serves it without a login cookie, while ordinary
// LAN/direct requests (which never pass through here) are unaffected.
func WithMeshAuth(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), MeshAuthKey, true))
}

// isMeshAuthed reports whether r was marked mesh-authenticated by WithMeshAuth.
func isMeshAuthed(r *http.Request) bool {
	if r == nil {
		return false
	}
	v, ok := r.Context().Value(MeshAuthKey).(bool)
	return ok && v
}

func CheckToken() gin.HandlerFunc {
	return func(c *gin.Context) {
		conf := config.GetInstance()

		if conf.Authentication == "disable" {
			c.Next()
			return
		}

		// A request tunneled in over the AllMyStuff mesh is authenticated by the
		// mesh roster (the daemon proved the peer's identity before any byte
		// reached us), so the KVM login is bypassed for it. Normal LAN/direct
		// requests are never marked this way. This can't be spoofed by the
		// loopback bypass below: a tunneled request's RemoteAddr is the mesh
		// route string (a non-IP), so c.ClientIP() is empty for it.
		if isMeshAuthed(c.Request) {
			c.Next()
			return
		}

		if c.ClientIP() == "127.0.0.1" || c.ClientIP() == "::1" || strings.HasPrefix(c.ClientIP(), "127.") {
			c.Next()
			return
		}

		token, err := c.Cookie("nano-kvm-token")
		if err != nil {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				token = strings.TrimPrefix(authHeader, "Bearer ")
			}
		}

		if token != "" {
			if _, err := ParseJWT(token); err == nil {
				c.Next()
				return
			}
		}

		c.JSON(http.StatusUnauthorized, "unauthorized")
		c.Abort()
	}
}

func GenerateJWT(username string) (string, error) {
	conf := config.GetInstance()

	expireDuration := time.Duration(conf.JWT.RefreshTokenDuration) * time.Second

	claims := Token{
		Username: username,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(expireDuration)),
		},
	}

	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)

	return t.SignedString([]byte(conf.JWT.SecretKey))
}

func ParseJWT(jwtToken string) (*Token, error) {
	conf := config.GetInstance()

	t, err := jwt.ParseWithClaims(jwtToken, &Token{}, func(token *jwt.Token) (interface{}, error) {
		return []byte(conf.JWT.SecretKey), nil
	})
	if err != nil {
		log.Debugf("parse jwt error: %s", err)
		return nil, err
	}

	if claims, ok := t.Claims.(*Token); ok && t.Valid {
		return claims, nil
	}

	return nil, errors.New("invalid token")
}
