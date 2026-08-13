package web

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Ssrrb/go-football/internal/auth"
)

const (
	sessionCookieName = "football_session"
	csrfCookieName    = "football_csrf"
	oauthStateCookie  = "football_oauth_state"
	oauthPKCECookie   = "football_oauth_pkce"
)

type authPageData struct {
	CSRFToken     string
	ErrorMessage  string
	GoogleEnabled bool
}

func (s *server) loginPage(response http.ResponseWriter, request *http.Request) {
	if s.currentUser(response, request) != nil {
		http.Redirect(response, request, "/", http.StatusSeeOther)
		return
	}
	s.renderAuth(response, request, "login_page", authPageData{
		CSRFToken: s.csrfToken(response, request), GoogleEnabled: s.auth.GoogleEnabled(),
	})
}

func (s *server) registerPage(response http.ResponseWriter, request *http.Request) {
	if s.currentUser(response, request) != nil {
		http.Redirect(response, request, "/", http.StatusSeeOther)
		return
	}
	s.renderAuth(response, request, "register_page", authPageData{
		CSRFToken: s.csrfToken(response, request), GoogleEnabled: s.auth.GoogleEnabled(),
	})
}

func (s *server) login(response http.ResponseWriter, request *http.Request) {
	data := authPageData{CSRFToken: s.csrfToken(response, request), GoogleEnabled: s.auth.GoogleEnabled()}
	if !s.validForm(response, request, data.CSRFToken) {
		data.ErrorMessage = "Your form expired. Please try again."
		s.renderAuthForm(response, request, "login_form", data)
		return
	}
	user, err := s.auth.Authenticate(request.Context(), request.FormValue("email"), request.FormValue("password"))
	if err != nil {
		if !errors.Is(err, auth.ErrInvalidCredentials) {
			s.report(request.Context(), "authenticate user", err)
			data.ErrorMessage = "Sign in is temporarily unavailable."
		} else {
			data.ErrorMessage = "Email or password is incorrect."
		}
		s.renderAuthForm(response, request, "login_form", data)
		return
	}
	if !s.startSession(response, request, user) {
		data.ErrorMessage = "Sign in is temporarily unavailable."
		s.renderAuthForm(response, request, "login_form", data)
		return
	}
	s.authSuccess(response, request)
}

func (s *server) register(response http.ResponseWriter, request *http.Request) {
	data := authPageData{CSRFToken: s.csrfToken(response, request), GoogleEnabled: s.auth.GoogleEnabled()}
	if !s.validForm(response, request, data.CSRFToken) {
		data.ErrorMessage = "Your form expired. Please try again."
		s.renderAuthForm(response, request, "register_form", data)
		return
	}
	user, err := s.auth.Register(request.Context(), request.FormValue("email"), request.FormValue("password"), request.FormValue("display_name"))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrEmailInUse):
			data.ErrorMessage = "An account with that email already exists."
		case errors.Is(err, auth.ErrInvalidInput):
			data.ErrorMessage = "Enter a valid email, a name, and a password between 8 and 72 characters."
		default:
			s.report(request.Context(), "register user", err)
			data.ErrorMessage = "Registration is temporarily unavailable."
		}
		s.renderAuthForm(response, request, "register_form", data)
		return
	}
	if !s.startSession(response, request, user) {
		data.ErrorMessage = "Your account was created, but sign in is temporarily unavailable."
		s.renderAuthForm(response, request, "login_form", data)
		return
	}
	s.authSuccess(response, request)
}

func (s *server) logout(response http.ResponseWriter, request *http.Request) {
	csrf := s.csrfToken(response, request)
	if !s.validForm(response, request, csrf) {
		http.Error(response, "invalid CSRF token", http.StatusForbidden)
		return
	}
	if cookie, err := request.Cookie(sessionCookieName); err == nil {
		if err := s.auth.RevokeSession(request.Context(), cookie.Value); err != nil {
			s.report(request.Context(), "revoke session", err)
		}
	}
	s.clearCookie(response, sessionCookieName, http.SameSiteLaxMode)
	if request.Header.Get("HX-Request") == "true" {
		response.Header().Set("HX-Redirect", "/")
		response.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func (s *server) googleStart(response http.ResponseWriter, request *http.Request) {
	if !s.auth.GoogleEnabled() {
		http.NotFound(response, request)
		return
	}
	state, err := auth.RandomToken(32)
	if err != nil {
		s.report(request.Context(), "generate OAuth state", err)
		http.Error(response, "Google sign in is unavailable", http.StatusServiceUnavailable)
		return
	}
	verifier, err := auth.RandomToken(48)
	if err != nil {
		s.report(request.Context(), "generate PKCE verifier", err)
		http.Error(response, "Google sign in is unavailable", http.StatusServiceUnavailable)
		return
	}
	s.setCookie(response, oauthStateCookie, state, 10*time.Minute, true, http.SameSiteLaxMode)
	s.setCookie(response, oauthPKCECookie, verifier, 10*time.Minute, true, http.SameSiteLaxMode)
	http.Redirect(response, request, s.auth.GoogleAuthURL(state, auth.PKCEChallenge(verifier)), http.StatusFound)
}

func (s *server) googleCallback(response http.ResponseWriter, request *http.Request) {
	stateCookie, stateErr := request.Cookie(oauthStateCookie)
	pkceCookie, pkceErr := request.Cookie(oauthPKCECookie)
	s.clearCookie(response, oauthStateCookie, http.SameSiteLaxMode)
	s.clearCookie(response, oauthPKCECookie, http.SameSiteLaxMode)
	state := request.URL.Query().Get("state")
	if stateErr != nil || pkceErr != nil || state == "" || !secureEqual(state, stateCookie.Value) || request.URL.Query().Get("error") != "" {
		s.renderAuth(response, request, "login_page", authPageData{
			CSRFToken: s.csrfToken(response, request), GoogleEnabled: s.auth.GoogleEnabled(),
			ErrorMessage: "Google sign in was cancelled or expired.",
		})
		return
	}
	user, err := s.auth.AuthenticateGoogle(request.Context(), request.URL.Query().Get("code"), pkceCookie.Value)
	if err != nil {
		s.logger.Warn("Google sign in failed", "error", err)
		s.renderAuth(response, request, "login_page", authPageData{
			CSRFToken: s.csrfToken(response, request), GoogleEnabled: s.auth.GoogleEnabled(),
			ErrorMessage: "We could not verify that Google account.",
		})
		return
	}
	if !s.startSession(response, request, user) {
		http.Error(response, "sign in is temporarily unavailable", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func (s *server) startSession(response http.ResponseWriter, request *http.Request, user auth.User) bool {
	token, err := s.auth.NewSession(request.Context(), user.ID)
	if err != nil {
		s.report(request.Context(), "create session", err)
		return false
	}
	s.setCookie(response, sessionCookieName, token, s.auth.SessionDuration(), true, http.SameSiteLaxMode)
	return true
}

func (s *server) currentUser(response http.ResponseWriter, request *http.Request) *auth.User {
	cookie, err := request.Cookie(sessionCookieName)
	if err != nil {
		return nil
	}
	user, err := s.auth.UserBySession(request.Context(), cookie.Value)
	if err != nil {
		s.clearCookie(response, sessionCookieName, http.SameSiteLaxMode)
		if !errors.Is(err, auth.ErrInvalidSession) {
			s.report(request.Context(), "load session", err)
		}
		return nil
	}
	return &user
}

func (s *server) csrfToken(response http.ResponseWriter, request *http.Request) string {
	if cookie, err := request.Cookie(csrfCookieName); err == nil && len(cookie.Value) >= 32 {
		return cookie.Value
	}
	token, err := auth.RandomToken(32)
	if err != nil {
		s.report(request.Context(), "generate CSRF token", err)
		return ""
	}
	s.setCookie(response, csrfCookieName, token, 24*time.Hour, false, http.SameSiteStrictMode)
	return token
}

func (s *server) validForm(response http.ResponseWriter, request *http.Request, expected string) bool {
	request.Body = http.MaxBytesReader(response, request.Body, 64<<10)
	if err := request.ParseForm(); err != nil {
		return false
	}
	return expected != "" && secureEqual(expected, request.Form.Get("csrf_token"))
}

func secureEqual(left, right string) bool {
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func (s *server) setCookie(response http.ResponseWriter, name, value string, lifetime time.Duration, httpOnly bool, sameSite http.SameSite) {
	http.SetCookie(response, &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: httpOnly,
		Secure: s.cookieSecure, SameSite: sameSite,
		MaxAge: int(lifetime.Seconds()), Expires: time.Now().Add(lifetime),
	})
}

func (s *server) clearCookie(response http.ResponseWriter, name string, sameSite http.SameSite) {
	http.SetCookie(response, &http.Cookie{
		Name: name, Value: "", Path: "/", HttpOnly: name != csrfCookieName,
		Secure: s.cookieSecure, SameSite: sameSite, MaxAge: -1, Expires: time.Unix(1, 0),
	})
}

func (s *server) authSuccess(response http.ResponseWriter, request *http.Request) {
	if strings.EqualFold(request.Header.Get("HX-Request"), "true") {
		response.Header().Set("HX-Redirect", "/")
		response.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func (s *server) renderAuth(response http.ResponseWriter, request *http.Request, name string, data authPageData) {
	response.Header().Set("Cache-Control", "no-store")
	s.render(response, request, http.StatusOK, name, data)
}

func (s *server) renderAuthForm(response http.ResponseWriter, request *http.Request, name string, data authPageData) {
	response.Header().Set("Cache-Control", "no-store")
	s.render(response, request, http.StatusOK, name, data)
}
