package handler

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthHandler_InviteLogin_RequiresInvitationCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AuthHandler{}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/invite-login", bytes.NewBufferString(`{"invitation_code":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = req

	h.InviteLogin(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "Invalid request")
}
