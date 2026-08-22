package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/aisummoner/aisummoner/internal/auth"
	"github.com/aisummoner/aisummoner/internal/id"
	"github.com/aisummoner/aisummoner/internal/pairing"
	"github.com/aisummoner/aisummoner/internal/store"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (a *API) handleLogin(writer http.ResponseWriter, request *http.Request) {
	address, err := a.sourceResolver.Resolve(request)
	if err != nil {
		a.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request")
		return
	}
	now := a.now()
	if !a.loginLimiter.allow(address, now) {
		a.writeError(writer, request, http.StatusTooManyRequests, "RATE_LIMITED", "too many failed login attempts")
		return
	}
	var body loginRequest
	if err := decodeJSON(writer, request, &body); err != nil {
		a.loginLimiter.failed(address, now)
		a.writeError(writer, request, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	result, err := a.auth.Login(request.Context(), body.Username, body.Password, now)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		a.loginLimiter.failed(address, now)
		a.audit(request, nil, nil, "auth.login_failed")
		a.writeError(writer, request, http.StatusUnauthorized, "INVALID_CREDENTIALS", "invalid username or password")
		return
	}
	if errors.Is(err, auth.ErrVerificationBusy) {
		a.writeError(writer, request, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "authentication service is busy")
		return
	}
	if err != nil {
		a.internalError(writer, request, err)
		return
	}
	a.loginLimiter.succeeded(address)
	http.SetCookie(writer, &http.Cookie{
		Name: SessionCookieName, Value: result.Token, Path: "/", HttpOnly: true,
		Secure: a.cookieSecure, SameSite: http.SameSiteStrictMode,
		Expires: result.ExpiresAt, MaxAge: int(auth.SessionDuration.Seconds()),
	})
	a.audit(request, &result.User.ID, nil, "auth.login_succeeded")
	a.writeJSON(writer, http.StatusOK, map[string]any{"user": userResponse(result.User)})
}

func (a *API) handleLogout(writer http.ResponseWriter, request *http.Request) {
	authenticated, ok := a.authenticate(writer, request)
	if !ok {
		return
	}
	user, _ := userFromContext(authenticated)
	cookie, _ := authenticated.Cookie(SessionCookieName)
	if cookie != nil {
		if err := a.auth.Logout(authenticated.Context(), cookie.Value); err != nil {
			a.internalError(writer, authenticated, err)
			return
		}
	}
	http.SetCookie(writer, &http.Cookie{
		Name: SessionCookieName, Value: "", Path: "/", HttpOnly: true,
		Secure: a.cookieSecure, SameSite: http.SameSiteStrictMode,
		Expires: time.Unix(1, 0), MaxAge: -1,
	})
	a.audit(authenticated, &user.ID, nil, "auth.logout")
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) handleMe(writer http.ResponseWriter, request *http.Request) {
	authenticated, ok := a.authenticate(writer, request)
	if !ok {
		return
	}
	user, _ := userFromContext(authenticated)
	a.writeJSON(writer, http.StatusOK, map[string]any{"user": userResponse(user)})
}

type pairingClaimRequest struct {
	Code string `json:"code"`
}

func (a *API) handlePairingClaim(writer http.ResponseWriter, request *http.Request) {
	authenticated, ok := a.authenticate(writer, request)
	if !ok {
		return
	}
	address, err := a.sourceResolver.Resolve(authenticated)
	if err != nil {
		a.writeError(writer, authenticated, http.StatusBadRequest, "INVALID_REQUEST", "invalid request")
		return
	}
	now := a.now()
	if !a.claimLimiter.allow(address, now) {
		a.writeError(writer, authenticated, http.StatusTooManyRequests, "RATE_LIMITED", "too many failed pairing attempts")
		return
	}
	var body pairingClaimRequest
	if err := decodeJSON(writer, authenticated, &body); err != nil {
		a.claimLimiter.failed(address, now)
		a.writeError(writer, authenticated, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	user, _ := userFromContext(authenticated)
	pairedDevice, err := a.pairing.Claim(authenticated.Context(), user.ID, body.Code, now)
	if errors.Is(err, pairing.ErrInvalidCode) {
		a.claimLimiter.failed(address, now)
		a.writeError(writer, authenticated, http.StatusBadRequest, "PAIRING_CODE_INVALID", "pairing code is invalid or already used")
		return
	}
	if errors.Is(err, store.ErrPairingExpired) {
		a.claimLimiter.failed(address, now)
		a.writeError(writer, authenticated, http.StatusGone, "PAIRING_CODE_EXPIRED", "pairing code has expired")
		return
	}
	if errors.Is(err, store.ErrConflict) {
		a.claimLimiter.failed(address, now)
		a.writeError(writer, authenticated, http.StatusConflict, "DEVICE_ALREADY_PAIRED", "device is already paired")
		return
	}
	if err != nil {
		a.internalError(writer, authenticated, err)
		return
	}
	a.claimLimiter.succeeded(address)
	a.audit(authenticated, &user.ID, &pairedDevice.ID, "device.paired")
	view, err := a.devices.Get(authenticated.Context(), user.ID, pairedDevice.ID)
	if err != nil {
		a.internalError(writer, authenticated, err)
		return
	}
	a.writeJSON(writer, http.StatusOK, map[string]any{"device": deviceResponse(view)})
}

func (a *API) handleDevices(writer http.ResponseWriter, request *http.Request) {
	authenticated, ok := a.authenticate(writer, request)
	if !ok {
		return
	}
	user, _ := userFromContext(authenticated)
	views, err := a.devices.List(authenticated.Context(), user.ID)
	if err != nil {
		a.internalError(writer, authenticated, err)
		return
	}
	response := make([]any, 0, len(views))
	for _, view := range views {
		response = append(response, deviceResponse(view))
	}
	a.writeJSON(writer, http.StatusOK, map[string]any{"devices": response})
}

func (a *API) handleDevice(writer http.ResponseWriter, request *http.Request, deviceID string) {
	authenticated, ok := a.authenticate(writer, request)
	if !ok {
		return
	}
	user, _ := userFromContext(authenticated)
	view, err := a.devices.Get(authenticated.Context(), user.ID, deviceID)
	if errors.Is(err, store.ErrNotFound) {
		a.writeError(writer, authenticated, http.StatusNotFound, "DEVICE_NOT_FOUND", "device not found")
		return
	}
	if err != nil {
		a.internalError(writer, authenticated, err)
		return
	}
	a.writeJSON(writer, http.StatusOK, map[string]any{"device": deviceResponse(view)})
}

func (a *API) handleDeviceDelete(writer http.ResponseWriter, request *http.Request, deviceID string) {
	authenticated, ok := a.authenticate(writer, request)
	if !ok {
		return
	}
	user, _ := userFromContext(authenticated)
	if err := a.devices.Unpair(authenticated.Context(), user.ID, deviceID, a.now()); errors.Is(err, store.ErrNotFound) {
		a.writeError(writer, authenticated, http.StatusNotFound, "DEVICE_NOT_FOUND", "device not found")
		return
	} else if err != nil {
		a.internalError(writer, authenticated, err)
		return
	}
	a.audit(authenticated, &user.ID, &deviceID, "device.unpaired")
	writer.WriteHeader(http.StatusNoContent)
}

func (a *API) audit(request *http.Request, actorUserID, deviceID *string, eventType string) {
	if a.auditor == nil {
		return
	}
	eventID, err := id.New("audit")
	if err != nil {
		a.logger.Error("create audit id", "request_id", requestID(request), "error", err)
		return
	}
	if err := a.auditor.CreateAuditEvent(request.Context(), store.AuditEvent{
		ID: eventID, ActorUserID: actorUserID, DeviceID: deviceID,
		EventType: eventType, MetadataJSON: "{}", CreatedAt: a.now().UTC(),
	}); err != nil {
		a.logger.Error("write audit event", "request_id", requestID(request), "event_type", eventType, "error", err)
	}
}
