package proxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const capabilityTTL = 15 * time.Minute

type capabilityPayload struct {
	InstanceID string `json:"i"`
	ExpiresAt  int64  `json:"e"`
}

func (s *Server) cookieName() string { return "kandev_bridge_" + s.instance.ID }

// SetCapabilityCookie grants access to this instance's bridge. Call only after
// authenticating locally and authorizing the instance owner. Cookies are host-only
// and shared across ports; the instance-specific name and signature isolate bridges.
func (s *Server) SetCapabilityCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: s.cookieName(), Value: s.mintCapability(s.instance.ID, time.Now().Add(capabilityTTL)), Path: "/", HttpOnly: true, Secure: r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"), SameSite: http.SameSiteLaxMode, MaxAge: int(capabilityTTL.Seconds())})
}

func (s *Server) mintCapability(instanceID string, expiry time.Time) string {
	raw, _ := json.Marshal(capabilityPayload{InstanceID: instanceID, ExpiresAt: expiry.Unix()})
	return base64.RawURLEncoding.EncodeToString(raw) + "." + s.sign(raw)
}

func (s *Server) sign(raw []byte) string {
	mac := hmac.New(sha256.New, s.secret[:])
	_, _ = mac.Write(raw)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) validCapability(token string) bool {
	encoded, signature, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return false
	}
	if !hmac.Equal([]byte(s.sign(raw)), []byte(signature)) {
		return false
	}
	var payload capabilityPayload
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	return payload.InstanceID == s.instance.ID && time.Now().Unix() < payload.ExpiresAt
}

func (s *Server) requireBridgeAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !bridgeCORS(w, r) {
			return
		}
		if s.authOn {
			cookie, err := r.Cookie(s.cookieName())
			if err != nil || !s.validCapability(cookie.Value) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"bridge_capability_required"}`))
				return
			}
			s.SetCapabilityCookie(w, r)
		}
		next.ServeHTTP(w, r)
	})
}
