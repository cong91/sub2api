package admin

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// PaymentHandler handles admin payment management.
type PaymentHandler struct {
	paymentService *service.PaymentService
	configService  *service.PaymentConfigService
	adminService   service.AdminService
}

// NewPaymentHandler creates a new admin PaymentHandler.
func NewPaymentHandler(paymentService *service.PaymentService, configService *service.PaymentConfigService, adminService service.AdminService) *PaymentHandler {
	return &PaymentHandler{
		paymentService: paymentService,
		configService:  configService,
		adminService:   adminService,
	}
}

// --- Dashboard ---

// GetDashboard returns payment dashboard statistics.
// GET /api/v1/admin/payment/dashboard
func (h *PaymentHandler) GetDashboard(c *gin.Context) {
	days := 30
	if d := c.Query("days"); d != "" {
		if v, err := strconv.Atoi(d); err == nil && v > 0 {
			days = v
		}
	}
	stats, err := h.paymentService.GetDashboardStats(c.Request.Context(), days)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, stats)
}

// --- Orders ---

// ListOrders returns a paginated list of all payment orders.
// GET /api/v1/admin/payment/orders
func (h *PaymentHandler) ListOrders(c *gin.Context) {
	page, pageSize := response.ParsePagination(c)
	var userID int64
	if uid := c.Query("user_id"); uid != "" {
		if v, err := strconv.ParseInt(uid, 10, 64); err == nil {
			userID = v
		}
	}
	var explicitUserID *int64
	if userID > 0 {
		explicitUserID = &userID
	}
	scopedUserIDs, ok := restrictExplicitUserIDToMarketingScope(c, h.adminService, explicitUserID)
	if !ok {
		return
	}
	orders, total, err := h.paymentService.AdminListOrders(c.Request.Context(), userID, service.OrderListParams{
		Page:        page,
		UserIDs:     scopedUserIDs,
		PageSize:    pageSize,
		Status:      c.Query("status"),
		OrderType:   c.Query("order_type"),
		PaymentType: c.Query("payment_type"),
		Keyword:     c.Query("keyword"),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	sanitized := sanitizeAdminPaymentOrdersForResponse(orders)

	// Enrich orders with device_code from user_devices
	enriched := enrichOrdersWithDeviceCode(c.Request.Context(), h.paymentService, sanitized)
	response.Paginated(c, enriched, int64(total), page, pageSize)
}

// GetOrderDetail returns detailed information about a single order.
// GET /api/v1/admin/payment/orders/:id
func (h *PaymentHandler) GetOrderDetail(c *gin.Context) {
	orderID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	order, err := h.paymentService.GetOrderByID(c.Request.Context(), orderID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !ensureMarketingCanManageUser(c, h.adminService, order.UserID) {
		return
	}
	auditLogs, _ := h.paymentService.GetOrderAuditLogs(c.Request.Context(), orderID)
	response.Success(c, gin.H{"order": sanitizeAdminPaymentOrderForResponse(order), "auditLogs": auditLogs})
}

// CancelOrder cancels a pending order (admin).
// POST /api/v1/admin/payment/orders/:id/cancel
func (h *PaymentHandler) CancelOrder(c *gin.Context) {
	orderID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	order, err := h.paymentService.GetOrderByID(c.Request.Context(), orderID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !ensureMarketingCanManageUser(c, h.adminService, order.UserID) {
		return
	}

	msg, err := h.paymentService.AdminCancelOrder(c.Request.Context(), orderID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": msg})
}

// RetryFulfillment retries fulfillment for a paid order.
// POST /api/v1/admin/payment/orders/:id/retry
func (h *PaymentHandler) RetryFulfillment(c *gin.Context) {
	orderID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	order, err := h.paymentService.GetOrderByID(c.Request.Context(), orderID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !ensureMarketingCanManageUser(c, h.adminService, order.UserID) {
		return
	}

	if err := h.paymentService.RetryFulfillment(c.Request.Context(), orderID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "fulfillment retried"})
}

// AdminCompleteManualOrder marks a pending manual QR payment as paid and runs fulfillment.
// POST /api/v1/admin/payment/orders/:id/manual-complete
func (h *PaymentHandler) AdminCompleteManualOrder(c *gin.Context) {
	orderID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	order, err := h.paymentService.GetOrderByID(c.Request.Context(), orderID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !ensureMarketingCanManageUser(c, h.adminService, order.UserID) {
		return
	}

	var req struct {
		TradeNo string `json:"trade_no"`
		Note    string `json:"note"`
	}
	if err := c.ShouldBindJSON(&req); err != nil && err != io.EOF {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	adminUserID := int64(0)
	if subject, ok := servermiddleware.GetAuthSubjectFromContext(c); ok {
		adminUserID = subject.UserID
	}
	if err := h.paymentService.AdminCompleteManualOrder(c.Request.Context(), orderID, adminUserID, req.TradeNo, req.Note); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "manual payment completed"})
}

// UploadManualQRCode accepts a small QR-code image and returns a data URL that
// can be stored in provider config under manualQrCodeImg.
// POST /api/v1/admin/payment/providers/manual-qr
func (h *PaymentHandler) UploadManualQRCode(c *gin.Context) {
	const maxManualQRBytes = 1024 * 1024
	file, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "file is required")
		return
	}
	if file.Size <= 0 || file.Size > maxManualQRBytes {
		response.BadRequest(c, "file must be a non-empty image up to 1MB")
		return
	}
	src, err := file.Open()
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "failed to read upload")
		return
	}
	defer func() { _ = src.Close() }()
	data, err := io.ReadAll(io.LimitReader(src, maxManualQRBytes+1))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, "failed to read upload")
		return
	}
	if len(data) == 0 || len(data) > maxManualQRBytes {
		response.BadRequest(c, "file must be a non-empty image up to 1MB")
		return
	}
	contentType := http.DetectContentType(data)
	if !strings.HasPrefix(contentType, "image/") {
		response.BadRequest(c, "file must be an image")
		return
	}
	dataURL := fmt.Sprintf("data:%s;base64,%s", contentType, base64.StdEncoding.EncodeToString(data))
	response.Success(c, gin.H{"url": dataURL, "content_type": contentType, "size": len(data)})
}

func sanitizeAdminPaymentOrdersForResponse(orders []*dbent.PaymentOrder) []*dbent.PaymentOrder {
	if len(orders) == 0 {
		return orders
	}
	out := make([]*dbent.PaymentOrder, 0, len(orders))
	for _, order := range orders {
		out = append(out, sanitizeAdminPaymentOrderForResponse(order))
	}
	return out
}

// AdminOrderWithDeviceCode wraps a PaymentOrder with an optional device_code field.
type AdminOrderWithDeviceCode struct {
	*dbent.PaymentOrder
	DeviceCode string `json:"device_code,omitempty"`
}

func enrichOrdersWithDeviceCode(ctx context.Context, paymentService *service.PaymentService, orders []*dbent.PaymentOrder) []AdminOrderWithDeviceCode {
	if len(orders) == 0 {
		return nil
	}
	// Collect unique user IDs
	userIDSet := make(map[int64]struct{}, len(orders))
	for _, o := range orders {
		if o.UserID > 0 {
			userIDSet[o.UserID] = struct{}{}
		}
	}
	userIDs := make([]int64, 0, len(userIDSet))
	for id := range userIDSet {
		userIDs = append(userIDs, id)
	}

	deviceCodes := paymentService.GetDeviceCodesByUserIDs(ctx, userIDs)

	result := make([]AdminOrderWithDeviceCode, len(orders))
	for i, o := range orders {
		result[i] = AdminOrderWithDeviceCode{
			PaymentOrder: o,
			DeviceCode:   deviceCodes[o.UserID],
		}
	}
	return result
}

func sanitizeAdminPaymentOrderForResponse(order *dbent.PaymentOrder) *dbent.PaymentOrder {
	if order == nil {
		return nil
	}
	cloned := *order
	if len(order.ProviderSnapshot) > 0 {
		cloned.ProviderSnapshot = sanitizeAdminProviderSnapshot(order.ProviderSnapshot)
	} else {
		cloned.ProviderSnapshot = nil
	}
	return &cloned
}

func sanitizeAdminProviderSnapshot(snapshot map[string]any) map[string]any {
	out := make(map[string]any)
	for _, key := range []string{"schema_version", "provider_instance_id", "provider_key", "payment_mode", "currency"} {
		if value, ok := snapshot[key]; ok {
			out[key] = value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AdminProcessRefundRequest is the request body for admin refund processing.
type AdminProcessRefundRequest struct {
	Amount        float64 `json:"amount"`
	Reason        string  `json:"reason"`
	Force         bool    `json:"force"`
	DeductBalance bool    `json:"deduct_balance"`
}

// ProcessRefund processes a refund for an order (admin).
// POST /api/v1/admin/payment/orders/:id/refund
func (h *PaymentHandler) ProcessRefund(c *gin.Context) {
	orderID, ok := parseIDParam(c, "id")
	if !ok {
		return
	}

	var req AdminProcessRefundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	order, err := h.paymentService.GetOrderByID(c.Request.Context(), orderID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if !ensureMarketingCanManageUser(c, h.adminService, order.UserID) {
		return
	}

	plan, earlyResult, err := h.paymentService.PrepareRefund(c.Request.Context(), orderID, req.Amount, req.Reason, req.Force, req.DeductBalance)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if earlyResult != nil {
		response.Success(c, earlyResult)
		return
	}

	result, err := h.paymentService.ExecuteRefund(c.Request.Context(), plan)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// --- Subscription Plans ---

// ListPlans returns all subscription plans.
// GET /api/v1/admin/payment/plans
func (h *PaymentHandler) ListPlans(c *gin.Context) {
	plans, err := h.configService.ListPlans(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, plans)
}

// CreatePlan creates a new subscription plan.
// POST /api/v1/admin/payment/plans
func (h *PaymentHandler) CreatePlan(c *gin.Context) {
	var req service.CreatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	plan, err := h.configService.CreatePlan(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, plan)
}

// UpdatePlan updates an existing subscription plan.
// PUT /api/v1/admin/payment/plans/:id
func (h *PaymentHandler) UpdatePlan(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.UpdatePlanRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	plan, err := h.configService.UpdatePlan(c.Request.Context(), id, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, plan)
}

// DeletePlan deletes a subscription plan.
// DELETE /api/v1/admin/payment/plans/:id
func (h *PaymentHandler) DeletePlan(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.configService.DeletePlan(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "deleted"})
}

// --- Balance Packages ---

// ListBalancePackages returns all balance recharge packages.
// GET /api/v1/admin/payment/balance-packages
func (h *PaymentHandler) ListBalancePackages(c *gin.Context) {
	packages, err := h.configService.ListBalanceRechargePackages(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, packages)
}

// CreateBalancePackage creates a new balance recharge package.
// POST /api/v1/admin/payment/balance-packages
func (h *PaymentHandler) CreateBalancePackage(c *gin.Context) {
	var req service.CreateBalancePackageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	pkg, err := h.configService.CreateBalancePackage(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, pkg)
}

// UpdateBalancePackage updates an existing balance recharge package.
// PUT /api/v1/admin/payment/balance-packages/:id
func (h *PaymentHandler) UpdateBalancePackage(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.UpdateBalancePackageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	pkg, err := h.configService.UpdateBalancePackage(c.Request.Context(), id, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, pkg)
}

// DeleteBalancePackage deletes a balance recharge package.
// DELETE /api/v1/admin/payment/balance-packages/:id
func (h *PaymentHandler) DeleteBalancePackage(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.configService.DeleteBalancePackage(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "deleted"})
}

// --- Provider Instances ---

// ListProviders returns all payment provider instances.
// GET /api/v1/admin/payment/providers
func (h *PaymentHandler) ListProviders(c *gin.Context) {
	providers, err := h.configService.ListProviderInstancesWithConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, providers)
}

// ListSepayBankAccounts returns SePay bank accounts for a temporary API token.
// POST /api/v1/admin/payment/providers/sepay/bank-accounts
func (h *PaymentHandler) ListSepayBankAccounts(c *gin.Context) {
	var req service.ListSepayBankAccountsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	accounts, err := h.configService.ListSepayBankAccounts(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, accounts)
}

// CreateProvider creates a new payment provider instance.
// POST /api/v1/admin/payment/providers
func (h *PaymentHandler) CreateProvider(c *gin.Context) {
	var req service.CreateProviderInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	inst, err := h.configService.CreateProviderInstance(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.paymentService.RefreshProviders(c.Request.Context())
	response.Created(c, inst)
}

// UpdateProvider updates an existing payment provider instance.
// PUT /api/v1/admin/payment/providers/:id
func (h *PaymentHandler) UpdateProvider(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	var req service.UpdateProviderInstanceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	inst, err := h.configService.UpdateProviderInstance(c.Request.Context(), id, req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.paymentService.RefreshProviders(c.Request.Context())
	response.Success(c, inst)
}

// DeleteProvider deletes a payment provider instance.
// DELETE /api/v1/admin/payment/providers/:id
func (h *PaymentHandler) DeleteProvider(c *gin.Context) {
	id, ok := parseIDParam(c, "id")
	if !ok {
		return
	}
	if err := h.configService.DeleteProviderInstance(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.paymentService.RefreshProviders(c.Request.Context())
	response.Success(c, gin.H{"message": "deleted"})
}

// parseIDParam parses an int64 path parameter.
// Returns the parsed ID and true on success; on failure it writes a BadRequest response and returns false.
func parseIDParam(c *gin.Context, paramName string) (int64, bool) {
	id, err := strconv.ParseInt(c.Param(paramName), 10, 64)
	if err != nil {
		response.BadRequest(c, "Invalid "+paramName)
		return 0, false
	}
	return id, true
}

// --- Config ---

// GetConfig returns the payment configuration (admin view).
// GET /api/v1/admin/payment/config
func (h *PaymentHandler) GetConfig(c *gin.Context) {
	cfg, err := h.configService.GetPaymentConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}

// UpdateConfig updates the payment configuration.
// PUT /api/v1/admin/payment/config
func (h *PaymentHandler) UpdateConfig(c *gin.Context) {
	var req service.UpdatePaymentConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.configService.UpdatePaymentConfig(c.Request.Context(), req); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "updated"})
}
