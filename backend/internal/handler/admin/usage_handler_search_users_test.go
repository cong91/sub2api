package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestUsageHandlerSearchUsersIncludesDeviceIdentityHints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	code := "DLG-FN7Y-NJQJ-XNV6"
	redeemType := service.RedeemTypeDeviceLogin
	adminSvc := newStubAdminService()
	adminSvc.users = []service.User{{
		ID:                42,
		Email:             "device-user@example.com",
		PrimaryRedeemCode: &code,
		PrimaryRedeemType: &redeemType,
		HasDeviceBinding:  true,
	}}

	handler := NewUsageHandler(nil, nil, adminSvc, nil)
	router := gin.New()
	router.GET("/admin/usage/search-users", handler.SearchUsers)

	req := httptest.NewRequest(http.MethodGet, "/admin/usage/search-users?q=DLG-FN7Y", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, adminSvc.lastListUsers.calls)
	require.Equal(t, "DLG-FN7Y", adminSvc.lastListUsers.filters.Search)
	require.NotNil(t, adminSvc.lastListUsers.filters.IncludeSubscriptions)
	require.False(t, *adminSvc.lastListUsers.filters.IncludeSubscriptions)

	var body struct {
		Code int `json:"code"`
		Data []struct {
			ID                int64   `json:"id"`
			Email             string  `json:"email"`
			PrimaryRedeemCode *string `json:"primary_redeem_code"`
			PrimaryRedeemType *string `json:"primary_redeem_type"`
			HasDeviceBinding  bool    `json:"has_device_binding"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Len(t, body.Data, 1)
	require.Equal(t, int64(42), body.Data[0].ID)
	require.Equal(t, "device-user@example.com", body.Data[0].Email)
	require.NotNil(t, body.Data[0].PrimaryRedeemCode)
	require.Equal(t, code, *body.Data[0].PrimaryRedeemCode)
	require.NotNil(t, body.Data[0].PrimaryRedeemType)
	require.Equal(t, redeemType, *body.Data[0].PrimaryRedeemType)
	require.True(t, body.Data[0].HasDeviceBinding)
}
