package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AdminAPIKeyHandler handles admin API key management
type AdminAPIKeyHandler struct {
	adminService service.AdminService
}

// NewAdminAPIKeyHandler creates a new admin API key handler
func NewAdminAPIKeyHandler(adminService service.AdminService) *AdminAPIKeyHandler {
	return &AdminAPIKeyHandler{
		adminService: adminService,
	}
}

// AdminUpdateAPIKeyGroupRequest represents the request to update an API key's
// canonical membership group_ids[]. This narrow admin lane still materializes a
// single effective execution group by accepting either [] or [x].
type AdminUpdateAPIKeyGroupRequest struct {
	GroupIDs *[]int64 `json:"group_ids"` // nil=no-op, []=unbind all, [x]=bind exactly one membership group; request-time effective scalar is derived later
}

// UpdateGroup handles updating an API key's membership groups in the narrow
// admin single-effective-group lane.
// PUT /api/v1/admin/api-keys/:id
func (h *AdminAPIKeyHandler) UpdateGroup(c *gin.Context) {
	keyID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid API key ID")
		return
	}

	var req AdminUpdateAPIKeyGroupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	var groupID *int64
	if req.GroupIDs != nil {
		switch len(*req.GroupIDs) {
		case 0:
			zero := int64(0)
			groupID = &zero
		case 1:
			gid := (*req.GroupIDs)[0]
			groupID = &gid
		default:
			response.BadRequest(c, "group_ids must contain at most one group in admin update path")
			return
		}
	}

	result, err := h.adminService.AdminUpdateAPIKeyGroupID(c.Request.Context(), keyID, groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	resp := struct {
		APIKey                 *dto.APIKey `json:"api_key"`
		AutoGrantedGroupAccess bool        `json:"auto_granted_group_access"`
		AutoGrantedGroupName   string      `json:"auto_granted_group_name,omitempty"`
	}{
		APIKey:                 dto.APIKeyFromService(result.APIKey),
		AutoGrantedGroupAccess: result.AutoGrantedGroupAccess,
		AutoGrantedGroupName:   result.AutoGrantedGroupName,
	}
	response.Success(c, resp)
}
