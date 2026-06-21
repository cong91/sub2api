//go:build unit

package response

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	errors2 "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// ---------- 辅助函数 ----------

// parseResponseBody 从 httptest.ResponseRecorder 中解析 JSON 响应体
func parseResponseBody(t *testing.T, w *httptest.ResponseRecorder) Response {
	t.Helper()
	var got Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	return got
}

// parsePaginatedBody 从响应体中解析分页数据（Data 字段是 PaginatedData）
func parsePaginatedBody(t *testing.T, w *httptest.ResponseRecorder) (Response, PaginatedData) {
	t.Helper()
	// 先用 raw json 解析，因为 Data 是 any 类型
	var raw struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Reason  string          `json:"reason,omitempty"`
		Data    json.RawMessage `json:"data,omitempty"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))

	var pd PaginatedData
	require.NoError(t, json.Unmarshal(raw.Data, &pd))

	return Response{Code: raw.Code, Message: raw.Message, Reason: raw.Reason}, pd
}

// newContextWithQuery 创建一个带有 URL query 参数的 gin.Context 用于测试 ParsePagination
func newContextWithQuery(query string) (*httptest.ResponseRecorder, *gin.Context) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
	return w, c
}

// ---------- 现有测试 ----------

func TestErrorWithDetails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		statusCode int
		message    string
		reason     string
		metadata   map[string]string
		want       Response
	}{
		{
			name:       "plain_error",
			statusCode: http.StatusBadRequest,
			message:    "invalid request",
			want: Response{
				Code:    http.StatusBadRequest,
				Message: "invalid request",
			},
		},
		{
			name:       "localized_error_is_normalized",
			statusCode: http.StatusBadRequest,
			message:    "请求格式或参数不正确，请检查 messages 参数",
			want: Response{
				Code:    http.StatusBadRequest,
				Message: "Invalid request format or parameters",
			},
		},
		{
			name:       "structured_error",
			statusCode: http.StatusForbidden,
			message:    "no access",
			reason:     "FORBIDDEN",
			metadata:   map[string]string{"k": "v"},
			want: Response{
				Code:     http.StatusForbidden,
				Message:  "no access",
				Reason:   "FORBIDDEN",
				Metadata: map[string]string{"k": "v"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			ErrorWithDetails(c, tt.statusCode, tt.message, tt.reason, tt.metadata)

			require.Equal(t, tt.statusCode, w.Code)

			var got Response
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			require.Equal(t, tt.want, got)
		})
	}
}

func TestErrorFrom(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name         string
		err          error
		wantWritten  bool
		wantHTTPCode int
		wantBody     Response
	}{
		{
			name:        "nil_error",
			err:         nil,
			wantWritten: false,
		},
		{
			name:         "application_error",
			err:          errors2.Forbidden("FORBIDDEN", "no access").WithMetadata(map[string]string{"scope": "admin"}),
			wantWritten:  true,
			wantHTTPCode: http.StatusForbidden,
			wantBody: Response{
				Code:     http.StatusForbidden,
				Message:  "no access",
				Reason:   "FORBIDDEN",
				Metadata: map[string]string{"scope": "admin"},
			},
		},
		{
			name:         "bad_request_error",
			err:          errors2.BadRequest("INVALID_REQUEST", "invalid request"),
			wantWritten:  true,
			wantHTTPCode: http.StatusBadRequest,
			wantBody: Response{
				Code:    http.StatusBadRequest,
				Message: "invalid request",
				Reason:  "INVALID_REQUEST",
			},
		},
		{
			name:         "unauthorized_error",
			err:          errors2.Unauthorized("UNAUTHORIZED", "unauthorized"),
			wantWritten:  true,
			wantHTTPCode: http.StatusUnauthorized,
			wantBody: Response{
				Code:    http.StatusUnauthorized,
				Message: "unauthorized",
				Reason:  "UNAUTHORIZED",
			},
		},
		{
			name:         "not_found_error",
			err:          errors2.NotFound("NOT_FOUND", "not found"),
			wantWritten:  true,
			wantHTTPCode: http.StatusNotFound,
			wantBody: Response{
				Code:    http.StatusNotFound,
				Message: "not found",
				Reason:  "NOT_FOUND",
			},
		},
		{
			name:         "conflict_error",
			err:          errors2.Conflict("CONFLICT", "conflict"),
			wantWritten:  true,
			wantHTTPCode: http.StatusConflict,
			wantBody: Response{
				Code:    http.StatusConflict,
				Message: "conflict",
				Reason:  "CONFLICT",
			},
		},
		{
			name:         "unknown_error_defaults_to_500",
			err:          errors.New("boom"),
			wantWritten:  true,
			wantHTTPCode: http.StatusInternalServerError,
			wantBody: Response{
				Code:    http.StatusInternalServerError,
				Message: errors2.UnknownMessage,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			written := ErrorFrom(c, tt.err)
			require.Equal(t, tt.wantWritten, written)

			if !tt.wantWritten {
				require.Equal(t, 200, w.Code)
				require.Empty(t, w.Body.String())
				return
			}

			require.Equal(t, tt.wantHTTPCode, w.Code)
			var got Response
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			require.Equal(t, tt.wantBody, got)
		})
	}
}

// ---------- 新增测试 ----------

func TestSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		data     any
		wantCode int
		wantBody Response
	}{
		{
			name:     "返回字符串数据",
			data:     "hello",
			wantCode: http.StatusOK,
			wantBody: Response{Code: 0, Message: "success", Data: "hello"},
		},
		{
			name:     "返回nil数据",
			data:     nil,
			wantCode: http.StatusOK,
			wantBody: Response{Code: 0, Message: "success"},
		},
		{
			name:     "返回map数据",
			data:     map[string]string{"key": "value"},
			wantCode: http.StatusOK,
			wantBody: Response{Code: 0, Message: "success"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			Success(c, tt.data)

			require.Equal(t, tt.wantCode, w.Code)

			// 只验证 code 和 message，data 字段类型在 JSON 反序列化时会变成 map/slice
			got := parseResponseBody(t, w)
			require.Equal(t, 0, got.Code)
			require.Equal(t, "success", got.Message)

			if tt.data == nil {
				require.Nil(t, got.Data)
			} else {
				require.NotNil(t, got.Data)
			}
		})
	}
}

func TestCreated(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		data     any
		wantCode int
	}{
		{
			name:     "创建成功_返回数据",
			data:     map[string]int{"id": 42},
			wantCode: http.StatusCreated,
		},
		{
			name:     "创建成功_nil数据",
			data:     nil,
			wantCode: http.StatusCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			Created(c, tt.data)

			require.Equal(t, tt.wantCode, w.Code)

			got := parseResponseBody(t, w)
			require.Equal(t, 0, got.Code)
			require.Equal(t, "success", got.Message)
		})
	}
}

func TestError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name       string
		statusCode int
		message    string
	}{
		{
			name:       "400错误",
			statusCode: http.StatusBadRequest,
			message:    "bad request",
		},
		{
			name:       "500错误",
			statusCode: http.StatusInternalServerError,
			message:    "internal error",
		},
		{
			name:       "自定义状态码",
			statusCode: 418,
			message:    "I'm a teapot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			Error(c, tt.statusCode, tt.message)

			require.Equal(t, tt.statusCode, w.Code)

			got := parseResponseBody(t, w)
			require.Equal(t, tt.statusCode, got.Code)
			require.Equal(t, tt.message, got.Message)
			require.Empty(t, got.Reason)
			require.Nil(t, got.Metadata)
			require.Nil(t, got.Data)
		})
	}
}

func TestBadRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	BadRequest(c, "参数无效")

	require.Equal(t, http.StatusBadRequest, w.Code)
	got := parseResponseBody(t, w)
	require.Equal(t, http.StatusBadRequest, got.Code)
	require.Equal(t, "Invalid request parameters", got.Message)
}

func TestUnauthorized(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	Unauthorized(c, "未登录")

	require.Equal(t, http.StatusUnauthorized, w.Code)
	got := parseResponseBody(t, w)
	require.Equal(t, http.StatusUnauthorized, got.Code)
	require.Equal(t, "Authentication required", got.Message)
}

func TestForbidden(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	Forbidden(c, "无权限")

	require.Equal(t, http.StatusForbidden, w.Code)
	got := parseResponseBody(t, w)
	require.Equal(t, http.StatusForbidden, got.Code)
	require.Equal(t, "Access forbidden", got.Message)
}

func TestNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	NotFound(c, "资源不存在")

	require.Equal(t, http.StatusNotFound, w.Code)
	got := parseResponseBody(t, w)
	require.Equal(t, http.StatusNotFound, got.Code)
	require.Equal(t, "Resource not found", got.Message)
}

func TestInternalError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	InternalError(c, "服务器内部错误")

	require.Equal(t, http.StatusInternalServerError, w.Code)
	got := parseResponseBody(t, w)
	require.Equal(t, http.StatusInternalServerError, got.Code)
	require.Equal(t, "Internal server error", got.Message)
}

func TestPaginated(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name         string
		items        any
		total        int64
		page         int
		pageSize     int
		wantPages    int
		wantTotal    int64
		wantPage     int
		wantPageSize int
	}{
		{
			name:         "标准分页_多页",
			items:        []string{"a", "b"},
			total:        25,
			page:         1,
			pageSize:     10,
			wantPages:    3,
			wantTotal:    25,
			wantPage:     1,
			wantPageSize: 10,
		},
		{
			name:         "总数刚好整除",
			items:        []string{"a"},
			total:        20,
			page:         2,
			pageSize:     10,
			wantPages:    2,
			wantTotal:    20,
			wantPage:     2,
			wantPageSize: 10,
		},
		{
			name:         "总数为0_pages至少为1",
			items:        []string{},
			total:        0,
			page:         1,
			pageSize:     10,
			wantPages:    1,
			wantTotal:    0,
			wantPage:     1,
			wantPageSize: 10,
		},
		{
			name:         "单页数据",
			items:        []int{1, 2, 3},
			total:        3,
			page:         1,
			pageSize:     20,
			wantPages:    1,
			wantTotal:    3,
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "总数为1",
			items:        []string{"only"},
			total:        1,
			page:         1,
			pageSize:     10,
			wantPages:    1,
			wantTotal:    1,
			wantPage:     1,
			wantPageSize: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			Paginated(c, tt.items, tt.total, tt.page, tt.pageSize)

			require.Equal(t, http.StatusOK, w.Code)

			resp, pd := parsePaginatedBody(t, w)
			require.Equal(t, 0, resp.Code)
			require.Equal(t, "success", resp.Message)
			require.Equal(t, tt.wantTotal, pd.Total)
			require.Equal(t, tt.wantPage, pd.Page)
			require.Equal(t, tt.wantPageSize, pd.PageSize)
			require.Equal(t, tt.wantPages, pd.Pages)
		})
	}
}

func TestPaginatedWithResult(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name         string
		items        any
		pagination   *PaginationResult
		wantTotal    int64
		wantPage     int
		wantPageSize int
		wantPages    int
	}{
		{
			name:  "正常分页结果",
			items: []string{"a", "b"},
			pagination: &PaginationResult{
				Total:    50,
				Page:     3,
				PageSize: 10,
				Pages:    5,
			},
			wantTotal:    50,
			wantPage:     3,
			wantPageSize: 10,
			wantPages:    5,
		},
		{
			name:         "pagination为nil_使用默认值",
			items:        []string{},
			pagination:   nil,
			wantTotal:    0,
			wantPage:     1,
			wantPageSize: 20,
			wantPages:    1,
		},
		{
			name:  "单页结果",
			items: []int{1},
			pagination: &PaginationResult{
				Total:    1,
				Page:     1,
				PageSize: 20,
				Pages:    1,
			},
			wantTotal:    1,
			wantPage:     1,
			wantPageSize: 20,
			wantPages:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)

			PaginatedWithResult(c, tt.items, tt.pagination)

			require.Equal(t, http.StatusOK, w.Code)

			resp, pd := parsePaginatedBody(t, w)
			require.Equal(t, 0, resp.Code)
			require.Equal(t, "success", resp.Message)
			require.Equal(t, tt.wantTotal, pd.Total)
			require.Equal(t, tt.wantPage, pd.Page)
			require.Equal(t, tt.wantPageSize, pd.PageSize)
			require.Equal(t, tt.wantPages, pd.Pages)
		})
	}
}

func TestParsePagination(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name         string
		query        string
		wantPage     int
		wantPageSize int
	}{
		{
			name:         "无参数_使用默认值",
			query:        "",
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "仅指定page",
			query:        "page=3",
			wantPage:     3,
			wantPageSize: 20,
		},
		{
			name:         "仅指定page_size",
			query:        "page_size=50",
			wantPage:     1,
			wantPageSize: 50,
		},
		{
			name:         "同时指定page和page_size",
			query:        "page=2&page_size=30",
			wantPage:     2,
			wantPageSize: 30,
		},
		{
			name:         "使用limit代替page_size",
			query:        "limit=15",
			wantPage:     1,
			wantPageSize: 15,
		},
		{
			name:         "page_size优先于limit",
			query:        "page_size=25&limit=50",
			wantPage:     1,
			wantPageSize: 25,
		},
		{
			name:         "page为0_使用默认值",
			query:        "page=0",
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "page_size超过1000_使用默认值",
			query:        "page_size=1001",
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "page_size恰好1000_有效",
			query:        "page_size=1000",
			wantPage:     1,
			wantPageSize: 1000,
		},
		{
			name:         "page为非数字_使用默认值",
			query:        "page=abc",
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "page_size为非数字_使用默认值",
			query:        "page_size=xyz",
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "limit为非数字_使用默认值",
			query:        "limit=abc",
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "page_size为0_使用默认值",
			query:        "page_size=0",
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "limit为0_使用默认值",
			query:        "limit=0",
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "大页码",
			query:        "page=999&page_size=100",
			wantPage:     999,
			wantPageSize: 100,
		},
		{
			name:         "page_size为1_最小有效值",
			query:        "page_size=1",
			wantPage:     1,
			wantPageSize: 1,
		},
		{
			name:         "混合数字和字母的page",
			query:        "page=12a",
			wantPage:     1,
			wantPageSize: 20,
		},
		{
			name:         "limit超过1000_使用默认值",
			query:        "limit=2000",
			wantPage:     1,
			wantPageSize: 20,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, c := newContextWithQuery(tt.query)

			page, pageSize := ParsePagination(c)

			require.Equal(t, tt.wantPage, page, "page 不符合预期")
			require.Equal(t, tt.wantPageSize, pageSize, "pageSize 不符合预期")
		})
	}
}

func Test_parseInt(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantVal int
		wantErr bool
	}{
		{
			name:    "正常数字",
			input:   "123",
			wantVal: 123,
			wantErr: false,
		},
		{
			name:    "零",
			input:   "0",
			wantVal: 0,
			wantErr: false,
		},
		{
			name:    "单个数字",
			input:   "5",
			wantVal: 5,
			wantErr: false,
		},
		{
			name:    "大数字",
			input:   "99999",
			wantVal: 99999,
			wantErr: false,
		},
		{
			name:    "包含字母_返回0",
			input:   "abc",
			wantVal: 0,
			wantErr: false,
		},
		{
			name:    "数字开头接字母_返回0",
			input:   "12a",
			wantVal: 0,
			wantErr: false,
		},
		{
			name:    "包含负号_返回0",
			input:   "-1",
			wantVal: 0,
			wantErr: false,
		},
		{
			name:    "包含小数点_返回0",
			input:   "1.5",
			wantVal: 0,
			wantErr: false,
		},
		{
			name:    "包含空格_返回0",
			input:   "1 2",
			wantVal: 0,
			wantErr: false,
		},
		{
			name:    "空字符串",
			input:   "",
			wantVal: 0,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, err := parseInt(tt.input)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantVal, val)
		})
	}
}
