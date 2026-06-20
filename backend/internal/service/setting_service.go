package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/imroc/req/v3"
	"golang.org/x/sync/singleflight"
)

// CoerceDingTalkCorpPolicyForWrite 是 coerceDeprecatedDingTalkCorpPolicy 的导出版本，
// 用于 admin handler 在写入路径上对客户端直传的入参做防御性 coerce（前端 UI 虽已无 whitelist 选项，
// 但 API 可被直接调用）。
func CoerceDingTalkCorpPolicyForWrite(policy string) string {
	return coerceDeprecatedDingTalkCorpPolicy(policy)
}

// coerceDeprecatedDingTalkCorpPolicy 把已废弃的 corp_restriction_policy 值替换成安全的等价值。
// 升级前残留在 DB 中的 "whitelist" 会导致 callback 链路在 default case 静默 fail-closed
// （所有钉钉登录被拒）。这里统一退化为 "none" 让服务保持可用，并 warn 日志提醒 admin 重新保存设置。
func coerceDeprecatedDingTalkCorpPolicy(policy string) string {
	if policy == "whitelist" {
		slog.Warn("dingtalk: corp_restriction_policy=whitelist is deprecated and unsupported, coercing to none",
			"hint", "re-save DingTalk settings in admin UI to clear this warning")
		return "none"
	}
	return policy
}

var (
	ErrRegistrationDisabled   = infraerrors.Forbidden("REGISTRATION_DISABLED", "registration is currently disabled")
	ErrSettingNotFound        = infraerrors.NotFound("SETTING_NOT_FOUND", "setting not found")
	ErrDefaultSubGroupInvalid = infraerrors.BadRequest(
		"DEFAULT_SUBSCRIPTION_GROUP_INVALID",
		"default subscription group must exist and be subscription type",
	)
	ErrDefaultSubGroupDuplicate = infraerrors.BadRequest(
		"DEFAULT_SUBSCRIPTION_GROUP_DUPLICATE",
		"default subscription group cannot be duplicated",
	)
)

type SettingRepository interface {
	Get(ctx context.Context, key string) (*Setting, error)
	GetValue(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
	GetMultiple(ctx context.Context, keys []string) (map[string]string, error)
	SetMultiple(ctx context.Context, settings map[string]string) error
	GetAll(ctx context.Context) (map[string]string, error)
	Delete(ctx context.Context, key string) error
}

// cachedVersionBounds 缓存 Claude Code 版本号上下限（进程内缓存，60s TTL）
type cachedVersionBounds struct {
	min       string // 空字符串 = 不检查
	max       string // 空字符串 = 不检查
	expiresAt int64  // unix nano
}

// versionBoundsCache 版本号上下限进程内缓存
var versionBoundsCache atomic.Value // *cachedVersionBounds

// versionBoundsSF 防止缓存过期时 thundering herd
var versionBoundsSF singleflight.Group

// versionBoundsCacheTTL 缓存有效期
const versionBoundsCacheTTL = 60 * time.Second

// versionBoundsErrorTTL DB 错误时的短缓存，快速重试
const versionBoundsErrorTTL = 5 * time.Second

// versionBoundsDBTimeout singleflight 内 DB 查询超时，独立于请求 context
const versionBoundsDBTimeout = 5 * time.Second

// cachedBackendMode Backend Mode cache (in-process, 60s TTL)
type cachedBackendMode struct {
	value     bool
	expiresAt int64 // unix nano
}

var backendModeCache atomic.Value // *cachedBackendMode
var backendModeSF singleflight.Group

const backendModeCacheTTL = 60 * time.Second
const backendModeErrorTTL = 5 * time.Second
const backendModeDBTimeout = 5 * time.Second

// cachedGatewayForwardingSettings 缓存网关转发行为设置（进程内缓存，60s TTL）
type cachedGatewayForwardingSettings struct {
	fingerprintUnification           bool
	metadataPassthrough              bool
	cchSigning                       bool
	claudeOAuthSystemPromptInjection bool
	claudeOAuthSystemPrompt          string
	claudeOAuthSystemPromptBlocks    string
	anthropicCacheTTL1hInjection     bool
	rewriteMessageCacheControl       bool
	expiresAt                        int64 // unix nano
}

var gatewayForwardingCache atomic.Value // *cachedGatewayForwardingSettings
var gatewayForwardingSF singleflight.Group

const gatewayForwardingCacheTTL = 60 * time.Second
const gatewayForwardingErrorTTL = 5 * time.Second
const gatewayForwardingDBTimeout = 5 * time.Second

// cachedAntigravityUserAgentVersion 缓存 Antigravity UA 版本号（进程内缓存，60s TTL）
type cachedAntigravityUserAgentVersion struct {
	version   string
	expiresAt int64 // unix nano
}

const antigravityUserAgentVersionCacheTTL = 60 * time.Second
const antigravityUserAgentVersionErrorTTL = 5 * time.Second
const antigravityUserAgentVersionDBTimeout = 5 * time.Second

// DefaultOpenAICodexUserAgent OpenAI Codex 默认 User-Agent（用于规避 Cloudflare 对浏览器 UA 的质询）
const DefaultOpenAICodexUserAgent = "codex-tui/0.125.0 (Ubuntu 22.4.0; x86_64) xterm-256color (codex-tui; 0.125.0)"

// cachedOpenAICodexUserAgent 缓存 OpenAI Codex UA（进程内缓存，60s TTL）
type cachedOpenAICodexUserAgent struct {
	value     string
	expiresAt int64 // unix nano
}

type cachedOpenAIQuotaAutoPauseSettings struct {
	settings  OpsOpenAIAccountQuotaAutoPauseSettings
	expiresAt int64
}

const openAICodexUserAgentCacheTTL = 60 * time.Second
const openAICodexUserAgentErrorTTL = 5 * time.Second
const openAICodexUserAgentDBTimeout = 5 * time.Second

const codexRestrictionPolicyCacheTTL = 60 * time.Second
const codexRestrictionPolicyDBTimeout = 5 * time.Second

// cachedCodexRestrictionPolicy codex_cli_only 全局加固策略缓存（进程内，60s TTL）。
// GetCodexRestrictionPolicy 在每个 codex_cli_only 账号的网关请求热路径上被调用，避免每次访问 DB。
type cachedCodexRestrictionPolicy struct {
	value     CodexRestrictionPolicy
	expiresAt int64 // unix nano
}

// cachedCyberSessionBlockRuntime cyber 会话屏蔽开关+TTL 进程内缓存（60s TTL）。
// GetCyberSessionBlockRuntime 在网关请求热路径上被调用，避免每次访问 DB。
type cachedCyberSessionBlockRuntime struct {
	enabled   bool
	ttl       time.Duration
	expiresAt int64 // unix nano
}

const cyberSessionBlockRuntimeCacheTTL = 60 * time.Second
const cyberSessionBlockRuntimeErrorTTL = 5 * time.Second
const cyberSessionBlockRuntimeDBTimeout = 5 * time.Second

const openAIQuotaAutoPauseSettingsCacheTTL = 60 * time.Second
const openAIQuotaAutoPauseSettingsErrorTTL = 5 * time.Second
const openAIQuotaAutoPauseSettingsDBTimeout = 5 * time.Second

const openAIQuotaAutoPauseSettingsRefreshKey = "openai_quota_auto_pause_settings"

// DefaultSubscriptionGroupReader validates group references used by default subscriptions.
type DefaultSubscriptionGroupReader interface {
	GetByID(ctx context.Context, id int64) (*Group, error)
}

// WebSearchManagerBuilder creates a websearch.Manager from config (injected by infra layer).
// proxyURLs maps proxy ID to resolved URL for provider-level proxy support.
type WebSearchManagerBuilder func(cfg *WebSearchEmulationConfig, proxyURLs map[int64]string)

// SettingService 系统设置服务
type SettingService struct {
	settingRepo                 SettingRepository
	defaultSubGroupReader       DefaultSubscriptionGroupReader
	proxyRepo                   ProxyRepository // for resolving websearch provider proxy URLs
	cfg                         *config.Config
	onUpdate                    func() // Callback when settings are updated (for cache invalidation)
	version                     string // Application version
	webSearchManagerBuilder     WebSearchManagerBuilder
	antigravityUAVersionCache   atomic.Value // *cachedAntigravityUserAgentVersion
	antigravityUAVersionSF      singleflight.Group
	openAICodexUACache          atomic.Value // *cachedOpenAICodexUserAgent
	openAICodexUASF             singleflight.Group
	codexRestrictionPolicyCache atomic.Value // *cachedCodexRestrictionPolicy
	codexRestrictionPolicySF    singleflight.Group

	cyberSessionBlockRuntimeCache atomic.Value // *cachedCyberSessionBlockRuntime
	cyberSessionBlockRuntimeSF    singleflight.Group

	// openAIQuotaAutoPauseSettingsCache holds the most recently observed quota auto-pause
	// settings. GetOpenAIQuotaAutoPauseSettings reads this atomic.Value on the request hot
	// path without ever blocking on the DB; when the cached entry expires, a background
	// goroutine refreshes it via openAIQuotaAutoPauseSettingsSF (stale-while-revalidate).
	// This per-service field also gives tests natural isolation — each SettingService
	// instance owns its own cache, no shared package-level state.
	openAIQuotaAutoPauseSettingsCache atomic.Value // *cachedOpenAIQuotaAutoPauseSettings
	openAIQuotaAutoPauseSettingsSF    singleflight.Group
}

// DefaultPlatformQuotaSetting 单 platform 三档限额（nil = 沿用上层；0 = 显式禁用；>0 = 上限）
type DefaultPlatformQuotaSetting struct {
	DailyLimitUSD   *float64 `json:"daily"`
	WeeklyLimitUSD  *float64 `json:"weekly"`
	MonthlyLimitUSD *float64 `json:"monthly"`
}

type ProviderDefaultGrantSettings struct {
	Balance          float64
	Concurrency      int
	Subscriptions    []DefaultSubscriptionSetting
	GrantOnSignup    bool
	GrantOnFirstBind bool
	PlatformQuotas   map[string]*DefaultPlatformQuotaSetting // key = platform name
}

type AuthSourceDefaultSettings struct {
	Email                        ProviderDefaultGrantSettings
	LinuxDo                      ProviderDefaultGrantSettings
	OIDC                         ProviderDefaultGrantSettings
	WeChat                       ProviderDefaultGrantSettings
	GitHub                       ProviderDefaultGrantSettings
	Google                       ProviderDefaultGrantSettings
	DingTalk                     ProviderDefaultGrantSettings
	ForceEmailOnThirdPartySignup bool
}

type authSourceDefaultKeySet struct {
	// source 是 auth source 标识（如 "email"、"github"），仅用于 parse 时
	// slog.Warn 诊断输出，不再参与 key 拼接（platformQuotas 字段已存完整 key）。
	source           string
	balance          string
	concurrency      string
	subscriptions    string
	grantOnSignup    string
	grantOnFirstBind string
	platformQuotas   string // SettingKeyAuthSourcePlatformQuotas(source)
}

var (
	emailAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "email",
		balance:          SettingKeyAuthSourceDefaultEmailBalance,
		concurrency:      SettingKeyAuthSourceDefaultEmailConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultEmailSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultEmailGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultEmailGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("email"),
	}
	linuxDoAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "linuxdo",
		balance:          SettingKeyAuthSourceDefaultLinuxDoBalance,
		concurrency:      SettingKeyAuthSourceDefaultLinuxDoConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultLinuxDoSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultLinuxDoGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("linuxdo"),
	}
	oidcAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "oidc",
		balance:          SettingKeyAuthSourceDefaultOIDCBalance,
		concurrency:      SettingKeyAuthSourceDefaultOIDCConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultOIDCSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultOIDCGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultOIDCGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("oidc"),
	}
	weChatAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "wechat",
		balance:          SettingKeyAuthSourceDefaultWeChatBalance,
		concurrency:      SettingKeyAuthSourceDefaultWeChatConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultWeChatSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultWeChatGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultWeChatGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("wechat"),
	}
	gitHubAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "github",
		balance:          SettingKeyAuthSourceDefaultGitHubBalance,
		concurrency:      SettingKeyAuthSourceDefaultGitHubConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultGitHubSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultGitHubGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultGitHubGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("github"),
	}
	googleAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "google",
		balance:          SettingKeyAuthSourceDefaultGoogleBalance,
		concurrency:      SettingKeyAuthSourceDefaultGoogleConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultGoogleSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultGoogleGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultGoogleGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("google"),
	}
	dingTalkAuthSourceDefaultKeys = authSourceDefaultKeySet{
		source:           "dingtalk",
		balance:          SettingKeyAuthSourceDefaultDingTalkBalance,
		concurrency:      SettingKeyAuthSourceDefaultDingTalkConcurrency,
		subscriptions:    SettingKeyAuthSourceDefaultDingTalkSubscriptions,
		grantOnSignup:    SettingKeyAuthSourceDefaultDingTalkGrantOnSignup,
		grantOnFirstBind: SettingKeyAuthSourceDefaultDingTalkGrantOnFirstBind,
		platformQuotas:   SettingKeyAuthSourcePlatformQuotas("dingtalk"),
	}
)

const (
	defaultAuthSourceBalance     = 0
	defaultAuthSourceConcurrency = 5
	defaultWeChatConnectMode     = "open"
	defaultWeChatConnectScopes   = "snsapi_login"
	defaultWeChatConnectFrontend = "/auth/wechat/callback"
	defaultGitHubOAuthAuthorize  = "https://github.com/login/oauth/authorize"
	defaultGitHubOAuthToken      = "https://github.com/login/oauth/access_token"
	defaultGitHubOAuthUserInfo   = "https://api.github.com/user"
	defaultGitHubOAuthEmails     = "https://api.github.com/user/emails"
	defaultGitHubOAuthScopes     = "read:user user:email"
	defaultGitHubOAuthFrontend   = "/auth/oauth/callback"
	defaultGoogleOAuthAuthorize  = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleOAuthToken      = "https://oauth2.googleapis.com/token"
	defaultGoogleOAuthUserInfo   = "https://openidconnect.googleapis.com/v1/userinfo"
	defaultGoogleOAuthScopes     = "openid email profile"
	defaultGoogleOAuthFrontend   = "/auth/oauth/callback"
	defaultLoginAgreementMode    = "modal"
	defaultLoginAgreementDate    = "2026-06-20"
)

func normalizeLoginAgreementMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "checkbox":
		return "checkbox"
	default:
		return defaultLoginAgreementMode
	}
}

func defaultLoginAgreementDocuments() []LoginAgreementDocument {
	return []LoginAgreementDocument{
		{
			ID:    "terms",
			Title: `服务条款`,
			ContentMD: `# V-Claw 服务条款

最后更新：2026-06-20

## 一、目的

本条款适用于 V-Claw 的使用。V-Claw 是共享 AI API 访问及内部用量额度服务。用户创建账号、购买或领取用量额度、生成 API Key 或使用本服务，即表示同意本条款。

V-Claw 通过共享基础设施为用户提供受支持 AI 模型的访问能力。除非另有明确说明，V-Claw 不是任何第三方 AI 模型提供商的官方产品，也不与其存在正式关联关系。

## 二、服务性质

V-Claw 通过平台向用户提供受支持 AI 模型接口的访问能力。用户可购买或获得内部用量额度，并在请求经由本服务处理时消耗相应用量。

用量额度仅为本服务内部额度，不是现金、加密货币、证券、区块链 Token、储值工具或可转让数字资产。用量额度在 V-Claw 平台之外不具有价值，只能用于平台内受支持的服务。

## 三、技术中介与用户控制的活动

V-Claw 仅提供技术路由、账号管理、额度计量、速率控制、审计和安全能力。除平台明确提供的管理功能外，V-Claw 不决定用户的具体提示词、输入数据、输出使用方式、最终业务目的或下游传播方式。

用户对自己提交的 prompt、文件、数据、选择的模型、接收的输出、保存/删除/转发行为，以及任何下游使用负全部责任。用户应自行判断其活动是否合法、是否获得授权、是否需要通知或获得第三方同意。

## 四、账号与 API Key 安全

用户应自行维护账号、API Key、设备及凭据的安全。通过用户账号或 API Key 发生的活动，可被视为该用户本人的活动。

未经 V-Claw 明确允许，用户不得共享、转售、再授权、滥用、逆向工程、过载使用或试图绕过本服务。若发现异常使用、凭据泄露、滥用或违反政策，V-Claw 可暂停、轮换、限制或终止相关访问。

## 五、用量额度、费率与计费

用量额度将按照使用时平台展示或配置的价格、模型费率、倍率、套餐规则和使用政策进行消耗。

实际消耗可能因模型类型、输入 token、输出 token、缓存 token、工具调用、路由、上游提供商价格、倍率、套餐配置或其他技术因素而变化。用户应自行在使用前后查看用量记录和余额。

由于上游提供商变化、基础设施成本变化、风控要求或运营需要，V-Claw 可不时更新支持的模型、路由、价格、套餐规则或倍率。对于重要变化，在可行情况下将通过平台或官方支持渠道进行展示或通知。

## 六、上游服务依赖

本服务依赖第三方 AI 模型提供商、网络连接、账号可用性、额度限制、速率限制、提供商政策及上游可用性。

V-Claw 不保证任何特定模型、提供商、路由、速度、额度或功能始终可用。由于上游或运营因素，模型可能发生不可用、延迟、替换、限速、降级或停止支持，且可能无法提前通知。

## 七、用户法律基础与数据责任

如果用户向 V-Claw 提交、传输或指示处理个人数据、敏感数据、商业机密、受监管材料或任何受法律保护的数据，用户应自行确保其拥有适用的法律基础、授权、通知和必要同意，并已完成适当的风险评估与内部审批。

除非法律或书面协议另有明确要求，V-Claw 不负责判断用户是否拥有处理特定数据的法律基础。用户应避免提交不必要的敏感或高风险数据；若确有必要，用户应先采取匿名化、最小化、脱敏、加密、访问控制和保留限制等措施。

## 八、无预审或持续监控义务

V-Claw 不是对所有用户内容、提示词、文件、输出、请求或下游使用进行事前审查或持续监控的义务承担者。V-Claw 可基于风险管理、合规要求或安全需要进行抽样、自动化检测、人工复核或事件响应，但这不表示 V-Claw 承担识别、阻止或保证用户行为合法性的义务。

## 九、受监管/高风险用途

用户不得将本服务用于需要许可、认证、专门监管、强制人工审查或其他高标准合规控制的高风险场景，除非用户已自行确认满足全部适用法律、行业规范和内部控制要求。

高风险用途包括但不限于：医疗诊断或治疗决策、法律意见替代、金融授信或风控自动决策、关键基础设施控制、儿童/未成年人高敏感数据处理、执法或监控自动化、以及任何可能对人身、财产、就业、教育、信用或权利产生重大影响的用途。

## 十、可接受使用

用户不得将本服务用于违法、滥用、有害、欺诈、侵权或未经授权的活动，包括但不限于：

- 违反适用法律法规；
- 生成、传播或协助生成有害、违法、侵权或滥用内容；
- 攻击、抓取、过载、探测或绕过本服务；
- 未经许可转售或再分发访问能力；
- 试图未经授权访问系统、账号、模型、路由或数据；
- 使用泄露、被盗、未授权或违反规则共享的凭据；
- 违反相关上游 AI 提供商的政策。

若 V-Claw 有合理依据认为用户违反本条款、滥用本服务，或给平台、上游提供商或其他用户造成风险，可暂停、限制、轮换密钥或终止访问。

## 十一、法律请求与合规协作

如果 V-Claw 收到有效的法律请求、法院命令、监管要求、执法协助请求或其他强制性程序，V-Claw 可在适用法律允许的范围内采取必要行动，包括保存、披露、限制访问、冻结功能或配合调查。

用户同意在合理范围内配合 V-Claw 的合规审查、争议处理、滥用调查、计费核验、身份核验和风险处置，并应在被要求时提供必要信息、文件或说明。

## 十二、退款与余额调整

由于用量额度会在请求被处理时即时消耗，已使用额度通常不予退款。

未使用额度的退款或余额调整可按个案审查，并受支付方式限制、套餐规则、促销或赠送额度规则、已发生上游成本、滥用检查及运营可行性影响。

V-Claw 无义务退还已消耗额度、与滥用行为相关的额度、促销额度、赠送额度，或因用户侧问题导致受影响的额度，例如 API Key 泄露、集成错误、非预期请求或凭据管理不当。

## 十三、AI 输出不作保证

AI 生成内容可能不准确、不完整、延迟、令人不适或不适用于特定目的。用户应自行审查、核验并决定如何使用 AI 输出。

V-Claw 不保证任何 AI 生成内容或第三方模型响应的准确性、合法性、可靠性、可用性或商业适用性。

## 十四、服务可用性与维护

V-Claw 可在必要时进行维护、升级、路由调整、安全处置或紧急暂停。本服务可能因技术问题、上游提供商问题、滥用防控、网络问题、安全事件或其他运营原因而暂时不可用。

V-Claw 将尽合理努力维护服务，但不保证服务持续不中断或完全无错误。

## 十五、责任限制

在适用法律允许的最大范围内，V-Claw 不对因使用或无法使用本服务而产生的间接、附带、后果性、特殊、惩罚性、业务损失、数据损失、收入损失或利润损失承担责任。

用户应自行对其使用本服务的行为负责，包括 API 请求、生成内容、账号安全、法律合规、下游集成及使用后果。

## 十六、赔偿与免责

在适用法律允许的最大范围内，用户同意就以下事项为 V-Claw、其运营者、关联方、员工、承包商和供应商提供赔偿、辩护并使其免受损害：

- 用户违反本条款、适用法律、第三方权利或上游提供商规则；
- 用户提交或使用的内容、数据、请求或下游集成引发的索赔、调查、损失或费用；
- 用户账号、API Key、设备、自动化脚本或第三方工具被滥用或泄露所造成的后果；
- 用户未能取得必要法律基础、同意、通知或授权而产生的任何主张。

## 十七、条款变更

V-Claw 可根据服务变化、价格变化、上游提供商变化、法律要求、风控要求或运营需要，不时更新本条款。

更新条款发布后继续使用本服务，即视为接受更新后的条款。

## 十八、联系方式

如对本条款、用量额度、计费或账号访问有疑问，请通过平台提供的官方支持渠道联系 V-Claw 支持。`,
			TitleI18n: map[string]string{
				"zh": `服务条款`,
				"en": `Terms of Service`,
				"vi": `Điều khoản dịch vụ`,
				"ko": `서비스 약관`,
			},
			ContentMDI18n: map[string]string{
				"zh": `# V-Claw 服务条款

最后更新：2026-06-20

## 一、目的

本条款适用于 V-Claw 的使用。V-Claw 是共享 AI API 访问及内部用量额度服务。用户创建账号、购买或领取用量额度、生成 API Key 或使用本服务，即表示同意本条款。

V-Claw 通过共享基础设施为用户提供受支持 AI 模型的访问能力。除非另有明确说明，V-Claw 不是任何第三方 AI 模型提供商的官方产品，也不与其存在正式关联关系。

## 二、服务性质

V-Claw 通过平台向用户提供受支持 AI 模型接口的访问能力。用户可购买或获得内部用量额度，并在请求经由本服务处理时消耗相应用量。

用量额度仅为本服务内部额度，不是现金、加密货币、证券、区块链 Token、储值工具或可转让数字资产。用量额度在 V-Claw 平台之外不具有价值，只能用于平台内受支持的服务。

## 三、技术中介与用户控制的活动

V-Claw 仅提供技术路由、账号管理、额度计量、速率控制、审计和安全能力。除平台明确提供的管理功能外，V-Claw 不决定用户的具体提示词、输入数据、输出使用方式、最终业务目的或下游传播方式。

用户对自己提交的 prompt、文件、数据、选择的模型、接收的输出、保存/删除/转发行为，以及任何下游使用负全部责任。用户应自行判断其活动是否合法、是否获得授权、是否需要通知或获得第三方同意。

## 四、账号与 API Key 安全

用户应自行维护账号、API Key、设备及凭据的安全。通过用户账号或 API Key 发生的活动，可被视为该用户本人的活动。

未经 V-Claw 明确允许，用户不得共享、转售、再授权、滥用、逆向工程、过载使用或试图绕过本服务。若发现异常使用、凭据泄露、滥用或违反政策，V-Claw 可暂停、轮换、限制或终止相关访问。

## 五、用量额度、费率与计费

用量额度将按照使用时平台展示或配置的价格、模型费率、倍率、套餐规则和使用政策进行消耗。

实际消耗可能因模型类型、输入 token、输出 token、缓存 token、工具调用、路由、上游提供商价格、倍率、套餐配置或其他技术因素而变化。用户应自行在使用前后查看用量记录和余额。

由于上游提供商变化、基础设施成本变化、风控要求或运营需要，V-Claw 可不时更新支持的模型、路由、价格、套餐规则或倍率。对于重要变化，在可行情况下将通过平台或官方支持渠道进行展示或通知。

## 六、上游服务依赖

本服务依赖第三方 AI 模型提供商、网络连接、账号可用性、额度限制、速率限制、提供商政策及上游可用性。

V-Claw 不保证任何特定模型、提供商、路由、速度、额度或功能始终可用。由于上游或运营因素，模型可能发生不可用、延迟、替换、限速、降级或停止支持，且可能无法提前通知。

## 七、用户法律基础与数据责任

如果用户向 V-Claw 提交、传输或指示处理个人数据、敏感数据、商业机密、受监管材料或任何受法律保护的数据，用户应自行确保其拥有适用的法律基础、授权、通知和必要同意，并已完成适当的风险评估与内部审批。

除非法律或书面协议另有明确要求，V-Claw 不负责判断用户是否拥有处理特定数据的法律基础。用户应避免提交不必要的敏感或高风险数据；若确有必要，用户应先采取匿名化、最小化、脱敏、加密、访问控制和保留限制等措施。

## 八、无预审或持续监控义务

V-Claw 不是对所有用户内容、提示词、文件、输出、请求或下游使用进行事前审查或持续监控的义务承担者。V-Claw 可基于风险管理、合规要求或安全需要进行抽样、自动化检测、人工复核或事件响应，但这不表示 V-Claw 承担识别、阻止或保证用户行为合法性的义务。

## 九、受监管/高风险用途

用户不得将本服务用于需要许可、认证、专门监管、强制人工审查或其他高标准合规控制的高风险场景，除非用户已自行确认满足全部适用法律、行业规范和内部控制要求。

高风险用途包括但不限于：医疗诊断或治疗决策、法律意见替代、金融授信或风控自动决策、关键基础设施控制、儿童/未成年人高敏感数据处理、执法或监控自动化、以及任何可能对人身、财产、就业、教育、信用或权利产生重大影响的用途。

## 十、可接受使用

用户不得将本服务用于违法、滥用、有害、欺诈、侵权或未经授权的活动，包括但不限于：

- 违反适用法律法规；
- 生成、传播或协助生成有害、违法、侵权或滥用内容；
- 攻击、抓取、过载、探测或绕过本服务；
- 未经许可转售或再分发访问能力；
- 试图未经授权访问系统、账号、模型、路由或数据；
- 使用泄露、被盗、未授权或违反规则共享的凭据；
- 违反相关上游 AI 提供商的政策。

若 V-Claw 有合理依据认为用户违反本条款、滥用本服务，或给平台、上游提供商或其他用户造成风险，可暂停、限制、轮换密钥或终止访问。

## 十一、法律请求与合规协作

如果 V-Claw 收到有效的法律请求、法院命令、监管要求、执法协助请求或其他强制性程序，V-Claw 可在适用法律允许的范围内采取必要行动，包括保存、披露、限制访问、冻结功能或配合调查。

用户同意在合理范围内配合 V-Claw 的合规审查、争议处理、滥用调查、计费核验、身份核验和风险处置，并应在被要求时提供必要信息、文件或说明。

## 十二、退款与余额调整

由于用量额度会在请求被处理时即时消耗，已使用额度通常不予退款。

未使用额度的退款或余额调整可按个案审查，并受支付方式限制、套餐规则、促销或赠送额度规则、已发生上游成本、滥用检查及运营可行性影响。

V-Claw 无义务退还已消耗额度、与滥用行为相关的额度、促销额度、赠送额度，或因用户侧问题导致受影响的额度，例如 API Key 泄露、集成错误、非预期请求或凭据管理不当。

## 十三、AI 输出不作保证

AI 生成内容可能不准确、不完整、延迟、令人不适或不适用于特定目的。用户应自行审查、核验并决定如何使用 AI 输出。

V-Claw 不保证任何 AI 生成内容或第三方模型响应的准确性、合法性、可靠性、可用性或商业适用性。

## 十四、服务可用性与维护

V-Claw 可在必要时进行维护、升级、路由调整、安全处置或紧急暂停。本服务可能因技术问题、上游提供商问题、滥用防控、网络问题、安全事件或其他运营原因而暂时不可用。

V-Claw 将尽合理努力维护服务，但不保证服务持续不中断或完全无错误。

## 十五、责任限制

在适用法律允许的最大范围内，V-Claw 不对因使用或无法使用本服务而产生的间接、附带、后果性、特殊、惩罚性、业务损失、数据损失、收入损失或利润损失承担责任。

用户应自行对其使用本服务的行为负责，包括 API 请求、生成内容、账号安全、法律合规、下游集成及使用后果。

## 十六、赔偿与免责

在适用法律允许的最大范围内，用户同意就以下事项为 V-Claw、其运营者、关联方、员工、承包商和供应商提供赔偿、辩护并使其免受损害：

- 用户违反本条款、适用法律、第三方权利或上游提供商规则；
- 用户提交或使用的内容、数据、请求或下游集成引发的索赔、调查、损失或费用；
- 用户账号、API Key、设备、自动化脚本或第三方工具被滥用或泄露所造成的后果；
- 用户未能取得必要法律基础、同意、通知或授权而产生的任何主张。

## 十七、条款变更

V-Claw 可根据服务变化、价格变化、上游提供商变化、法律要求、风控要求或运营需要，不时更新本条款。

更新条款发布后继续使用本服务，即视为接受更新后的条款。

## 十八、联系方式

如对本条款、用量额度、计费或账号访问有疑问，请通过平台提供的官方支持渠道联系 V-Claw 支持。`,
				"en": `# V-Claw Terms of Service

Last updated: 2026-06-20

## 1. Purpose

These Terms govern the use of V-Claw, a shared AI API access and internal usage-credit service. By creating an account, purchasing or receiving usage credits, generating an API key, or using the Service, you agree to these Terms.

V-Claw is a service platform for accessing supported AI models through shared infrastructure. Unless expressly stated otherwise, V-Claw is not an official product of, and is not formally affiliated with, any third-party AI model provider.

## 2. Nature of the Service

V-Claw provides access to supported AI model endpoints through the platform. Users may purchase or receive internal usage credits that are consumed when requests are processed through the Service.

Usage credits are internal service credits only. They are not cash, cryptocurrency, securities, blockchain tokens, stored-value instruments, or transferable digital assets. Credits have no value outside the V-Claw platform and may only be used for supported services within the platform.

## 3. Technical Intermediary; User-Controlled Activity

V-Claw acts as a technical intermediary that provides routing, account management, quota metering, rate controls, audit, and security functions. Except for the management features expressly exposed by the platform, V-Claw does not decide the user's prompts, input data, output use, business purpose, or downstream distribution.

Users are solely responsible for the prompts they submit, files or data they process, models they select, outputs they receive, how they store, delete, or forward those outputs, and any downstream use. Users must determine whether their activity is lawful, authorized, and requires notice, consent, or other third-party approvals.

## 4. Account and API Key Security

Users are responsible for maintaining the security of their accounts, API keys, devices, and credentials. Any activity performed through a user's account or API key may be treated as activity by that user.

Users must not share, resell, sublicense, abuse, reverse engineer, overload, or attempt to bypass the Service unless explicitly permitted by V-Claw. V-Claw may suspend, rotate, restrict, or terminate access if abnormal usage, credential leakage, abuse, or policy violations are detected.

## 5. Usage Credits, Rates, and Billing

Credits are consumed according to the pricing, model rates, multipliers, package rules, and usage policies displayed or configured in the platform at the time of use.

Actual consumption may vary depending on model type, input tokens, output tokens, cached tokens, tool calls, routing, upstream provider pricing, rate multipliers, package configuration, or other technical factors. Users are responsible for reviewing their usage records and balance before and after use.

V-Claw may update supported models, routes, pricing, package rules, or rate multipliers from time to time due to upstream provider changes, infrastructure cost changes, risk-control requirements, or operational needs. Where practical, material changes will be reflected in the platform or announced through the official support channel.

## 6. Upstream Provider Dependency

The Service depends on third-party AI model providers, network connectivity, account availability, quota limits, rate limits, provider policies, and upstream availability.

V-Claw does not guarantee that any specific model, provider, route, speed, quota, or feature will remain available at all times. Models may be unavailable, delayed, replaced, rate-limited, degraded, or discontinued without prior notice due to upstream or operational factors.

## 7. User Legal Basis and Data Responsibility

If you submit, transmit, or instruct V-Claw to process personal data, sensitive data, trade secrets, regulated materials, or any other protected data, you are solely responsible for ensuring you have the applicable legal basis, authorization, notices, and any required consent, and for completing any risk assessment or internal approvals that may be required.

Unless required by law or a written agreement, V-Claw does not evaluate whether you have a lawful basis to process a particular dataset. You should avoid submitting unnecessary sensitive or high-risk data. If such data is necessary, you must first apply minimization, anonymization, pseudonymization, encryption, access controls, and retention limits where appropriate.

## 8. No Duty to Pre-Screen or Monitor

V-Claw is not under a duty to pre-screen or continuously monitor all user content, prompts, files, outputs, requests, or downstream use. V-Claw may conduct sampling, automated detection, manual review, or incident response for risk-management, compliance, or security purposes, but this does not create a duty to identify, stop, or guarantee the legality of user behavior.

## 9. Regulated / High-Risk Use

You must not use the Service for high-risk contexts that require licenses, certification, specialized regulatory oversight, mandatory human review, or other heightened compliance controls unless you have independently confirmed that all applicable laws, industry rules, and internal controls are satisfied.

High-risk uses include, without limitation: medical diagnosis or treatment decisions, legal advice substitution, financial credit or risk decisions, critical infrastructure control, highly sensitive data processing for children or minors, law-enforcement or surveillance automation, and any use that may materially affect a person's safety, property, employment, education, credit, or rights.

## 10. Acceptable Use

Users must not use the Service for illegal, abusive, harmful, fraudulent, infringing, or unauthorized activities. This includes, without limitation:

- violating applicable laws or regulations;
- generating, distributing, or facilitating harmful, illegal, infringing, or abusive content;
- attacking, scraping, overloading, probing, or bypassing the Service;
- reselling or redistributing access without permission;
- attempting to obtain unauthorized access to systems, accounts, models, routes, or data;
- using leaked, stolen, unauthorized, or shared credentials in violation of applicable rules;
- violating the policies of applicable upstream AI providers.

V-Claw may suspend, restrict, rotate keys, or terminate access if it reasonably believes that a user has violated these Terms, abused the Service, or created risk for the platform, upstream providers, or other users.

## 11. Lawful Requests and Compliance Cooperation

If V-Claw receives a valid legal request, court order, regulatory demand, law-enforcement request, or other compulsory process, V-Claw may take the actions required or permitted by law, including preservation, disclosure, access restriction, feature freezing, or cooperation with an investigation.

You agree to reasonably cooperate with V-Claw's compliance review, dispute handling, abuse investigation, billing verification, identity verification, and risk mitigation, and to provide necessary information, documents, or explanations when requested.

## 12. Refunds and Adjustments

Because usage credits are consumed when requests are processed, used credits are generally non-refundable.

Refunds or balance adjustments for unused credits may be reviewed on a case-by-case basis, subject to payment method limitations, package rules, promotional or bonus-credit rules, upstream costs already incurred, abuse checks, and operational feasibility.

V-Claw is not required to refund credits that have already been consumed, credits linked to abusive activity, promotional credits, bonus credits, or credits affected by user-side issues such as leaked API keys, incorrect integration, unintended requests, or insecure credential handling.

## 13. No Warranty on AI Output

AI-generated outputs may be inaccurate, incomplete, delayed, offensive, or unsuitable for a particular purpose. Users are solely responsible for reviewing, verifying, and deciding how to use AI outputs.

V-Claw does not guarantee the accuracy, legality, reliability, availability, or commercial suitability of any AI-generated content or third-party model response.

## 14. Service Availability and Maintenance

V-Claw may perform maintenance, upgrades, routing changes, security actions, or emergency suspensions when necessary. The Service may be temporarily unavailable due to technical issues, upstream provider issues, abuse prevention, network problems, security incidents, or other operational reasons.

V-Claw will make reasonable efforts to maintain the Service, but does not guarantee uninterrupted or error-free operation.

## 15. Limitation of Liability

To the maximum extent permitted by applicable law, V-Claw is not liable for indirect, incidental, consequential, special, punitive, business-loss, data-loss, revenue-loss, or profit-loss damages arising from use of or inability to use the Service.

Users are responsible for their own use of the Service, including API requests, generated content, account security, legal compliance, downstream integrations, and downstream consequences of their usage.

## 16. Indemnification; Hold Harmless

To the maximum extent permitted by law, you agree to indemnify, defend, and hold harmless V-Claw, its operators, affiliates, employees, contractors, and suppliers from and against any claims, investigations, losses, liabilities, damages, costs, or expenses arising out of or related to:

- your breach of these Terms, applicable law, third-party rights, or upstream provider rules;
- the content, data, requests, or downstream integrations you submit or use;
- misuse or leakage of your account, API key, devices, automation scripts, or third-party tools;
- failure to obtain required legal basis, consent, notices, or authorizations.

## 17. Changes to These Terms

V-Claw may update these Terms from time to time to reflect service changes, pricing changes, upstream provider changes, legal requirements, risk-control requirements, or operational needs.

Continued use of the Service after the updated Terms become available constitutes acceptance of the updated Terms.

## 18. Contact

If you have questions about these Terms, usage credits, billing, or account access, please contact V-Claw support through the official support channel provided by the platform.`,
				"vi": `# Điều khoản Dịch vụ V-Claw

Cập nhật lần cuối: 2026-06-20

## 1. Mục đích

Điều khoản này quy định việc sử dụng V-Claw, một dịch vụ chia sẻ quyền truy cập AI API và credit sử dụng nội bộ. Khi tạo tài khoản, mua hoặc nhận credit, tạo API key, hoặc sử dụng Dịch vụ, người dùng đồng ý với các Điều khoản này.

V-Claw là nền tảng dịch vụ hỗ trợ truy cập các mô hình AI được hỗ trợ thông qua hạ tầng dùng chung. Trừ khi được công bố rõ ràng, V-Claw không phải sản phẩm chính thức và không có quan hệ liên kết chính thức với bất kỳ nhà cung cấp mô hình AI bên thứ ba nào.

## 2. Bản chất Dịch vụ

V-Claw cung cấp quyền truy cập các endpoint mô hình AI được hỗ trợ thông qua nền tảng. Người dùng có thể mua hoặc được cấp credit sử dụng nội bộ; credit này được trừ khi yêu cầu được xử lý qua Dịch vụ.

Credit sử dụng chỉ là credit nội bộ của dịch vụ. Credit không phải tiền mặt, tiền mã hóa, chứng khoán, token blockchain, công cụ lưu trữ giá trị, hoặc tài sản số có thể chuyển nhượng. Credit không có giá trị bên ngoài nền tảng V-Claw và chỉ được dùng cho các dịch vụ được hỗ trợ trong nền tảng.

## 3. Trung gian kỹ thuật và hoạt động do người dùng kiểm soát

V-Claw chỉ cung cấp hạ tầng kỹ thuật, định tuyến, quản lý tài khoản, đo lường quota, kiểm soát tốc độ, ghi nhận log và chức năng an ninh. Ngoại trừ các chức năng quản trị mà nền tảng công bố rõ ràng, V-Claw không quyết định prompt, dữ liệu đầu vào, cách dùng đầu ra, mục đích kinh doanh cuối cùng hay cách phân phối xuống phía sau của người dùng.

Người dùng tự chịu trách nhiệm hoàn toàn đối với prompt gửi lên, tệp hoặc dữ liệu xử lý, mô hình được chọn, đầu ra nhận được, cách lưu/xóa/chia sẻ đầu ra và mọi cách sử dụng phía sau. Người dùng phải tự xác định hoạt động của mình có hợp pháp, có được phép hay không, và có cần thông báo, đồng ý hoặc chấp thuận của bên thứ ba hay không.

## 4. Bảo mật tài khoản và API key

Người dùng chịu trách nhiệm bảo mật tài khoản, API key, thiết bị và thông tin đăng nhập của mình. Mọi hoạt động phát sinh từ tài khoản hoặc API key của người dùng có thể được xem là hoạt động của chính người dùng đó.

Người dùng không được chia sẻ, bán lại, cấp quyền lại, lạm dụng, đảo ngược kỹ thuật, gây quá tải hoặc cố gắng vượt qua giới hạn của Dịch vụ nếu chưa được V-Claw cho phép rõ ràng. V-Claw có thể tạm dừng, xoay/đổi key, hạn chế hoặc chấm dứt quyền truy cập nếu phát hiện sử dụng bất thường, rò rỉ thông tin, lạm dụng hoặc vi phạm chính sách.

## 5. Credit sử dụng, mức giá và tính phí

Credit được tiêu thụ theo mức giá, rate mô hình, hệ số nhân, quy tắc gói và chính sách sử dụng được hiển thị hoặc cấu hình trong nền tảng tại thời điểm sử dụng.

Mức tiêu thụ thực tế có thể thay đổi tùy theo loại mô hình, token đầu vào, token đầu ra, token cache, tool call, tuyến xử lý, giá của nhà cung cấp thượng nguồn, hệ số nhân, cấu hình gói hoặc các yếu tố kỹ thuật khác. Người dùng có trách nhiệm kiểm tra lịch sử sử dụng và số dư trước và sau khi sử dụng.

V-Claw có thể cập nhật mô hình hỗ trợ, tuyến xử lý, giá, quy tắc gói hoặc hệ số nhân theo thời gian do thay đổi từ nhà cung cấp thượng nguồn, chi phí hạ tầng, yêu cầu quản trị rủi ro hoặc nhu cầu vận hành. Khi phù hợp, các thay đổi quan trọng sẽ được thể hiện trong nền tảng hoặc thông báo qua kênh hỗ trợ chính thức.

## 6. Phụ thuộc nhà cung cấp thượng nguồn

Dịch vụ phụ thuộc vào nhà cung cấp mô hình AI bên thứ ba, kết nối mạng, tình trạng tài khoản, quota, rate limit, chính sách nhà cung cấp và độ sẵn sàng của thượng nguồn.

V-Claw không bảo đảm rằng một mô hình, nhà cung cấp, tuyến xử lý, tốc độ, quota hoặc tính năng cụ thể sẽ luôn khả dụng. Mô hình có thể không khả dụng, chậm, bị thay thế, bị giới hạn tốc độ, suy giảm chất lượng hoặc ngừng hỗ trợ mà không cần thông báo trước do yếu tố thượng nguồn hoặc vận hành.

## 7. Cơ sở pháp lý và trách nhiệm dữ liệu của người dùng

Nếu người dùng gửi, truyền hoặc yêu cầu V-Claw xử lý dữ liệu cá nhân, dữ liệu nhạy cảm, bí mật kinh doanh, tài liệu thuộc diện quản lý đặc thù hoặc bất kỳ dữ liệu nào được pháp luật bảo vệ, người dùng tự chịu trách nhiệm bảo đảm mình có cơ sở pháp lý, thẩm quyền, thông báo và sự đồng ý cần thiết, đồng thời đã thực hiện đánh giá rủi ro và phê duyệt nội bộ nếu cần.

Trừ khi pháp luật hoặc thỏa thuận bằng văn bản yêu cầu khác, V-Claw không có trách nhiệm xác minh người dùng có cơ sở pháp lý hợp lệ để xử lý từng bộ dữ liệu cụ thể hay không. Người dùng nên tránh đưa vào Dịch vụ những dữ liệu nhạy cảm hoặc rủi ro không cần thiết; nếu thật sự cần thiết, người dùng nên áp dụng tối thiểu hóa, ẩn danh, giả danh, mã hóa, kiểm soát truy cập và giới hạn lưu giữ phù hợp.

## 8. Không có nghĩa vụ tiền kiểm hoặc giám sát liên tục

V-Claw không có nghĩa vụ tiền kiểm hoặc giám sát liên tục toàn bộ nội dung, prompt, tệp, đầu ra, request hoặc việc sử dụng phía sau của người dùng. V-Claw có thể thực hiện lấy mẫu, phát hiện tự động, rà soát thủ công hoặc ứng phó sự cố nhằm quản trị rủi ro, tuân thủ hoặc bảo mật, nhưng điều đó không làm phát sinh nghĩa vụ phải phát hiện, ngăn chặn hay bảo đảm tính hợp pháp của hành vi người dùng.

## 9. Sử dụng trong lĩnh vực được quản lý / rủi ro cao

Người dùng không được sử dụng Dịch vụ cho các bối cảnh rủi ro cao cần giấy phép, chứng chỉ, giám sát pháp lý chuyên ngành, rà soát thủ công bắt buộc hoặc các kiểm soát tuân thủ tăng cường khác, trừ khi người dùng đã tự xác nhận mọi luật áp dụng, quy tắc ngành và kiểm soát nội bộ đều được đáp ứng.

Ví dụ gồm: quyết định chẩn đoán hoặc điều trị y tế, thay thế tư vấn pháp lý, quyết định tín dụng hoặc quản trị rủi ro tài chính, điều khiển hạ tầng quan trọng, xử lý dữ liệu cực kỳ nhạy cảm của trẻ em/vị thành niên, tự động hóa thực thi pháp luật hoặc giám sát, và bất kỳ cách sử dụng nào có thể ảnh hưởng đáng kể đến an toàn, tài sản, việc làm, giáo dục, tín dụng hoặc quyền của một người.

## 10. Sử dụng được chấp nhận

Người dùng không được sử dụng Dịch vụ cho hoạt động bất hợp pháp, lạm dụng, gây hại, gian lận, xâm phạm quyền hoặc không được phép. Bao gồm nhưng không giới hạn:

- vi phạm pháp luật hoặc quy định hiện hành;
- tạo, phân phối hoặc hỗ trợ nội dung gây hại, bất hợp pháp, xâm phạm quyền hoặc lạm dụng;
- tấn công, scraping, gây quá tải, dò quét hoặc vượt qua giới hạn của Dịch vụ;
- bán lại hoặc phân phối lại quyền truy cập khi chưa được phép;
- cố gắng truy cập trái phép hệ thống, tài khoản, mô hình, tuyến xử lý hoặc dữ liệu;
- sử dụng thông tin đăng nhập bị rò rỉ, bị đánh cắp, không được phép hoặc được chia sẻ trái quy định;
- vi phạm chính sách của các nhà cung cấp AI thượng nguồn có liên quan.

Nếu V-Claw có cơ sở hợp lý cho rằng người dùng vi phạm Điều khoản, lạm dụng Dịch vụ hoặc tạo rủi ro cho nền tảng, nhà cung cấp thượng nguồn hoặc người dùng khác, V-Claw có thể tạm dừng, hạn chế, xoay/đổi key hoặc chấm dứt quyền truy cập.

## 11. Yêu cầu hợp pháp và hợp tác tuân thủ

Nếu V-Claw nhận được yêu cầu pháp lý hợp lệ, lệnh của tòa án, yêu cầu của cơ quan quản lý, yêu cầu hỗ trợ từ cơ quan thực thi pháp luật hoặc thủ tục bắt buộc khác, V-Claw có thể thực hiện các biện pháp được yêu cầu hoặc được phép theo luật, bao gồm lưu giữ, tiết lộ, hạn chế truy cập, đóng băng tính năng hoặc hợp tác điều tra.

Người dùng đồng ý hợp tác hợp lý với việc rà soát tuân thủ, xử lý tranh chấp, điều tra lạm dụng, xác minh thanh toán, xác minh danh tính và giảm thiểu rủi ro của V-Claw, đồng thời cung cấp thông tin, tài liệu hoặc giải trình khi được yêu cầu.

## 12. Hoàn tiền và điều chỉnh số dư

Vì credit được tiêu thụ khi yêu cầu được xử lý, credit đã sử dụng nhìn chung không được hoàn lại.

Yêu cầu hoàn tiền hoặc điều chỉnh số dư đối với credit chưa sử dụng có thể được xem xét theo từng trường hợp, phụ thuộc vào giới hạn của phương thức thanh toán, quy tắc gói, quy tắc credit khuyến mại hoặc bonus, chi phí thượng nguồn đã phát sinh, kiểm tra lạm dụng và khả năng vận hành.

V-Claw không bắt buộc phải hoàn credit đã tiêu thụ, credit liên quan đến hành vi lạm dụng, credit khuyến mại, credit bonus, hoặc credit bị ảnh hưởng bởi lỗi phía người dùng như lộ API key, tích hợp sai, gửi request ngoài ý muốn hoặc quản lý thông tin đăng nhập không an toàn.

## 13. Không bảo hành đầu ra AI

Đầu ra do AI tạo có thể không chính xác, không đầy đủ, chậm, gây khó chịu hoặc không phù hợp với một mục đích cụ thể. Người dùng tự chịu trách nhiệm kiểm tra, xác minh và quyết định cách sử dụng đầu ra AI.

V-Claw không bảo đảm tính chính xác, hợp pháp, đáng tin cậy, độ sẵn sàng hoặc sự phù hợp thương mại của bất kỳ nội dung do AI tạo hoặc phản hồi mô hình bên thứ ba nào.

## 14. Độ sẵn sàng và bảo trì Dịch vụ

V-Claw có thể thực hiện bảo trì, nâng cấp, thay đổi tuyến xử lý, hành động bảo mật hoặc tạm dừng khẩn cấp khi cần thiết. Dịch vụ có thể tạm thời không khả dụng do lỗi kỹ thuật, sự cố nhà cung cấp thượng nguồn, phòng chống lạm dụng, sự cố mạng, sự cố bảo mật hoặc lý do vận hành khác.

V-Claw sẽ nỗ lực hợp lý để duy trì Dịch vụ, nhưng không bảo đảm hoạt động liên tục hoặc không có lỗi.

## 15. Giới hạn trách nhiệm

Trong phạm vi tối đa được pháp luật cho phép, V-Claw không chịu trách nhiệm đối với thiệt hại gián tiếp, ngẫu nhiên, hệ quả, đặc biệt, trừng phạt, mất kinh doanh, mất dữ liệu, mất doanh thu hoặc mất lợi nhuận phát sinh từ việc sử dụng hoặc không thể sử dụng Dịch vụ.

Người dùng chịu trách nhiệm về việc sử dụng Dịch vụ của mình, bao gồm API request, nội dung được tạo, bảo mật tài khoản, tuân thủ pháp luật, tích hợp phía sau và mọi hệ quả phát sinh từ việc sử dụng.

## 16. Bồi thường và miễn trách

Trong phạm vi tối đa được pháp luật cho phép, người dùng đồng ý bồi thường, bảo vệ và giữ cho V-Claw, người vận hành, đơn vị liên quan, nhân viên, nhà thầu và nhà cung cấp của V-Claw không bị thiệt hại từ mọi khiếu nại, điều tra, tổn thất, trách nhiệm, thiệt hại, chi phí hoặc khoản phí phát sinh từ hoặc liên quan đến:

- việc người dùng vi phạm Điều khoản này, pháp luật áp dụng, quyền của bên thứ ba hoặc quy tắc của nhà cung cấp thượng nguồn;
- nội dung, dữ liệu, request hoặc tích hợp phía sau mà người dùng gửi hoặc sử dụng;
- việc tài khoản, API key, thiết bị, script tự động hoặc công cụ bên thứ ba của người dùng bị lạm dụng hoặc bị lộ;
- việc không có được cơ sở pháp lý, sự đồng ý, thông báo hoặc thẩm quyền cần thiết.

## 17. Thay đổi Điều khoản

V-Claw có thể cập nhật Điều khoản này theo thời gian để phản ánh thay đổi dịch vụ, thay đổi giá, thay đổi nhà cung cấp thượng nguồn, yêu cầu pháp lý, yêu cầu quản trị rủi ro hoặc nhu cầu vận hành.

Việc tiếp tục sử dụng Dịch vụ sau khi Điều khoản cập nhật được công bố đồng nghĩa với việc chấp nhận Điều khoản cập nhật.

## 18. Liên hệ

Nếu có câu hỏi về Điều khoản này, credit sử dụng, thanh toán hoặc quyền truy cập tài khoản, vui lòng liên hệ bộ phận hỗ trợ V-Claw qua kênh hỗ trợ chính thức được cung cấp trên nền tảng.`,
				"ko": `# V-Claw 서비스 약관

최종 업데이트: 2026-06-20

## 1. 목적

본 약관은 공유 AI API 접근 및 내부 사용 크레딧 서비스인 V-Claw의 이용에 적용됩니다. 계정 생성, 사용 크레딧 구매 또는 수령, API Key 생성, 서비스 이용 시 사용자는 본 약관에 동의한 것으로 간주됩니다.

V-Claw는 공유 인프라를 통해 지원되는 AI 모델에 접근할 수 있도록 제공되는 서비스 플랫폼입니다. 명시적으로 달리 공지되지 않는 한, V-Claw는 제3자 AI 모델 제공업체의 공식 제품이 아니며 해당 제공업체와 공식 제휴 관계에 있지 않습니다.

## 2. 서비스의 성격

V-Claw는 플랫폼을 통해 지원되는 AI 모델 엔드포인트에 대한 접근을 제공합니다. 사용자는 내부 사용 크레딧을 구매하거나 받을 수 있으며, 요청이 서비스를 통해 처리될 때 해당 크레딧이 차감됩니다.

사용 크레딧은 서비스 내부 크레딧일 뿐입니다. 현금, 암호화폐, 증권, 블록체인 토큰, 저장가치 수단 또는 양도 가능한 디지털 자산이 아닙니다. 크레딧은 V-Claw 플랫폼 외부에서 가치를 가지지 않으며 플랫폼 내 지원 서비스에만 사용할 수 있습니다.

## 3. 기술 중개자 및 사용자 제어 활동

V-Claw는 라우팅, 계정 관리, 할당량 측정, 속도 제어, 감사 및 보안 기능을 제공하는 기술 중개자 역할을 합니다. 플랫폼에 명시적으로 제공된 관리 기능을 제외하고, V-Claw는 사용자의 프롬프트, 입력 데이터, 출력 사용 방식, 최종 업무 목적 또는 하위 배포 방식을 결정하지 않습니다.

사용자는 제출한 프롬프트, 처리한 파일 또는 데이터, 선택한 모델, 수신한 출력, 해당 출력의 저장/삭제/전달 방식 및 모든 하위 사용에 대해 전적으로 책임을 집니다. 사용자는 자신의 활동이 합법적인지, 허가를 받았는지, 통지/동의/기타 제3자 승인 여부가 필요한지 스스로 판단해야 합니다.

## 4. 계정 및 API Key 보안

사용자는 자신의 계정, API Key, 기기 및 자격 증명의 보안을 유지할 책임이 있습니다. 사용자의 계정 또는 API Key를 통해 발생한 활동은 해당 사용자의 활동으로 간주될 수 있습니다.

V-Claw의 명시적 허가 없이 사용자는 서비스를 공유, 재판매, 재라이선스, 남용, 역공학, 과부하 사용 또는 우회하려고 시도해서는 안 됩니다. 비정상 사용, 자격 증명 유출, 남용 또는 정책 위반이 감지되면 V-Claw는 접근을 일시 중지, 키를 교체, 제한 또는 종료할 수 있습니다.

## 5. 사용 크레딧, 요율 및 과금

크레딧은 사용 시점에 플랫폼에 표시되거나 설정된 가격, 모델 요율, 배율, 패키지 규칙 및 사용 정책에 따라 차감됩니다.

실제 사용량은 모델 유형, 입력 토큰, 출력 토큰, 캐시 토큰, 도구 호출, 라우팅, 상위 제공업체 가격, 배율, 패키지 설정 또는 기타 기술적 요인에 따라 달라질 수 있습니다. 사용자는 사용 전후의 사용 기록과 잔액을 직접 확인해야 합니다.

V-Claw는 상위 제공업체 변경, 인프라 비용 변경, 리스크 관리 요구 또는 운영상 필요에 따라 지원 모델, 라우트, 가격, 패키지 규칙 또는 배율을 수시로 업데이트할 수 있습니다. 중요한 변경 사항은 가능한 경우 플랫폼 또는 공식 지원 채널을 통해 표시되거나 안내됩니다.

## 6. 상위 제공업체 의존성

서비스는 제3자 AI 모델 제공업체, 네트워크 연결, 계정 가용성, 할당량, 속도 제한, 제공업체 정책 및 상위 서비스의 가용성에 의존합니다.

V-Claw는 특정 모델, 제공업체, 라우트, 속도, 할당량 또는 기능이 항상 제공된다고 보장하지 않습니다. 상위 또는 운영 요인으로 인해 모델은 예고 없이 이용 불가, 지연, 교체, 속도 제한, 품질 저하 또는 지원 중단될 수 있습니다.

## 7. 사용자 법적 근거와 데이터 책임

개인 데이터 또는 기타 보호된 데이터를 V-Claw에 제출하는 경우, 해당 데이터 처리에 필요한 법적 근거, 고지 및 필요한 동의를 보유하고 있으며, 필요한 경우 위험 평가 및 내부 승인을 완료했는지 사용자가 직접 책임져야 합니다.

법률이 요구하지 않는 한, V-Claw는 특정 데이터셋을 처리할 적법한 근거가 있는지 판단하지 않습니다. 불필요한 민감 데이터나 고위험 데이터는 제출하지 않는 것이 좋습니다. 필요한 경우 최소화, 익명화/가명화, 마스킹 및 접근 제한을 먼저 적용해야 합니다.

## 8. 사전 검토 또는 지속적 모니터링 의무 없음

V-Claw는 모든 사용자 콘텐츠, 프롬프트, 파일, 출력, 요청 또는 하위 사용을 사전 검토하거나 지속적으로 모니터링할 의무가 없습니다. V-Claw는 리스크 관리, 준수 또는 보안 목적을 위해 샘플링, 자동 감지, 수동 검토 또는 사고 대응을 수행할 수 있지만, 이는 사용자 행위의 적법성을 식별, 차단 또는 보장할 의무를 의미하지 않습니다.

## 9. 규제 대상 / 고위험 사용

사용자는 적용 법률, 업계 규칙 및 내부 통제가 모두 충족됨을 직접 확인하지 않는 한, 면허, 자격증, 특별 규제 감독, 의무적 인적 검토 또는 강화된 준수 통제가 필요한 고위험 상황에 서비스를 사용해서는 안 됩니다.

고위험 사용에는 의료 진단/치료 결정, 법률 자문 대체, 금융 신용 또는 리스크 결정, 중요 인프라 제어, 아동/미성년자의 매우 민감한 데이터 처리, 법집행 또는 감시 자동화, 그리고 사람의 안전, 재산, 고용, 교육, 신용 또는 권리에 중대한 영향을 줄 수 있는 모든 사용이 포함됩니다.

## 10. 허용 가능한 사용

사용자는 서비스를 불법, 남용, 유해, 사기, 침해 또는 무단 활동에 사용해서는 안 됩니다. 여기에는 다음이 포함되지만 이에 한정되지 않습니다.

- 적용 법률 또는 규정 위반;
- 유해, 불법, 침해 또는 남용 콘텐츠의 생성, 배포 또는 조력;
- 서비스 공격, 스크래핑, 과부하, 탐지 또는 우회;
- 허가 없는 접근권 재판매 또는 재배포;
- 시스템, 계정, 모델, 라우트 또는 데이터에 대한 무단 접근 시도;
- 유출, 도난, 무단 또는 규칙 위반 방식으로 공유된 자격 증명 사용;
- 관련 상위 AI 제공업체 정책 위반.

V-Claw는 사용자가 본 약관을 위반했거나 서비스를 남용했거나 플랫폼, 상위 제공업체 또는 다른 사용자에게 위험을 초래했다고 합리적으로 판단하는 경우 접근을 일시 중지, 제한, 키 교체 또는 종료할 수 있습니다.

## 11. 적법한 요청 및 준수 협력

V-Claw가 유효한 법적 요청, 법원 명령, 규제 요구, 법집행 요청 또는 기타 강제 절차를 받는 경우, V-Claw는 보존, 공개, 접근 제한, 기능 동결 또는 조사 협력 등 법률이 요구하거나 허용하는 조치를 취할 수 있습니다.

사용자는 V-Claw의 준수 검토, 분쟁 처리, 남용 조사, 결제 검증, 신원 확인 및 리스크 완화에 합리적으로 협력하고, 요청 시 필요한 정보, 문서 또는 설명을 제공하는 데 동의합니다.

## 12. 환불 및 잔액 조정

사용 크레딧은 요청이 처리될 때 차감되므로 이미 사용된 크레딧은 일반적으로 환불되지 않습니다.

미사용 크레딧에 대한 환불 또는 잔액 조정은 결제 수단 제한, 패키지 규칙, 프로모션 또는 보너스 크레딧 규칙, 이미 발생한 상위 비용, 남용 검사 및 운영 가능성에 따라 사례별로 검토될 수 있습니다.

V-Claw는 이미 소비된 크레딧, 남용 활동과 관련된 크레딧, 프로모션 크레딧, 보너스 크레딧, 또는 API Key 유출, 잘못된 연동, 의도하지 않은 요청, 안전하지 않은 자격 증명 관리 등 사용자 측 문제로 영향을 받은 크레딧을 환불할 의무가 없습니다.

## 13. AI 출력에 대한 보증 없음

AI 생성 출력은 부정확하거나 불완전하거나 지연되거나 불쾌하거나 특정 목적에 적합하지 않을 수 있습니다. 사용자는 AI 출력을 검토, 확인하고 사용 여부와 방법을 결정할 전적인 책임이 있습니다.

V-Claw는 AI 생성 콘텐츠 또는 제3자 모델 응답의 정확성, 합법성, 신뢰성, 가용성 또는 상업적 적합성을 보장하지 않습니다.

## 14. 서비스 가용성 및 유지보수

V-Claw는 필요한 경우 유지보수, 업그레이드, 라우팅 변경, 보안 조치 또는 긴급 중단을 수행할 수 있습니다. 서비스는 기술 문제, 상위 제공업체 문제, 남용 방지, 네트워크 문제, 보안 사고 또는 기타 운영상 이유로 일시적으로 이용할 수 없을 수 있습니다.

V-Claw는 서비스를 유지하기 위해 합리적인 노력을 기울이지만, 중단 없는 운영 또는 무오류 운영을 보장하지 않습니다.

## 15. 책임 제한

관련 법률이 허용하는 최대 범위 내에서 V-Claw는 서비스 사용 또는 사용 불능으로 인한 간접, 부수, 결과, 특별, 징벌, 사업 손실, 데이터 손실, 매출 손실 또는 이익 손실에 대해 책임을 지지 않습니다.

사용자는 API 요청, 생성 콘텐츠, 계정 보안, 법적 준수, 하위 연동 및 사용 결과를 포함하여 자신의 서비스 이용에 대한 책임을 집니다.

## 16. 배상 및 면책

관련 법률이 허용하는 최대 범위 내에서 사용자는 V-Claw, 운영자, 관계사, 직원, 계약자 및 공급업체를 다음과 관련된 청구, 조사, 손실, 책임, 손해, 비용 또는 지출로부터 배상하고 방어하며 면책하는 데 동의합니다.

- 본 약관, 적용 법률, 제3자 권리 또는 상위 제공업체 규칙 위반;
- 사용자가 제출하거나 사용하는 콘텐츠, 데이터, 요청 또는 하위 연동;
- 계정, API Key, 기기, 자동화 스크립트 또는 제3자 도구의 오용 또는 유출;
- 필요한 법적 근거, 동의, 고지 또는 승인을 확보하지 못한 경우.

## 17. 약관 변경

V-Claw는 서비스 변경, 가격 변경, 상위 제공업체 변경, 법적 요구, 리스크 관리 요구 또는 운영상 필요를 반영하기 위해 본 약관을 수시로 업데이트할 수 있습니다.

업데이트된 약관이 제공된 이후 서비스를 계속 이용하는 것은 업데이트된 약관에 동의하는 것으로 간주됩니다.

## 18. 문의

본 약관, 사용 크레딧, 과금 또는 계정 접근에 관한 질문은 플랫폼에서 제공하는 공식 지원 채널을 통해 V-Claw 지원팀에 문의하십시오.`,
			},
		},
		{
			ID:    "usage-policy",
			Title: `使用政策`,
			ContentMD: `# V-Claw 使用政策

本政策适用于 V-Claw 共享 AI API 访问、内部用量额度、API Key 及相关平台功能的所有使用行为。

## 一、合法且负责任的使用

用户只能将本服务用于合法、授权且负责任的目的。用户应对其提交的提示词、处理的文件或数据、生成并保存或分发的输出，以及本服务的任何下游使用承担责任。

## 二、禁止行为

用户不得利用本服务从事以下活动：

- 违反适用法律法规、制裁、出口管制、隐私规则或合同义务；
- 生成、请求、传播或协助生成违法、有害、滥用、侵权、欺诈或剥削性内容；
- 攻击、扫描、抓取、过载、干扰或绕过 V-Claw、上游提供商或第三方系统；
- 未经许可转售、共享或再分发 API 访问、额度、路由、账号或密钥；
- 使用泄露、被盗、未授权或不当共享的凭据；
- 隐藏滥用流量、规避速率限制、绕过风控或操纵计费记录；
- 违反相关上游 AI 提供商的条款、可接受使用政策或技术限制。

## 三、监测与处置

为保护平台、上游提供商和其他用户，V-Claw 可使用日志、额度控制、速率限制、滥用检查、密钥轮换、路由限制、人工审核及自动风控。

若 V-Claw 有合理依据认为某项使用带来法律、安全、运营、计费或上游政策风险，可在通知或不通知的情况下限制、暂停、轮换密钥、拒绝请求、移除路由或终止访问。

## 四、用户责任

用户应对其账号和 API Key 下的所有活动负责，包括因密钥泄露、不安全集成、公开代码仓库、浏览器扩展、自动化脚本或第三方工具导致的活动。

本服务并不保证每个提示词、输出、集成或下游使用均自动合规。用户应根据自身使用场景自行完成审查、过滤、留存、删除、披露及合规控制。

## 五、滥用与投诉渠道

如果用户认为服务被误判、错误限制，或发现潜在滥用/安全问题，可以通过平台提供的官方支持渠道提交投诉或申诉。为便于处理，请提供账户标识、时间、请求概述、相关路由/页面信息以及问题说明；不要在工单中附上不必要的密钥或敏感材料。`,
			TitleI18n: map[string]string{
				"zh": `使用政策`,
				"en": `Usage Policy`,
				"vi": `Chính sách sử dụng`,
				"ko": `사용 정책`,
			},
			ContentMDI18n: map[string]string{
				"zh": `# V-Claw 使用政策

本政策适用于 V-Claw 共享 AI API 访问、内部用量额度、API Key 及相关平台功能的所有使用行为。

## 一、合法且负责任的使用

用户只能将本服务用于合法、授权且负责任的目的。用户应对其提交的提示词、处理的文件或数据、生成并保存或分发的输出，以及本服务的任何下游使用承担责任。

## 二、禁止行为

用户不得利用本服务从事以下活动：

- 违反适用法律法规、制裁、出口管制、隐私规则或合同义务；
- 生成、请求、传播或协助生成违法、有害、滥用、侵权、欺诈或剥削性内容；
- 攻击、扫描、抓取、过载、干扰或绕过 V-Claw、上游提供商或第三方系统；
- 未经许可转售、共享或再分发 API 访问、额度、路由、账号或密钥；
- 使用泄露、被盗、未授权或不当共享的凭据；
- 隐藏滥用流量、规避速率限制、绕过风控或操纵计费记录；
- 违反相关上游 AI 提供商的条款、可接受使用政策或技术限制。

## 三、监测与处置

为保护平台、上游提供商和其他用户，V-Claw 可使用日志、额度控制、速率限制、滥用检查、密钥轮换、路由限制、人工审核及自动风控。

若 V-Claw 有合理依据认为某项使用带来法律、安全、运营、计费或上游政策风险，可在通知或不通知的情况下限制、暂停、轮换密钥、拒绝请求、移除路由或终止访问。

## 四、用户责任

用户应对其账号和 API Key 下的所有活动负责，包括因密钥泄露、不安全集成、公开代码仓库、浏览器扩展、自动化脚本或第三方工具导致的活动。

本服务并不保证每个提示词、输出、集成或下游使用均自动合规。用户应根据自身使用场景自行完成审查、过滤、留存、删除、披露及合规控制。

## 五、滥用与投诉渠道

如果用户认为服务被误判、错误限制，或发现潜在滥用/安全问题，可以通过平台提供的官方支持渠道提交投诉或申诉。为便于处理，请提供账户标识、时间、请求概述、相关路由/页面信息以及问题说明；不要在工单中附上不必要的密钥或敏感材料。`,
				"en": `# V-Claw Usage Policy

This policy applies to all use of V-Claw shared AI API access, internal usage credits, API keys, and related platform functions.

## 1. Lawful and Responsible Use

Users must use the Service only for lawful, authorized, and responsible purposes. Users are responsible for the prompts they submit, files or data they process, generated outputs they store or distribute, and any downstream use of the Service.

## 2. Prohibited Conduct

Users must not use the Service to:

- violate applicable laws, regulations, sanctions, export controls, privacy rules, or contractual obligations;
- generate, request, distribute, or facilitate illegal, harmful, abusive, infringing, deceptive, or exploitative content;
- attack, scan, scrape, overload, disrupt, or bypass V-Claw, upstream providers, or third-party systems;
- resell, share, or redistribute API access, credits, routes, accounts, or keys without permission;
- use leaked, stolen, unauthorized, or improperly shared credentials;
- attempt to hide abusive traffic, evade rate limits, bypass risk controls, or manipulate billing records;
- violate the terms, acceptable-use policies, or technical restrictions of applicable upstream AI providers.

## 3. Monitoring and Enforcement

V-Claw may use logs, quota controls, rate limits, abuse checks, key rotation, route restrictions, manual review, and automated risk controls to protect the platform, upstream providers, and other users.

If V-Claw reasonably believes that usage creates legal, security, operational, billing, or upstream-policy risk, V-Claw may restrict, suspend, rotate keys, reject requests, remove routes, or terminate access with or without prior notice.

## 4. User Responsibility

Users remain responsible for all activity under their accounts and API keys, including activity caused by leaked keys, insecure integrations, public repositories, browser extensions, automation scripts, or third-party tools.

The Service is not designed to guarantee that every prompt, output, integration, or downstream use is compliant. Users must perform their own review, filtering, retention, deletion, disclosure, and compliance controls appropriate to their use case.

## 5. Abuse and Complaint Channel

If you believe the Service was incorrectly restricted, or if you want to report abuse or a security issue, contact the official support channel provided by the platform. Please include the account identifier, approximate time, request summary, route/page involved, and the nature of the issue. Do not include unnecessary keys or sensitive data in the ticket.`,
				"vi": `# Chính sách sử dụng V-Claw

Chính sách này áp dụng cho mọi hoạt động sử dụng quyền truy cập AI API dùng chung của V-Claw, credit sử dụng nội bộ, API key và các chức năng liên quan của nền tảng.

## 1. Sử dụng hợp pháp và có trách nhiệm

Người dùng chỉ được sử dụng Dịch vụ cho mục đích hợp pháp, được phép và có trách nhiệm. Người dùng chịu trách nhiệm về prompt gửi lên, tệp hoặc dữ liệu xử lý, đầu ra được tạo, nội dung lưu trữ hoặc phân phối và mọi cách sử dụng Dịch vụ ở phía sau.

## 2. Hành vi bị cấm

Người dùng không được sử dụng Dịch vụ để:

- vi phạm pháp luật, quy định, lệnh cấm vận, kiểm soát xuất khẩu, quy định quyền riêng tư hoặc nghĩa vụ hợp đồng;
- tạo, yêu cầu, phân phối hoặc hỗ trợ nội dung bất hợp pháp, gây hại, lạm dụng, xâm phạm quyền, lừa đảo hoặc bóc lột;
- tấn công, quét, scraping, gây quá tải, làm gián đoạn hoặc vượt qua giới hạn của V-Claw, nhà cung cấp thượng nguồn hoặc hệ thống bên thứ ba;
- bán lại, chia sẻ hoặc phân phối lại quyền truy cập API, credit, route, tài khoản hoặc key khi chưa được phép;
- sử dụng thông tin đăng nhập bị rò rỉ, bị đánh cắp, không được phép hoặc được chia sẻ không đúng quy định;
- che giấu traffic lạm dụng, né rate limit, vượt qua kiểm soát rủi ro hoặc thao túng bản ghi tính phí;
- vi phạm điều khoản, chính sách sử dụng được chấp nhận hoặc giới hạn kỹ thuật của nhà cung cấp AI thượng nguồn có liên quan.

## 3. Giám sát và thực thi

V-Claw có thể sử dụng log, kiểm soát quota, rate limit, kiểm tra lạm dụng, xoay/đổi key, giới hạn route, rà soát thủ công và kiểm soát rủi ro tự động để bảo vệ nền tảng, nhà cung cấp thượng nguồn và người dùng khác.

Nếu V-Claw có cơ sở hợp lý cho rằng việc sử dụng tạo rủi ro pháp lý, bảo mật, vận hành, tính phí hoặc rủi ro theo chính sách thượng nguồn, V-Claw có thể hạn chế, tạm dừng, xoay/đổi key, từ chối request, gỡ route hoặc chấm dứt truy cập, có hoặc không thông báo trước.

## 4. Trách nhiệm người dùng

Người dùng chịu trách nhiệm với mọi hoạt động dưới tài khoản và API key của mình, bao gồm hoạt động phát sinh do lộ key, tích hợp không an toàn, repository công khai, extension trình duyệt, script tự động hoặc công cụ bên thứ ba.

Dịch vụ không được thiết kế để bảo đảm mọi prompt, đầu ra, tích hợp hoặc cách sử dụng phía sau đều tuân thủ. Người dùng phải tự thực hiện rà soát, lọc, lưu giữ, xóa, công bố và kiểm soát tuân thủ phù hợp với trường hợp sử dụng của mình.

## 5. Kênh phản ánh và khiếu nại

Nếu bạn cho rằng Dịch vụ bị hạn chế sai, hoặc muốn báo cáo lạm dụng/sự cố bảo mật, hãy liên hệ kênh hỗ trợ chính thức của nền tảng. Vui lòng cung cấp mã tài khoản, thời điểm ước lượng, tóm tắt request, route/trang liên quan và mô tả vấn đề. Không gửi key hoặc dữ liệu nhạy cảm không cần thiết trong ticket.`,
				"ko": `# V-Claw 사용 정책

본 정책은 V-Claw 공유 AI API 접근, 내부 사용 크레딧, API Key 및 관련 플랫폼 기능의 모든 사용에 적용됩니다.

## 1. 합법적이고 책임 있는 사용

사용자는 서비스를 합법적이고 승인된 책임 있는 목적에만 사용해야 합니다. 사용자는 제출한 프롬프트, 처리한 파일 또는 데이터, 저장하거나 배포한 생성 출력, 서비스의 모든 하위 사용에 대해 책임을 집니다.

## 2. 금지 행위

사용자는 서비스를 다음 목적에 사용해서는 안 됩니다.

- 적용 법률, 규정, 제재, 수출 통제, 개인정보 보호 규칙 또는 계약상 의무 위반;
- 불법, 유해, 남용, 침해, 사기 또는 착취적 콘텐츠의 생성, 요청, 배포 또는 조력;
- V-Claw, 상위 제공업체 또는 제3자 시스템에 대한 공격, 스캔, 스크래핑, 과부하, 방해 또는 우회;
- 허가 없는 API 접근, 크레딧, 라우트, 계정 또는 키의 재판매, 공유 또는 재배포;
- 유출, 도난, 무단 또는 부적절하게 공유된 자격 증명 사용;
- 남용 트래픽 은닉, 속도 제한 회피, 리스크 통제 우회 또는 과금 기록 조작;
- 관련 상위 AI 제공업체의 약관, 허용 사용 정책 또는 기술 제한 위반.

## 3. 모니터링 및 집행

V-Claw는 플랫폼, 상위 제공업체 및 다른 사용자를 보호하기 위해 로그, 할당량 제어, 속도 제한, 남용 검사, 키 교체, 라우트 제한, 수동 검토 및 자동 리스크 통제를 사용할 수 있습니다.

V-Claw가 특정 사용이 법적, 보안, 운영, 과금 또는 상위 정책상 위험을 초래한다고 합리적으로 판단하는 경우 사전 통지 여부와 관계없이 접근 제한, 일시 중지, 키 교체, 요청 거부, 라우트 제거 또는 접근 종료를 할 수 있습니다.

## 4. 사용자 책임

사용자는 유출된 키, 안전하지 않은 연동, 공개 저장소, 브라우저 확장, 자동화 스크립트 또는 제3자 도구로 인해 발생한 활동을 포함하여 자신의 계정 및 API Key 하의 모든 활동에 대해 책임을 집니다.

서비스는 모든 프롬프트, 출력, 연동 또는 하위 사용이 자동으로 준수된다고 보장하도록 설계되지 않았습니다. 사용자는 자신의 사용 사례에 맞는 검토, 필터링, 보관, 삭제, 공개 및 준수 통제를 직접 수행해야 합니다.

## 5. 남용 및 신고 채널

서비스가 잘못 제한되었거나, 남용/보안 문제를 신고하려면 플랫폼의 공식 지원 채널로 문의하세요. 계정 식별자, 대략적인 시간, 요청 요약, 관련 라우트/페이지, 문제 설명을 포함해 주세요. 티켓에는 불필요한 키나 민감한 데이터를 넣지 마세요.`,
			},
		},
		{
			ID:    "supported-regions",
			Title: `支持地区`,
			ContentMD: `# 支持地区

V-Claw 仅在适用法律、上游提供商条款、网络条件、支付限制及当地合规要求允许的地区提供。

用户应自行确认其居住国家或地区、服务器所在地、业务所在地及实际使用地允许访问和使用本服务。

若法律法规、制裁、网络条件、支付限制、上游限制或风控要求导致本服务不可用、受限或不适合提供，V-Claw 可拒绝、暂停、限制或终止服务。`,
			TitleI18n: map[string]string{
				"zh": `支持地区`,
				"en": `Supported Regions`,
				"vi": `Khu vực hỗ trợ`,
				"ko": `지원 지역`,
			},
			ContentMDI18n: map[string]string{
				"zh": `# 支持地区

V-Claw 仅在适用法律、上游提供商条款、网络条件、支付限制及当地合规要求允许的地区提供。

用户应自行确认其居住国家或地区、服务器所在地、业务所在地及实际使用地允许访问和使用本服务。

若法律法规、制裁、网络条件、支付限制、上游限制或风控要求导致本服务不可用、受限或不适合提供，V-Claw 可拒绝、暂停、限制或终止服务。`,
				"en": `# Supported Regions

V-Claw is available only where applicable laws, upstream provider terms, network conditions, payment constraints, and local compliance requirements permit its use.

Users are responsible for confirming that access to and use of the Service are allowed in their country or region of residence, server location, business location, and actual place of use.

V-Claw may refuse, suspend, restrict, or terminate service where laws, regulations, sanctions, network conditions, payment limitations, upstream restrictions, or risk-control requirements make the Service unavailable, restricted, or unsuitable.`,
				"vi": `# Khu vực hỗ trợ

V-Claw chỉ được cung cấp tại các khu vực mà pháp luật hiện hành, điều khoản của nhà cung cấp thượng nguồn, điều kiện mạng, giới hạn thanh toán và yêu cầu tuân thủ địa phương cho phép sử dụng.

Người dùng có trách nhiệm tự xác nhận rằng việc truy cập và sử dụng Dịch vụ được phép tại quốc gia/khu vực cư trú, vị trí máy chủ, địa điểm kinh doanh và nơi sử dụng thực tế của mình.

V-Claw có thể từ chối, tạm dừng, hạn chế hoặc chấm dứt dịch vụ nếu pháp luật, quy định, lệnh cấm vận, điều kiện mạng, giới hạn thanh toán, hạn chế thượng nguồn hoặc yêu cầu quản trị rủi ro khiến Dịch vụ không khả dụng, bị hạn chế hoặc không phù hợp.`,
				"ko": `# 지원 지역

V-Claw는 적용 법률, 상위 제공업체 약관, 네트워크 조건, 결제 제한 및 현지 준수 요구가 사용을 허용하는 지역에서만 제공됩니다.

사용자는 거주 국가 또는 지역, 서버 위치, 사업 위치 및 실제 사용 장소에서 서비스 접근과 사용이 허용되는지 직접 확인해야 합니다.

법률, 규정, 제재, 네트워크 조건, 결제 제한, 상위 제한 또는 리스크 관리 요구로 인해 서비스 제공이 불가능하거나 제한되거나 부적합한 경우 V-Claw는 서비스를 거부, 일시 중지, 제한 또는 종료할 수 있습니다.`,
			},
		},
		{
			ID:    "service-specific-terms",
			Title: `服务特定条款`,
			ContentMD: `# 服务特定条款

不同模型、路由、账号类型、API Key、套餐、余额产品、赠送额度或平台功能，可能适用额外限制、有效期、使用上限、价格规则、退款审查规则、风控措施或上游提供商限制。

用户在购买额度、选择套餐、生成 API Key、导入账号或使用特定模型/路由前，应阅读相关页面、套餐说明、订单提示、管理员公告或官方支持消息中的具体规则。

若服务特定规则与本通用条款不一致，则该服务适用更严格或更具体的规则。除非 V-Claw 以具体书面服务承诺明确说明，否则不提供安装保修、交付保修、固定可用性承诺或特定模型持续可用保证。`,
			TitleI18n: map[string]string{
				"zh": `服务特定条款`,
				"en": `Service-Specific Terms`,
				"vi": `Điều khoản riêng theo dịch vụ`,
				"ko": `서비스별 약관`,
			},
			ContentMDI18n: map[string]string{
				"zh": `# 服务特定条款

不同模型、路由、账号类型、API Key、套餐、余额产品、赠送额度或平台功能，可能适用额外限制、有效期、使用上限、价格规则、退款审查规则、风控措施或上游提供商限制。

用户在购买额度、选择套餐、生成 API Key、导入账号或使用特定模型/路由前，应阅读相关页面、套餐说明、订单提示、管理员公告或官方支持消息中的具体规则。

若服务特定规则与本通用条款不一致，则该服务适用更严格或更具体的规则。除非 V-Claw 以具体书面服务承诺明确说明，否则不提供安装保修、交付保修、固定可用性承诺或特定模型持续可用保证。`,
				"en": `# Service-Specific Terms

Different models, routes, account types, API keys, packages, balance products, credit grants, or platform features may have additional limits, validity periods, usage caps, pricing rules, refund-review rules, risk controls, or upstream provider restrictions.

Before purchasing credits, selecting a package, generating an API key, importing an account, or using a specific model or route, users should review the rules shown on the relevant page, package description, order notice, admin notice, or official support message.

If a service-specific rule conflicts with these general Terms, the stricter or more specific rule applies for that service. No installation warranty, handover warranty, fixed uptime guarantee, or guaranteed model availability is provided unless it is expressly stated in a specific written service commitment from V-Claw.`,
				"vi": `# Điều khoản riêng theo dịch vụ

Các mô hình, route, loại tài khoản, API key, gói, sản phẩm số dư, credit được cấp hoặc tính năng nền tảng khác nhau có thể áp dụng thêm giới hạn, thời hạn hiệu lực, hạn mức sử dụng, quy tắc giá, quy tắc xem xét hoàn tiền, kiểm soát rủi ro hoặc hạn chế từ nhà cung cấp thượng nguồn.

Trước khi mua credit, chọn gói, tạo API key, nhập tài khoản hoặc sử dụng một mô hình/route cụ thể, người dùng nên đọc quy tắc hiển thị trên trang liên quan, mô tả gói, thông báo đơn hàng, thông báo quản trị hoặc tin nhắn hỗ trợ chính thức.

Nếu quy tắc riêng theo dịch vụ mâu thuẫn với Điều khoản chung này, quy tắc nghiêm ngặt hơn hoặc cụ thể hơn sẽ được áp dụng cho dịch vụ đó. Không có bảo hành cài đặt, bảo hành bàn giao, cam kết uptime cố định hoặc bảo đảm mô hình luôn khả dụng, trừ khi V-Claw có cam kết dịch vụ bằng văn bản cụ thể.`,
				"ko": `# 서비스별 약관

모델, 라우트, 계정 유형, API Key, 패키지, 잔액 상품, 크레딧 지급 또는 플랫폼 기능에 따라 추가 제한, 유효 기간, 사용 한도, 가격 규칙, 환불 검토 규칙, 리스크 통제 또는 상위 제공업체 제한이 적용될 수 있습니다.

크레딧 구매, 패키지 선택, API Key 생성, 계정 가져오기 또는 특정 모델/라우트 사용 전에 사용자는 관련 페이지, 패키지 설명, 주문 안내, 관리자 공지 또는 공식 지원 메시지에 표시된 구체적 규칙을 확인해야 합니다.

서비스별 규칙이 본 일반 약관과 충돌하는 경우 해당 서비스에는 더 엄격하거나 더 구체적인 규칙이 적용됩니다. V-Claw가 구체적인 서면 서비스 약속으로 명시하지 않는 한 설치 보증, 인도 보증, 고정 가동시간 보장 또는 특정 모델의 지속적 이용 가능성 보장은 제공되지 않습니다.`,
			},
		},
		{
			ID:    "privacy-data-processing",
			Title: `隐私与数据处理说明`,
			ContentMD: `# V-Claw 隐私与数据处理说明

最后更新：2026-06-20

本说明适用于 V-Claw 在提供共享 AI API 访问、内部用量额度、账号管理、计费、支持和安全控制过程中对数据的处理。

## 一、我们可能处理的数据

根据您使用的功能，V-Claw 可能处理以下数据：

- 账号信息：邮箱、用户名、头像、角色、登录记录、API Key 标识、余额和使用记录；
- 付款与订单信息：交易编号、支付状态、套餐、订单备注、发票/收据所需信息；
- 服务数据：请求时间、路由、模型、token 统计、错误码、限制事件、审计日志和安全事件；
- 内容数据：您提交的 prompt、文件、附件、上下文、系统消息、输出结果、保存的模板或配置；
- 支持通信：工单、反馈、投诉、聊天记录和您主动提供给支持团队的材料。

## 二、处理目的

我们处理上述数据用于：

- 提供、维护和改进服务；
- 执行账号管理、认证、计费、额度控制和对账；
- 路由请求到上游模型提供商并返回结果；
- 进行故障排查、风控、滥用检测、审计日志分析和安全防护；
- 处理支持请求、投诉、争议和法律合规事项；
- 履行法律义务、监管要求或强制性请求。

## 三、用户的法律基础与责任

如果您向 V-Claw 提交个人数据或其他受保护数据，您应确保自己拥有适用的法律基础、通知和必要同意，并且您已满足所有适用的数据保护义务。

除非法律另有要求，V-Claw 不判断您是否拥有处理某一特定数据集的合法依据。您应避免提交不必要的敏感数据或高风险数据；如确有必要，请先进行最小化、匿名化/假名化、脱敏和访问限制。

## 四、与上游和第三方共享 / 国际传输

为提供服务，您的请求、提示词、附件、上下文或相关元数据可能会被发送给上游 AI 提供商、云基础设施提供商、支付处理方、消息/邮件系统或其他服务商。上述接收方可能位于您所在司法辖区之外。

这些接收方对其处理可能适用各自的政策、条款和跨境传输规则。V-Claw 会在合理范围内选择服务商并采取必要的技术与组织措施，但无法保证所有第三方处理都完全在同一司法辖区内完成。

## 五、保留期限

我们不会无限期保存所有数据。不同数据会根据业务需要、法律义务、风控需求、争议处理、账务要求和备份策略，在不同期限内保留。

例如，账号和账务记录可能需要更长时间保存；请求日志、审计记录、缓存或临时处理数据通常只在实现服务、排障或安全目的所需的期间内保留。具体期限可能因配置、法律要求或运营需要而变化。

## 六、安全措施

我们采用合理的管理、技术和组织措施来保护数据，包括访问控制、权限最小化、加密、密钥管理、审计、速率限制、异常检测和安全隔离。

但任何系统都无法保证绝对安全。若发生安全事件，V-Claw 可在必要时暂停部分功能、限制访问、轮换密钥或采取其他响应措施。

## 七、敏感或高风险数据

除非您有明确的法律基础和适当的安全措施，否则不要提交敏感个人数据、儿童/未成年人数据、健康数据、财务身份数据、政府识别信息、客户机密或可能造成重大损害的高风险信息。

## 八、权利、请求与投诉

在适用法律允许的范围内，您可以就您的数据提出访问、更正、删除、限制处理或其他权利请求。若您希望提出隐私相关投诉或询问，请通过平台官方支持渠道联系 V-Claw。

## 九、更新

我们可能不时更新本说明，以反映法律、产品或安全要求的变化。`,
			TitleI18n: map[string]string{
				"zh": `隐私与数据处理说明`,
				"en": `Privacy & Data Processing Notice`,
				"vi": `Thông báo Quyền riêng tư & Xử lý dữ liệu`,
				"ko": `개인정보 보호 및 데이터 처리 고지`,
			},
			ContentMDI18n: map[string]string{
				"zh": `# V-Claw 隐私与数据处理说明

最后更新：2026-06-20

本说明适用于 V-Claw 在提供共享 AI API 访问、内部用量额度、账号管理、计费、支持和安全控制过程中对数据的处理。

## 一、我们可能处理的数据

根据您使用的功能，V-Claw 可能处理以下数据：

- 账号信息：邮箱、用户名、头像、角色、登录记录、API Key 标识、余额和使用记录；
- 付款与订单信息：交易编号、支付状态、套餐、订单备注、发票/收据所需信息；
- 服务数据：请求时间、路由、模型、token 统计、错误码、限制事件、审计日志和安全事件；
- 内容数据：您提交的 prompt、文件、附件、上下文、系统消息、输出结果、保存的模板或配置；
- 支持通信：工单、反馈、投诉、聊天记录和您主动提供给支持团队的材料。

## 二、处理目的

我们处理上述数据用于：

- 提供、维护和改进服务；
- 执行账号管理、认证、计费、额度控制和对账；
- 路由请求到上游模型提供商并返回结果；
- 进行故障排查、风控、滥用检测、审计日志分析和安全防护；
- 处理支持请求、投诉、争议和法律合规事项；
- 履行法律义务、监管要求或强制性请求。

## 三、用户的法律基础与责任

如果您向 V-Claw 提交个人数据或其他受保护数据，您应确保自己拥有适用的法律基础、通知和必要同意，并且您已满足所有适用的数据保护义务。

除非法律另有要求，V-Claw 不判断您是否拥有处理某一特定数据集的合法依据。您应避免提交不必要的敏感数据或高风险数据；如确有必要，请先进行最小化、匿名化/假名化、脱敏和访问限制。

## 四、与上游和第三方共享 / 国际传输

为提供服务，您的请求、提示词、附件、上下文或相关元数据可能会被发送给上游 AI 提供商、云基础设施提供商、支付处理方、消息/邮件系统或其他服务商。上述接收方可能位于您所在司法辖区之外。

这些接收方对其处理可能适用各自的政策、条款和跨境传输规则。V-Claw 会在合理范围内选择服务商并采取必要的技术与组织措施，但无法保证所有第三方处理都完全在同一司法辖区内完成。

## 五、保留期限

我们不会无限期保存所有数据。不同数据会根据业务需要、法律义务、风控需求、争议处理、账务要求和备份策略，在不同期限内保留。

例如，账号和账务记录可能需要更长时间保存；请求日志、审计记录、缓存或临时处理数据通常只在实现服务、排障或安全目的所需的期间内保留。具体期限可能因配置、法律要求或运营需要而变化。

## 六、安全措施

我们采用合理的管理、技术和组织措施来保护数据，包括访问控制、权限最小化、加密、密钥管理、审计、速率限制、异常检测和安全隔离。

但任何系统都无法保证绝对安全。若发生安全事件，V-Claw 可在必要时暂停部分功能、限制访问、轮换密钥或采取其他响应措施。

## 七、敏感或高风险数据

除非您有明确的法律基础和适当的安全措施，否则不要提交敏感个人数据、儿童/未成年人数据、健康数据、财务身份数据、政府识别信息、客户机密或可能造成重大损害的高风险信息。

## 八、权利、请求与投诉

在适用法律允许的范围内，您可以就您的数据提出访问、更正、删除、限制处理或其他权利请求。若您希望提出隐私相关投诉或询问，请通过平台官方支持渠道联系 V-Claw。

## 九、更新

我们可能不时更新本说明，以反映法律、产品或安全要求的变化。`,
				"en": `# V-Claw Privacy & Data Processing Notice

Last updated: 2026-06-20

This Notice describes how V-Claw may process data when providing shared AI API access, internal usage credits, account management, billing, support, and security controls.

## 1. Data We May Process

Depending on the features you use, V-Claw may process:

- Account data: email address, username, avatar, role, sign-in records, API key identifiers, balance, and usage records;
- Billing and order data: transaction identifiers, payment status, package selection, and order notes or receipt-related information;
- Service data: request timestamps, routes, models, token counts, error codes, throttling events, audit logs, and security events;
- Content data: prompts, files, attachments, context, system messages, outputs, saved templates, or configuration you submit;
- Support communications: tickets, feedback, complaints, chat history, and materials you provide to support staff.

## 2. Purposes of Processing

We process the above data to:

- provide, maintain, and improve the Service;
- operate account management, authentication, billing, quota control, and reconciliation;
- route requests to upstream model providers and return results;
- troubleshoot issues, perform risk control, detect abuse, audit activity, and protect security;
- handle support requests, complaints, disputes, and legal/compliance matters;
- comply with legal obligations, regulatory requirements, or compulsory requests.

## 3. User Legal Basis and Responsibility

If you submit personal data or other protected data to V-Claw, you are responsible for ensuring that you have the applicable legal basis, notice, and any required consent, and that you have satisfied any other applicable data-protection obligations.

Unless required by law, V-Claw does not decide whether you have a lawful basis to process a particular dataset. You should avoid submitting unnecessary sensitive or high-risk data. If such data is necessary, you should first apply minimization, anonymization/pseudonymization, redaction, and access restrictions.

## 4. Sharing with Upstream Providers and International Transfers

To provide the Service, your requests, prompts, attachments, context, or related metadata may be sent to upstream AI providers, cloud infrastructure providers, payment processors, messaging/mail systems, or other service providers. Those recipients may be located outside your jurisdiction.

Those recipients may process the data under their own policies, terms, and cross-border transfer rules. V-Claw will use reasonable care in selecting vendors and will apply appropriate technical and organizational measures where feasible, but we cannot guarantee that all third-party processing occurs inside a single jurisdiction.

## 5. Retention

We do not keep all data indefinitely. Different data types are retained for different periods depending on business need, legal obligations, risk control, dispute handling, accounting requirements, and backup policies.

For example, account and billing records may need longer retention, while request logs, audit records, caches, or temporary processing data are generally kept only as long as needed for service delivery, troubleshooting, or security purposes. Specific periods may vary by configuration, law, or operational need.

## 6. Security Measures

We use reasonable administrative, technical, and organizational measures to protect data, including access controls, least-privilege permissions, encryption, key management, auditing, rate limiting, anomaly detection, and security isolation.

No system can guarantee absolute security. If a security incident occurs, V-Claw may temporarily suspend some functions, restrict access, rotate keys, or take other response measures.

## 7. Sensitive or High-Risk Data

Unless you have a clear legal basis and appropriate safeguards, do not submit sensitive personal data, children's data, health data, financial identity data, government identifiers, customer secrets, or other high-risk information that could cause significant harm.

## 8. Rights, Requests, and Complaints

Where allowed by applicable law, you may request access, correction, deletion, restriction of processing, or other rights relating to your data. If you have a privacy-related question or complaint, please contact V-Claw through the platform's official support channel.

## 9. Updates

We may update this Notice from time to time to reflect legal, product, or security changes.`,
				"vi": `# Thông báo Quyền riêng tư & Xử lý dữ liệu của V-Claw

Cập nhật lần cuối: 2026-06-20

Thông báo này mô tả cách V-Claw có thể xử lý dữ liệu khi cung cấp quyền truy cập AI API dùng chung, credit sử dụng nội bộ, quản lý tài khoản, thanh toán, hỗ trợ và kiểm soát an ninh.

## 1. Dữ liệu có thể được xử lý

Tùy theo tính năng bạn sử dụng, V-Claw có thể xử lý:

- Dữ liệu tài khoản: email, tên người dùng, ảnh đại diện, vai trò, lịch sử đăng nhập, định danh API key, số dư và lịch sử sử dụng;
- Dữ liệu thanh toán và đơn hàng: mã giao dịch, trạng thái thanh toán, gói dịch vụ, ghi chú đơn hàng hoặc thông tin cần cho biên nhận/hóa đơn;
- Dữ liệu dịch vụ: thời điểm request, route, model, thống kê token, mã lỗi, sự kiện giới hạn tốc độ, log kiểm tra, log an ninh;
- Dữ liệu nội dung: prompt, tệp, đính kèm, ngữ cảnh, system message, đầu ra, mẫu lưu sẵn hoặc cấu hình mà bạn gửi;
- Trao đổi hỗ trợ: ticket, phản hồi, khiếu nại, lịch sử chat và tài liệu bạn chủ động cung cấp cho bộ phận hỗ trợ.

## 2. Mục đích xử lý

Chúng tôi xử lý dữ liệu nêu trên để:

- cung cấp, duy trì và cải thiện Dịch vụ;
- vận hành quản lý tài khoản, xác thực, tính phí, kiểm soát quota và đối soát;
- chuyển request tới nhà cung cấp mô hình thượng nguồn và trả kết quả về;
- xử lý sự cố, quản trị rủi ro, phát hiện lạm dụng, ghi log, audit và bảo vệ an ninh;
- xử lý yêu cầu hỗ trợ, khiếu nại, tranh chấp và các vấn đề pháp lý/tuân thủ;
- tuân thủ nghĩa vụ pháp lý, yêu cầu của cơ quan có thẩm quyền hoặc thủ tục bắt buộc.

## 3. Cơ sở pháp lý và trách nhiệm của người dùng

Nếu bạn gửi dữ liệu cá nhân hoặc dữ liệu được bảo vệ khác cho V-Claw, bạn phải tự bảo đảm rằng mình có cơ sở pháp lý, thông báo và sự đồng ý cần thiết, đồng thời đã đáp ứng mọi nghĩa vụ bảo vệ dữ liệu áp dụng.

Trừ khi pháp luật yêu cầu khác, V-Claw không quyết định thay bạn liệu bạn có cơ sở pháp lý hợp lệ để xử lý một bộ dữ liệu cụ thể hay không. Bạn nên tránh gửi dữ liệu nhạy cảm hoặc dữ liệu rủi ro không cần thiết; nếu thật sự cần, hãy áp dụng tối thiểu hóa, ẩn danh/giả danh, che bớt thông tin và giới hạn truy cập.

## 4. Chia sẻ với nhà cung cấp thượng nguồn và chuyển dữ liệu xuyên biên giới

Để cung cấp Dịch vụ, request, prompt, đính kèm, ngữ cảnh hoặc metadata liên quan có thể được gửi tới nhà cung cấp AI thượng nguồn, nhà cung cấp hạ tầng đám mây, đơn vị xử lý thanh toán, hệ thống nhắn tin/email hoặc nhà cung cấp dịch vụ khác. Các bên nhận này có thể đặt ngoài khu vực pháp lý của bạn.

Các bên nhận đó có thể xử lý dữ liệu theo chính sách, điều khoản và quy tắc chuyển dữ liệu xuyên biên giới của họ. V-Claw sẽ lựa chọn nhà cung cấp một cách thận trọng và áp dụng các biện pháp kỹ thuật/tổ chức phù hợp trong phạm vi khả thi, nhưng không thể bảo đảm mọi xử lý của bên thứ ba đều diễn ra trong cùng một khu vực pháp lý.

## 5. Thời hạn lưu giữ

Chúng tôi không lưu giữ tất cả dữ liệu vô thời hạn. Mỗi loại dữ liệu được giữ trong thời gian khác nhau tùy theo nhu cầu kinh doanh, nghĩa vụ pháp lý, kiểm soát rủi ro, xử lý tranh chấp, yêu cầu kế toán và chính sách sao lưu.

Ví dụ, dữ liệu tài khoản và thanh toán có thể cần lưu lâu hơn, trong khi log request, bản ghi kiểm toán, cache hoặc dữ liệu xử lý tạm thời thường chỉ được giữ trong khoảng thời gian cần thiết cho việc cung cấp dịch vụ, khắc phục sự cố hoặc bảo vệ an ninh. Thời hạn cụ thể có thể thay đổi theo cấu hình, luật áp dụng hoặc nhu cầu vận hành.

## 6. Biện pháp bảo mật

Chúng tôi áp dụng các biện pháp quản trị, kỹ thuật và tổ chức hợp lý để bảo vệ dữ liệu, bao gồm kiểm soát truy cập, phân quyền tối thiểu, mã hóa, quản lý khóa, audit, rate limit, phát hiện bất thường và cô lập an ninh.

Không hệ thống nào có thể bảo đảm an toàn tuyệt đối. Nếu xảy ra sự cố an ninh, V-Claw có thể tạm dừng một phần chức năng, hạn chế truy cập, xoay/đổi key hoặc thực hiện biện pháp ứng phó khác.

## 7. Dữ liệu nhạy cảm hoặc rủi ro cao

Trừ khi bạn có cơ sở pháp lý rõ ràng và biện pháp bảo vệ phù hợp, đừng gửi dữ liệu cá nhân nhạy cảm, dữ liệu trẻ em/vị thành niên, dữ liệu sức khỏe, dữ liệu định danh tài chính, mã định danh nhà nước, bí mật khách hàng hoặc thông tin rủi ro cao có thể gây thiệt hại đáng kể.

## 8. Quyền, yêu cầu và khiếu nại

Trong phạm vi pháp luật cho phép, bạn có thể yêu cầu truy cập, chỉnh sửa, xóa, hạn chế xử lý hoặc các quyền khác liên quan đến dữ liệu của mình. Nếu có câu hỏi hoặc khiếu nại về quyền riêng tư, vui lòng liên hệ V-Claw qua kênh hỗ trợ chính thức của nền tảng.

## 9. Cập nhật

Chúng tôi có thể cập nhật Thông báo này theo thời gian để phản ánh thay đổi pháp lý, sản phẩm hoặc bảo mật.`,
				"ko": `# V-Claw 개인정보 보호 및 데이터 처리 고지

최종 업데이트: 2026-06-20

이 고지는 공유 AI API 접근, 내부 사용 크레딧, 계정 관리, 결제, 지원 및 보안 통제를 제공하는 과정에서 V-Claw가 데이터를 어떻게 처리할 수 있는지 설명합니다.

## 1. 처리할 수 있는 데이터

사용 기능에 따라 V-Claw는 다음 데이터를 처리할 수 있습니다.

- 계정 데이터: 이메일, 사용자명, 아바타, 역할, 로그인 기록, API Key 식별자, 잔액 및 사용 기록;
- 결제 및 주문 데이터: 거래 식별자, 결제 상태, 패키지 선택, 주문 메모 또는 영수증 관련 정보;
- 서비스 데이터: 요청 시각, 라우트, 모델, 토큰 수, 오류 코드, 제한 이벤트, 감사 로그, 보안 이벤트;
- 콘텐츠 데이터: 사용자가 제출하는 프롬프트, 파일, 첨부, 문맥, 시스템 메시지, 출력, 저장된 템플릿 또는 설정;
- 지원 커뮤니케이션: 티켓, 피드백, 민원, 채팅 기록 및 지원팀에 제공한 자료.

## 2. 처리 목적

우리는 위 데이터를 다음 목적으로 처리합니다.

- 서비스 제공, 유지 및 개선;
- 계정 관리, 인증, 과금, 할당량 제어 및 정산 운영;
- 요청을 상위 모델 제공업체로 전달하고 결과 반환;
- 장애 분석, 리스크 통제, 남용 탐지, 감사, 보안 보호;
- 지원 요청, 민원, 분쟁 및 법적/준수 사안 처리;
- 법적 의무, 규제 요구 또는 강제 요청 준수.

## 3. 사용자 법적 근거와 책임

개인 데이터 또는 기타 보호된 데이터를 V-Claw에 제출하는 경우, 해당 데이터 처리에 필요한 법적 근거, 고지 및 필요한 동의를 보유하고 있으며, 필요한 경우 위험 평가 및 내부 승인을 완료했는지 사용자가 직접 책임져야 합니다.

법률이 요구하지 않는 한, V-Claw는 특정 데이터셋을 처리할 적법한 근거가 있는지 판단하지 않습니다. 불필요한 민감 데이터나 고위험 데이터는 제출하지 않는 것이 좋습니다. 필요한 경우 최소화, 익명화/가명화, 마스킹 및 접근 제한을 먼저 적용해야 합니다.

## 4. 상위 제공업체와의 공유 / 국제 전송

서비스 제공을 위해 요청, 프롬프트, 첨부, 문맥 또는 관련 메타데이터가 상위 AI 제공업체, 클라우드 인프라 제공업체, 결제 처리업체, 메시징/메일 시스템 또는 기타 서비스 제공업체로 전송될 수 있습니다. 이러한 수신자는 사용자의 관할권 밖에 있을 수 있습니다.

수신자는 자체 정책, 약관 및 국경 간 전송 규칙에 따라 데이터를 처리할 수 있습니다. V-Claw는 공급업체를 합리적으로 선정하고 가능한 범위에서 적절한 기술적·조직적 조치를 적용하지만, 모든 제3자 처리가 단일 관할권 내에서만 이루어진다고 보장할 수는 없습니다.

## 5. 보관 기간

우리는 모든 데이터를 무기한 보관하지 않습니다. 각 데이터 유형은 비즈니스 필요, 법적 의무, 리스크 통제, 분쟁 처리, 회계 요구 및 백업 정책에 따라 서로 다른 기간 동안 보관됩니다.

예를 들어 계정 및 결제 기록은 더 오래 보관될 수 있고, 요청 로그, 감사 기록, 캐시 또는 임시 처리 데이터는 일반적으로 서비스 제공, 문제 해결 또는 보안 목적에 필요한 기간 동안만 보관됩니다. 구체적인 기간은 설정, 법률 또는 운영 필요에 따라 달라질 수 있습니다.

## 6. 보안 조치

우리는 접근 통제, 최소 권한, 암호화, 키 관리, 감사, 속도 제한, 이상 탐지 및 보안 격리를 포함한 합리적인 관리적·기술적·조직적 조치를 사용하여 데이터를 보호합니다.

어떤 시스템도 절대적인 보안을 보장할 수는 없습니다. 보안 사고가 발생하면 V-Claw는 일부 기능을 일시 중지하거나, 접근을 제한하거나, 키를 교체하는 등 대응 조치를 취할 수 있습니다.

## 7. 민감하거나 고위험한 데이터

명확한 법적 근거와 적절한 보호장치가 없다면 민감한 개인정보, 아동/미성년자 데이터, 건강 데이터, 금융 식별 데이터, 정부 식별자, 고객 비밀 또는 중대한 피해를 초래할 수 있는 고위험 정보는 제출하지 마십시오.

## 8. 권리, 요청 및 민원

적용 법률이 허용하는 범위에서 사용자는 자신의 데이터에 대한 접근, 정정, 삭제, 처리 제한 또는 기타 권리를 요청할 수 있습니다. 개인정보 관련 질문이나 민원이 있으면 플랫폼의 공식 지원 채널을 통해 V-Claw에 문의하십시오.

## 9. 업데이트

우리는 법률, 제품 또는 보안 변경을 반영하기 위해 이 고지를 수시로 업데이트할 수 있습니다.`,
			},
		},
	}
}
func normalizeLoginAgreementDocumentID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastSeparator := false
	for _, r := range raw {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			_, _ = b.WriteRune(r)
			lastSeparator = false
			continue
		}
		if r == '-' || r == '_' || r == ' ' || r == '.' || r == '/' {
			if !lastSeparator && b.Len() > 0 {
				if r == '_' {
					_, _ = b.WriteRune('_')
				} else {
					_, _ = b.WriteRune('-')
				}
				lastSeparator = true
			}
		}
	}
	return strings.Trim(b.String(), "-_")
}

func normalizeLoginAgreementLocale(raw string) string {
	locale := strings.ToLower(strings.TrimSpace(raw))
	if idx := strings.Index(locale, "-"); idx >= 0 {
		locale = locale[:idx]
	}
	switch locale {
	case "zh", "en", "vi", "ko":
		return locale
	default:
		return ""
	}
}

func normalizeLoginAgreementLocalizedMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(values))
	for key, value := range values {
		locale := normalizeLoginAgreementLocale(key)
		if locale == "" {
			continue
		}
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			normalized[locale] = trimmed
		}
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func cloneLoginAgreementLocalizedMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneLoginAgreementDocument(doc LoginAgreementDocument) LoginAgreementDocument {
	doc.TitleI18n = cloneLoginAgreementLocalizedMap(doc.TitleI18n)
	doc.ContentMDI18n = cloneLoginAgreementLocalizedMap(doc.ContentMDI18n)
	return doc
}

// DefaultLoginAgreementDocuments returns the built-in legal documents used when
// an installation has not stored customized agreement documents yet.
func DefaultLoginAgreementDocuments() []LoginAgreementDocument {
	defaults := defaultLoginAgreementDocuments()
	cloned := make([]LoginAgreementDocument, 0, len(defaults))
	for _, doc := range defaults {
		cloned = append(cloned, cloneLoginAgreementDocument(doc))
	}
	return cloned
}

func defaultLoginAgreementDocumentByID(id string) (LoginAgreementDocument, bool) {
	for _, doc := range defaultLoginAgreementDocuments() {
		if normalizeLoginAgreementDocumentID(doc.ID) == id {
			return cloneLoginAgreementDocument(doc), true
		}
	}
	return LoginAgreementDocument{}, false
}

func mergeLoginAgreementLocalizedDefaults(current, defaults map[string]string) map[string]string {
	merged := cloneLoginAgreementLocalizedMap(current)
	if len(defaults) == 0 {
		return merged
	}
	if merged == nil {
		merged = make(map[string]string, len(defaults))
	}
	for _, locale := range []string{"zh", "en", "vi", "ko"} {
		if strings.TrimSpace(merged[locale]) == "" {
			if value := strings.TrimSpace(defaults[locale]); value != "" {
				merged[locale] = value
			}
		}
	}
	if len(merged) == 0 {
		return nil
	}
	return merged
}

func firstLoginAgreementLocalizedValue(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	for _, locale := range []string{"zh", "en", "vi", "ko"} {
		if value := strings.TrimSpace(values[locale]); value != "" {
			return value
		}
	}
	return ""
}

func normalizeLoginAgreementDocuments(docs []LoginAgreementDocument) []LoginAgreementDocument {
	normalized := make([]LoginAgreementDocument, 0, len(docs))
	seen := make(map[string]int, len(docs))
	for i, doc := range docs {
		titleI18n := normalizeLoginAgreementLocalizedMap(doc.TitleI18n)
		contentI18n := normalizeLoginAgreementLocalizedMap(doc.ContentMDI18n)
		title := strings.TrimSpace(doc.Title)
		content := strings.TrimSpace(doc.ContentMD)
		if title == "" {
			title = firstLoginAgreementLocalizedValue(titleI18n)
		}
		if content == "" {
			content = firstLoginAgreementLocalizedValue(contentI18n)
		}
		id := normalizeLoginAgreementDocumentID(doc.ID)
		if id == "" {
			if title == "" && content == "" {
				continue
			}
			sum := sha256.Sum256([]byte(fmt.Sprintf("%d:%s:%s", i, title, content)))
			id = hex.EncodeToString(sum[:])[:12]
		}
		if defaults, ok := defaultLoginAgreementDocumentByID(id); ok {
			if title == "" {
				title = defaults.Title
			}
			if content == "" {
				content = defaults.ContentMD
			}
			titleI18n = mergeLoginAgreementLocalizedDefaults(titleI18n, defaults.TitleI18n)
			contentI18n = mergeLoginAgreementLocalizedDefaults(contentI18n, defaults.ContentMDI18n)
		}
		if title == "" && content == "" {
			continue
		}
		baseID := id
		for suffix := 2; seen[id] > 0; suffix++ {
			id = fmt.Sprintf("%s-%d", baseID, suffix)
		}
		seen[id]++
		normalized = append(normalized, LoginAgreementDocument{
			ID:            id,
			Title:         title,
			ContentMD:     content,
			TitleI18n:     titleI18n,
			ContentMDI18n: contentI18n,
		})
	}
	return normalized
}

func parseLoginAgreementDocuments(raw string) []LoginAgreementDocument {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLoginAgreementDocuments()
	}
	var docs []LoginAgreementDocument
	if err := json.Unmarshal([]byte(raw), &docs); err != nil {
		return defaultLoginAgreementDocuments()
	}
	docs = normalizeLoginAgreementDocuments(docs)
	if len(docs) == 0 {
		return defaultLoginAgreementDocuments()
	}
	return docs
}

func marshalLoginAgreementDocuments(docs []LoginAgreementDocument) (string, error) {
	normalized := normalizeLoginAgreementDocuments(docs)
	if len(normalized) == 0 {
		normalized = defaultLoginAgreementDocuments()
	}
	b, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("marshal login agreement documents: %w", err)
	}
	return string(b), nil
}

func buildLoginAgreementRevision(updatedAt string, docs []LoginAgreementDocument) string {
	normalized := normalizeLoginAgreementDocuments(docs)
	payload, err := json.Marshal(struct {
		UpdatedAt string                   `json:"updated_at"`
		Documents []LoginAgreementDocument `json:"documents"`
	}{
		UpdatedAt: strings.TrimSpace(updatedAt),
		Documents: normalized,
	})
	if err != nil {
		payload = []byte(strings.TrimSpace(updatedAt))
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])[:16]
}

func normalizeWeChatConnectModeSetting(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "mp":
		return "mp"
	case "mobile":
		return "mobile"
	default:
		return "open"
	}
}

func defaultWeChatConnectScopeForMode(mode string) string {
	switch normalizeWeChatConnectModeSetting(mode) {
	case "mp":
		return "snsapi_userinfo"
	case "mobile":
		return ""
	}
	return defaultWeChatConnectScopes
}

func normalizeWeChatConnectScopeSetting(raw, mode string) string {
	switch normalizeWeChatConnectModeSetting(mode) {
	case "mp":
		switch strings.TrimSpace(raw) {
		case "snsapi_base":
			return "snsapi_base"
		case "snsapi_userinfo":
			return "snsapi_userinfo"
		default:
			return defaultWeChatConnectScopeForMode(mode)
		}
	case "mobile":
		return ""
	default:
		return defaultWeChatConnectScopes
	}
}

func parseWeChatConnectCapabilitySettings(settings map[string]string, enabled bool, mode string) (bool, bool, bool) {
	mode = normalizeWeChatConnectModeSetting(mode)
	rawOpen, hasOpen := settings[SettingKeyWeChatConnectOpenEnabled]
	rawMP, hasMP := settings[SettingKeyWeChatConnectMPEnabled]
	rawMobile, hasMobile := settings[SettingKeyWeChatConnectMobileEnabled]
	openConfigured := hasOpen && strings.TrimSpace(rawOpen) != ""
	mpConfigured := hasMP && strings.TrimSpace(rawMP) != ""
	mobileConfigured := hasMobile && strings.TrimSpace(rawMobile) != ""

	if openConfigured || mpConfigured || mobileConfigured {
		openEnabled := strings.TrimSpace(rawOpen) == "true"
		mpEnabled := strings.TrimSpace(rawMP) == "true"
		mobileEnabled := strings.TrimSpace(rawMobile) == "true"
		return openEnabled, mpEnabled, mobileEnabled
	}

	if !enabled {
		return false, false, false
	}
	if mode == "mp" {
		return false, true, false
	}
	if mode == "mobile" {
		return false, false, true
	}
	return true, false, false
}

func normalizeWeChatConnectStoredMode(openEnabled, mpEnabled, mobileEnabled bool, mode string) string {
	mode = normalizeWeChatConnectModeSetting(mode)
	switch mode {
	case "open":
		if openEnabled {
			return "open"
		}
	case "mp":
		if mpEnabled {
			return "mp"
		}
	case "mobile":
		if mobileEnabled {
			return "mobile"
		}
	}
	switch {
	case openEnabled:
		return "open"
	case mpEnabled:
		return "mp"
	case mobileEnabled:
		return "mobile"
	default:
		return mode
	}
}

func mergeWeChatConnectCapabilitySettings(settings map[string]string, base config.WeChatConnectConfig, enabled bool, mode string) (bool, bool, bool) {
	mode = normalizeWeChatConnectModeSetting(firstNonEmpty(mode, base.Mode))
	rawOpen, hasOpen := settings[SettingKeyWeChatConnectOpenEnabled]
	rawMP, hasMP := settings[SettingKeyWeChatConnectMPEnabled]
	rawMobile, hasMobile := settings[SettingKeyWeChatConnectMobileEnabled]
	openConfigured := hasOpen && strings.TrimSpace(rawOpen) != ""
	mpConfigured := hasMP && strings.TrimSpace(rawMP) != ""
	mobileConfigured := hasMobile && strings.TrimSpace(rawMobile) != ""

	if openConfigured || mpConfigured || mobileConfigured {
		openEnabled := strings.TrimSpace(rawOpen) == "true"
		mpEnabled := strings.TrimSpace(rawMP) == "true"
		mobileEnabled := strings.TrimSpace(rawMobile) == "true"
		_, enabledConfigured := settings[SettingKeyWeChatConnectEnabled]
		if !enabledConfigured &&
			enabled &&
			!openEnabled &&
			!mpEnabled &&
			!mobileEnabled &&
			(base.OpenEnabled || base.MPEnabled || base.MobileEnabled) {
			return base.OpenEnabled, base.MPEnabled, base.MobileEnabled
		}
		return openEnabled, mpEnabled, mobileEnabled
	}
	if !enabled {
		return false, false, false
	}
	if base.OpenEnabled || base.MPEnabled || base.MobileEnabled {
		return base.OpenEnabled, base.MPEnabled, base.MobileEnabled
	}
	return parseWeChatConnectCapabilitySettings(settings, enabled, mode)
}

func (s *SettingService) effectiveWeChatConnectOAuthConfig(settings map[string]string) WeChatConnectOAuthConfig {
	base := config.WeChatConnectConfig{}
	if s != nil && s.cfg != nil {
		base = s.cfg.WeChat
	}

	enabled := base.Enabled
	if raw, ok := settings[SettingKeyWeChatConnectEnabled]; ok {
		enabled = strings.TrimSpace(raw) == "true"
	}

	legacyAppID := strings.TrimSpace(firstNonEmpty(
		settings[SettingKeyWeChatConnectAppID],
		base.AppID,
		base.OpenAppID,
		base.MPAppID,
		base.MobileAppID,
	))
	legacyAppSecret := strings.TrimSpace(firstNonEmpty(
		settings[SettingKeyWeChatConnectAppSecret],
		base.AppSecret,
		base.OpenAppSecret,
		base.MPAppSecret,
		base.MobileAppSecret,
	))
	openAppID := strings.TrimSpace(firstNonEmpty(settings[SettingKeyWeChatConnectOpenAppID], base.OpenAppID, legacyAppID))
	openAppSecret := strings.TrimSpace(firstNonEmpty(settings[SettingKeyWeChatConnectOpenAppSecret], base.OpenAppSecret, legacyAppSecret))
	mpAppID := strings.TrimSpace(firstNonEmpty(settings[SettingKeyWeChatConnectMPAppID], base.MPAppID, legacyAppID))
	mpAppSecret := strings.TrimSpace(firstNonEmpty(settings[SettingKeyWeChatConnectMPAppSecret], base.MPAppSecret, legacyAppSecret))
	mobileAppID := strings.TrimSpace(firstNonEmpty(settings[SettingKeyWeChatConnectMobileAppID], base.MobileAppID, legacyAppID))
	mobileAppSecret := strings.TrimSpace(firstNonEmpty(settings[SettingKeyWeChatConnectMobileAppSecret], base.MobileAppSecret, legacyAppSecret))

	modeRaw := firstNonEmpty(settings[SettingKeyWeChatConnectMode], base.Mode)
	openEnabled, mpEnabled, mobileEnabled := mergeWeChatConnectCapabilitySettings(settings, base, enabled, modeRaw)
	mode := normalizeWeChatConnectStoredMode(openEnabled, mpEnabled, mobileEnabled, modeRaw)

	return WeChatConnectOAuthConfig{
		Enabled:             enabled,
		LegacyAppID:         legacyAppID,
		LegacyAppSecret:     legacyAppSecret,
		OpenAppID:           openAppID,
		OpenAppSecret:       openAppSecret,
		MPAppID:             mpAppID,
		MPAppSecret:         mpAppSecret,
		MobileAppID:         mobileAppID,
		MobileAppSecret:     mobileAppSecret,
		OpenEnabled:         openEnabled,
		MPEnabled:           mpEnabled,
		MobileEnabled:       mobileEnabled,
		Mode:                mode,
		Scopes:              normalizeWeChatConnectScopeSetting(firstNonEmpty(settings[SettingKeyWeChatConnectScopes], base.Scopes), mode),
		RedirectURL:         strings.TrimSpace(firstNonEmpty(settings[SettingKeyWeChatConnectRedirectURL], base.RedirectURL)),
		FrontendRedirectURL: strings.TrimSpace(firstNonEmpty(settings[SettingKeyWeChatConnectFrontendRedirectURL], base.FrontendRedirectURL, defaultWeChatConnectFrontend)),
	}
}

// NewSettingService 创建系统设置服务实例
func NewSettingService(settingRepo SettingRepository, cfg *config.Config) *SettingService {
	service := &SettingService{
		settingRepo: settingRepo,
		cfg:         cfg,
	}
	antigravity.SetUserAgentVersionResolver(service.GetAntigravityUserAgentVersion)
	return service
}

// SetDefaultSubscriptionGroupReader injects an optional group reader for default subscription validation.
func (s *SettingService) SetDefaultSubscriptionGroupReader(reader DefaultSubscriptionGroupReader) {
	s.defaultSubGroupReader = reader
}

// SetProxyRepository injects a proxy repo for resolving websearch provider proxy URLs.
func (s *SettingService) SetProxyRepository(repo ProxyRepository) {
	s.proxyRepo = repo
}

func (s *SettingService) LoadAPIKeyACLTrustForwardedIPSetting(ctx context.Context) error {
	if s == nil || s.cfg == nil || s.settingRepo == nil {
		return nil
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAPIKeyACLTrustForwardedIP)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			s.cfg.SetTrustForwardedIPForAPIKeyACL(s.cfg.Security.TrustForwardedIPForAPIKeyACL)
			return nil
		}
		return fmt.Errorf("get api key acl forwarded ip setting: %w", err)
	}
	enabled := value == "true"
	s.cfg.SetTrustForwardedIPForAPIKeyACL(enabled)
	return nil
}

// GetAllSettings 获取所有系统设置
func (s *SettingService) GetAllSettings(ctx context.Context) (*SystemSettings, error) {
	settings, err := s.settingRepo.GetAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all settings: %w", err)
	}

	return s.parseSettings(settings), nil
}

// GetFrontendURL 获取前端基础URL（数据库优先，fallback 到配置文件）
func (s *SettingService) GetFrontendURL(ctx context.Context) string {
	val, err := s.settingRepo.GetValue(ctx, SettingKeyFrontendURL)
	if err == nil && strings.TrimSpace(val) != "" {
		return strings.TrimSpace(val)
	}
	return s.cfg.Server.FrontendURL
}

// GetCyberSessionBlockRuntime 返回 (开关, TTL)，进程内缓存 ~60s，
// 供网关热路径读取时避免 DB 往返。
// 两个 setting key 在单次 singleflight 里一起读取，减少 DB 往返。
// 默认值：开关 false，TTL 1h（与粘性会话对齐）。
func (s *SettingService) GetCyberSessionBlockRuntime(ctx context.Context) (bool, time.Duration) {
	if cached, ok := s.cyberSessionBlockRuntimeCache.Load().(*cachedCyberSessionBlockRuntime); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.enabled, cached.ttl
		}
	}
	result, _, _ := s.cyberSessionBlockRuntimeSF.Do("cyber_session_block_runtime", func() (any, error) {
		if cached, ok := s.cyberSessionBlockRuntimeCache.Load().(*cachedCyberSessionBlockRuntime); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached, nil
			}
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cyberSessionBlockRuntimeDBTimeout)
		defer cancel()

		enabledVal, enabledErr := s.settingRepo.GetValue(dbCtx, SettingKeyCyberSessionBlockEnabled)
		ttlVal, ttlErr := s.settingRepo.GetValue(dbCtx, SettingKeyCyberSessionBlockTTLSeconds)

		if enabledErr != nil && !errors.Is(enabledErr, ErrSettingNotFound) {
			slog.Warn("failed to get cyber_session_block_enabled setting", "error", enabledErr)
			entry := &cachedCyberSessionBlockRuntime{
				enabled:   false,
				ttl:       time.Hour,
				expiresAt: time.Now().Add(cyberSessionBlockRuntimeErrorTTL).UnixNano(),
			}
			s.cyberSessionBlockRuntimeCache.Store(entry)
			return entry, nil
		}

		enabled := enabledErr == nil && strings.TrimSpace(enabledVal) == "true"

		ttl := time.Hour
		if ttlErr == nil {
			if n, perr := strconv.Atoi(strings.TrimSpace(ttlVal)); perr == nil && n > 0 {
				ttl = time.Duration(n) * time.Second
			}
		}

		entry := &cachedCyberSessionBlockRuntime{
			enabled:   enabled,
			ttl:       ttl,
			expiresAt: time.Now().Add(cyberSessionBlockRuntimeCacheTTL).UnixNano(),
		}
		s.cyberSessionBlockRuntimeCache.Store(entry)
		return entry, nil
	})
	if entry, ok := result.(*cachedCyberSessionBlockRuntime); ok && entry != nil {
		return entry.enabled, entry.ttl
	}
	return false, time.Hour
}

// GetPublicSettings 获取公开设置（无需登录）
func (s *SettingService) GetPublicSettings(ctx context.Context) (*PublicSettings, error) {
	keys := []string{
		SettingKeyRegistrationEnabled,
		SettingKeyEmailVerifyEnabled,
		SettingKeyForceEmailOnThirdPartySignup,
		SettingKeyRegistrationEmailSuffixWhitelist,
		SettingKeyPromoCodeEnabled,
		SettingKeyPasswordResetEnabled,
		SettingKeyInvitationCodeEnabled,
		SettingKeyTotpEnabled,
		SettingKeyLoginAgreementEnabled,
		SettingKeyLoginAgreementMode,
		SettingKeyLoginAgreementUpdatedAt,
		SettingKeyLoginAgreementDocuments,
		SettingKeyTurnstileEnabled,
		SettingKeyTurnstileSiteKey,
		SettingKeyAPIKeyACLTrustForwardedIP,
		SettingKeySiteName,
		SettingKeySiteLogo,
		SettingKeySiteSubtitle,
		SettingKeyAPIBaseURL,
		SettingKeyContactInfo,
		SettingKeyDocURL,
		SettingKeyHomeContent,
		SettingKeyHideCcsImportButton,
		SettingKeyPurchaseSubscriptionEnabled,
		SettingKeyPurchaseSubscriptionURL,
		SettingKeyTableDefaultPageSize,
		SettingKeyTablePageSizeOptions,
		SettingKeyCustomMenuItems,
		SettingKeyCustomEndpoints,
		SettingKeyLinuxDoConnectEnabled,
		SettingKeyDingTalkConnectEnabled,
		SettingKeyWeChatConnectEnabled,
		SettingKeyWeChatConnectAppID,
		SettingKeyWeChatConnectAppSecret,
		SettingKeyWeChatConnectOpenAppID,
		SettingKeyWeChatConnectOpenAppSecret,
		SettingKeyWeChatConnectMPAppID,
		SettingKeyWeChatConnectMPAppSecret,
		SettingKeyWeChatConnectMobileAppID,
		SettingKeyWeChatConnectMobileAppSecret,
		SettingKeyWeChatConnectOpenEnabled,
		SettingKeyWeChatConnectMPEnabled,
		SettingKeyWeChatConnectMobileEnabled,
		SettingKeyWeChatConnectMode,
		SettingKeyWeChatConnectScopes,
		SettingKeyWeChatConnectRedirectURL,
		SettingKeyWeChatConnectFrontendRedirectURL,
		SettingKeyBackendModeEnabled,
		SettingPaymentEnabled,
		SettingKeyOIDCConnectEnabled,
		SettingKeyOIDCConnectProviderName,
		SettingKeyGitHubOAuthEnabled,
		SettingKeyGitHubOAuthClientID,
		SettingKeyGitHubOAuthClientSecret,
		SettingKeyGoogleOAuthEnabled,
		SettingKeyGoogleOAuthClientID,
		SettingKeyGoogleOAuthClientSecret,
		SettingKeyBalanceLowNotifyEnabled,
		SettingKeyBalanceLowNotifyThreshold,
		SettingKeyBalanceLowNotifyRechargeURL,
		SettingKeyAccountQuotaNotifyEnabled,
		SettingKeyChannelMonitorEnabled,
		SettingKeyChannelMonitorDefaultIntervalSeconds,
		SettingKeyAvailableChannelsEnabled,
		SettingKeyAffiliateEnabled,
		SettingKeyDeviceAutoActivationAffCodes,
		SettingKeyRiskControlEnabled,
		SettingKeyAllowUserViewErrorRequests,
	}

	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("get public settings: %w", err)
	}

	linuxDoEnabled := false
	if raw, ok := settings[SettingKeyLinuxDoConnectEnabled]; ok {
		linuxDoEnabled = raw == "true"
	} else {
		linuxDoEnabled = s.cfg != nil && s.cfg.LinuxDo.Enabled
	}
	dingTalkEnabled := false
	if raw, ok := settings[SettingKeyDingTalkConnectEnabled]; ok {
		dingTalkEnabled = raw == "true"
	} else {
		dingTalkEnabled = s.cfg != nil && s.cfg.DingTalk.Enabled
	}
	oidcEnabled := false
	if raw, ok := settings[SettingKeyOIDCConnectEnabled]; ok {
		oidcEnabled = raw == "true"
	} else {
		oidcEnabled = s.cfg != nil && s.cfg.OIDC.Enabled
	}
	oidcProviderName := strings.TrimSpace(settings[SettingKeyOIDCConnectProviderName])
	if oidcProviderName == "" && s.cfg != nil {
		oidcProviderName = strings.TrimSpace(s.cfg.OIDC.ProviderName)
	}
	if oidcProviderName == "" {
		oidcProviderName = "OIDC"
	}
	gitHubEnabled := s.emailOAuthPublicEnabled(settings, "github")
	googleEnabled := s.emailOAuthPublicEnabled(settings, "google")
	weChatEnabled, weChatOpenEnabled, weChatMPEnabled, weChatMobileEnabled := s.weChatOAuthCapabilitiesFromSettings(settings)

	// Password reset requires email verification to be enabled
	emailVerifyEnabled := settings[SettingKeyEmailVerifyEnabled] == "true"
	passwordResetEnabled := emailVerifyEnabled && settings[SettingKeyPasswordResetEnabled] == "true"
	registrationEmailSuffixWhitelist := ParseRegistrationEmailSuffixWhitelist(
		settings[SettingKeyRegistrationEmailSuffixWhitelist],
	)
	tableDefaultPageSize, tablePageSizeOptions := parseTablePreferences(
		settings[SettingKeyTableDefaultPageSize],
		settings[SettingKeyTablePageSizeOptions],
	)
	loginAgreementDocuments := parseLoginAgreementDocuments(settings[SettingKeyLoginAgreementDocuments])
	loginAgreementUpdatedAt := strings.TrimSpace(settings[SettingKeyLoginAgreementUpdatedAt])
	if loginAgreementUpdatedAt == "" {
		loginAgreementUpdatedAt = defaultLoginAgreementDate
	}

	var balanceLowNotifyThreshold float64
	if v, err := strconv.ParseFloat(settings[SettingKeyBalanceLowNotifyThreshold], 64); err == nil && v >= 0 {
		balanceLowNotifyThreshold = v
	}

	return &PublicSettings{
		RegistrationEnabled:              settings[SettingKeyRegistrationEnabled] == "true",
		EmailVerifyEnabled:               emailVerifyEnabled,
		ForceEmailOnThirdPartySignup:     settings[SettingKeyForceEmailOnThirdPartySignup] == "true",
		RegistrationEmailSuffixWhitelist: registrationEmailSuffixWhitelist,
		PromoCodeEnabled:                 settings[SettingKeyPromoCodeEnabled] != "false", // 默认启用
		PasswordResetEnabled:             passwordResetEnabled,
		InvitationCodeEnabled:            settings[SettingKeyInvitationCodeEnabled] == "true",
		TotpEnabled:                      settings[SettingKeyTotpEnabled] == "true",
		LoginAgreementEnabled:            settings[SettingKeyLoginAgreementEnabled] == "true" && len(loginAgreementDocuments) > 0,
		LoginAgreementMode:               normalizeLoginAgreementMode(settings[SettingKeyLoginAgreementMode]),
		LoginAgreementUpdatedAt:          loginAgreementUpdatedAt,
		LoginAgreementRevision:           buildLoginAgreementRevision(loginAgreementUpdatedAt, loginAgreementDocuments),
		LoginAgreementDocuments:          loginAgreementDocuments,
		TurnstileEnabled:                 settings[SettingKeyTurnstileEnabled] == "true",
		TurnstileSiteKey:                 settings[SettingKeyTurnstileSiteKey],
		SiteName:                         s.getStringOrDefault(settings, SettingKeySiteName, "Sub2API"),
		SiteLogo:                         settings[SettingKeySiteLogo],
		SiteSubtitle:                     s.getStringOrDefault(settings, SettingKeySiteSubtitle, "Subscription to API Conversion Platform"),
		APIBaseURL:                       settings[SettingKeyAPIBaseURL],
		ContactInfo:                      settings[SettingKeyContactInfo],
		DocURL:                           settings[SettingKeyDocURL],
		HomeContent:                      settings[SettingKeyHomeContent],
		HideCcsImportButton:              settings[SettingKeyHideCcsImportButton] == "true",
		PurchaseSubscriptionEnabled:      settings[SettingKeyPurchaseSubscriptionEnabled] == "true",
		PurchaseSubscriptionURL:          strings.TrimSpace(settings[SettingKeyPurchaseSubscriptionURL]),
		TableDefaultPageSize:             tableDefaultPageSize,
		TablePageSizeOptions:             tablePageSizeOptions,
		CustomMenuItems:                  settings[SettingKeyCustomMenuItems],
		CustomEndpoints:                  settings[SettingKeyCustomEndpoints],
		LinuxDoOAuthEnabled:              linuxDoEnabled,
		DingTalkOAuthEnabled:             dingTalkEnabled,
		WeChatOAuthEnabled:               weChatEnabled,
		WeChatOAuthOpenEnabled:           weChatOpenEnabled,
		WeChatOAuthMPEnabled:             weChatMPEnabled,
		WeChatOAuthMobileEnabled:         weChatMobileEnabled,
		BackendModeEnabled:               settings[SettingKeyBackendModeEnabled] == "true",
		PaymentEnabled:                   settings[SettingPaymentEnabled] == "true",
		OIDCOAuthEnabled:                 oidcEnabled,
		OIDCOAuthProviderName:            oidcProviderName,
		GitHubOAuthEnabled:               gitHubEnabled,
		GoogleOAuthEnabled:               googleEnabled,
		BalanceLowNotifyEnabled:          settings[SettingKeyBalanceLowNotifyEnabled] == "true",
		AccountQuotaNotifyEnabled:        settings[SettingKeyAccountQuotaNotifyEnabled] == "true",
		BalanceLowNotifyThreshold:        balanceLowNotifyThreshold,
		BalanceLowNotifyRechargeURL:      settings[SettingKeyBalanceLowNotifyRechargeURL],

		ChannelMonitorEnabled:                !isFalseSettingValue(settings[SettingKeyChannelMonitorEnabled]),
		ChannelMonitorDefaultIntervalSeconds: parseChannelMonitorInterval(settings[SettingKeyChannelMonitorDefaultIntervalSeconds]),

		AvailableChannelsEnabled: settings[SettingKeyAvailableChannelsEnabled] == "true",

		AffiliateEnabled:             settings[SettingKeyAffiliateEnabled] == "true",
		DeviceAutoActivationAffCodes: deviceAutoActivationAffCodesSetting(settings),

		RiskControlEnabled: settings[SettingKeyRiskControlEnabled] == "true",

		AllowUserViewErrorRequests: settings[SettingKeyAllowUserViewErrorRequests] == "true",
	}, nil
}

// channelMonitorIntervalMin / channelMonitorIntervalMax bound the default interval
// (mirrors the monitor-level constraint but lives here so setting_service stays decoupled).
const (
	channelMonitorIntervalMin      = 15
	channelMonitorIntervalMax      = 3600
	channelMonitorIntervalFallback = 60
)

// parseChannelMonitorInterval parses the stored string and clamps to [15, 3600].
// Empty / invalid input falls back to channelMonitorIntervalFallback.
func parseChannelMonitorInterval(raw string) int {
	v, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return channelMonitorIntervalFallback
	}
	return clampChannelMonitorInterval(v)
}

// clampChannelMonitorInterval clamps v to the allowed range. 0 means "not provided".
func clampChannelMonitorInterval(v int) int {
	if v <= 0 {
		return 0
	}
	if v < channelMonitorIntervalMin {
		return channelMonitorIntervalMin
	}
	if v > channelMonitorIntervalMax {
		return channelMonitorIntervalMax
	}
	return v
}

// ChannelMonitorRuntime is the lightweight view of the channel monitor feature
// consumed by the runner and user-facing handlers.
type ChannelMonitorRuntime struct {
	Enabled                bool
	DefaultIntervalSeconds int
}

// GetChannelMonitorRuntime reads the channel monitor feature flags directly from
// the settings store. Fail-open: on error returns Enabled=true with the default interval.
func (s *SettingService) GetChannelMonitorRuntime(ctx context.Context) ChannelMonitorRuntime {
	vals, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyChannelMonitorEnabled,
		SettingKeyChannelMonitorDefaultIntervalSeconds,
	})
	if err != nil {
		return ChannelMonitorRuntime{Enabled: true, DefaultIntervalSeconds: channelMonitorIntervalFallback}
	}
	return ChannelMonitorRuntime{
		Enabled:                !isFalseSettingValue(vals[SettingKeyChannelMonitorEnabled]),
		DefaultIntervalSeconds: parseChannelMonitorInterval(vals[SettingKeyChannelMonitorDefaultIntervalSeconds]),
	}
}

// AvailableChannelsRuntime is the lightweight view of the available-channels feature
// switch consumed by the user-facing handler.
type AvailableChannelsRuntime struct {
	Enabled bool
}

// GetAvailableChannelsRuntime reads the available-channels feature switch directly
// from the settings store. Fail-closed: on error returns Enabled=false, matching
// the opt-in default (unknown ↔ disabled).
func (s *SettingService) GetAvailableChannelsRuntime(ctx context.Context) AvailableChannelsRuntime {
	vals, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyAvailableChannelsEnabled})
	if err != nil {
		return AvailableChannelsRuntime{Enabled: false}
	}
	return AvailableChannelsRuntime{
		Enabled: vals[SettingKeyAvailableChannelsEnabled] == "true",
	}
}

// IsUserErrorViewAllowed reads the user-facing error-requests visibility switch
// directly from the settings store. Fail-closed: on error returns false (opt-in default).
func (s *SettingService) IsUserErrorViewAllowed(ctx context.Context) bool {
	vals, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyAllowUserViewErrorRequests})
	if err != nil {
		slog.Warn("failed to get allow_user_view_error_requests setting, defaulting to false", "error", err)
		return false
	}
	return vals[SettingKeyAllowUserViewErrorRequests] == "true"
}

// GetAntigravityUserAgentVersion 返回 Antigravity 上游请求使用的版本号。
// 后台设置优先；为空、缺失或非法时回退到 ANTIGRAVITY_USER_AGENT_VERSION / 内置默认值。
func (s *SettingService) GetAntigravityUserAgentVersion(ctx context.Context) string {
	fallback := antigravity.GetDefaultUserAgentVersion()
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	if cached, ok := s.antigravityUAVersionCache.Load().(*cachedAntigravityUserAgentVersion); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.version
		}
	}

	result, _, _ := s.antigravityUAVersionSF.Do("antigravity_user_agent_version", func() (any, error) {
		if cached, ok := s.antigravityUAVersionCache.Load().(*cachedAntigravityUserAgentVersion); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.version, nil
			}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), antigravityUserAgentVersionDBTimeout)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyAntigravityUserAgentVersion)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			slog.Warn("failed to get antigravity user agent version setting", "error", err)
			s.antigravityUAVersionCache.Store(&cachedAntigravityUserAgentVersion{
				version:   fallback,
				expiresAt: time.Now().Add(antigravityUserAgentVersionErrorTTL).UnixNano(),
			})
			return fallback, nil
		}
		version := antigravity.NormalizeUserAgentVersion(value)
		if version == "" {
			version = fallback
		}
		s.antigravityUAVersionCache.Store(&cachedAntigravityUserAgentVersion{
			version:   version,
			expiresAt: time.Now().Add(antigravityUserAgentVersionCacheTTL).UnixNano(),
		})
		return version, nil
	})
	if version, ok := result.(string); ok && version != "" {
		return version
	}
	return fallback
}

// GetOpenAICodexUserAgent 返回 OpenAI Codex 上游请求使用的 User-Agent。
// 后台设置优先；为空时回退到内置默认值。
func (s *SettingService) GetOpenAICodexUserAgent(ctx context.Context) string {
	fallback := DefaultOpenAICodexUserAgent
	if s == nil || s.settingRepo == nil {
		return fallback
	}
	if cached, ok := s.openAICodexUACache.Load().(*cachedOpenAICodexUserAgent); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}

	result, _, _ := s.openAICodexUASF.Do("openai_codex_user_agent", func() (any, error) {
		if cached, ok := s.openAICodexUACache.Load().(*cachedOpenAICodexUserAgent); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.value, nil
			}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAICodexUserAgentDBTimeout)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAICodexUserAgent)
		if err != nil && !errors.Is(err, ErrSettingNotFound) {
			slog.Warn("failed to get openai codex user agent setting", "error", err)
			s.openAICodexUACache.Store(&cachedOpenAICodexUserAgent{
				value:     fallback,
				expiresAt: time.Now().Add(openAICodexUserAgentErrorTTL).UnixNano(),
			})
			return fallback, nil
		}
		ua := strings.TrimSpace(value)
		if ua == "" {
			ua = fallback
		}
		s.openAICodexUACache.Store(&cachedOpenAICodexUserAgent{
			value:     ua,
			expiresAt: time.Now().Add(openAICodexUserAgentCacheTTL).UnixNano(),
		})
		return ua, nil
	})
	if ua, ok := result.(string); ok && ua != "" {
		return ua
	}
	return fallback
}

var legacyClaudeCodeCodexWhitelistEntry = openai.AllowedClientEntry{
	Originator: "Claude Code",
	UAContains: []string{"Claude Code/"},
}

// MigrateOpenAIAllowClaudeCodeCodexPluginSetting folds the deprecated global Claude Code
// plugin allow switch into codex_cli_only_whitelist. The app-server identity model is the
// same originator + UA marker pair, so runtime checks no longer need a separate flag.
func (s *SettingService) MigrateOpenAIAllowClaudeCodeCodexPluginSetting(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), codexRestrictionPolicyDBTimeout)
	defer cancel()

	legacyValue, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpenAIAllowClaudeCodeCodexPlugin)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return nil
		}
		return fmt.Errorf("get deprecated %s setting: %w", SettingKeyOpenAIAllowClaudeCodeCodexPlugin, err)
	}
	if strings.TrimSpace(legacyValue) != "true" {
		return nil
	}

	rawWhitelist, err := s.settingRepo.GetValue(dbCtx, SettingKeyCodexCLIOnlyWhitelist)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return fmt.Errorf("get %s setting: %w", SettingKeyCodexCLIOnlyWhitelist, err)
	}

	var entries []openai.AllowedClientEntry
	if strings.TrimSpace(rawWhitelist) != "" {
		if err := json.Unmarshal([]byte(rawWhitelist), &entries); err != nil {
			return fmt.Errorf("parse %s setting: %w", SettingKeyCodexCLIOnlyWhitelist, err)
		}
	}
	if codexClientEntriesContain(entries, legacyClaudeCodeCodexWhitelistEntry) {
		return nil
	}

	entries = append(entries, legacyClaudeCodeCodexWhitelistEntry)
	encoded, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("marshal %s setting: %w", SettingKeyCodexCLIOnlyWhitelist, err)
	}
	if err := s.settingRepo.Set(dbCtx, SettingKeyCodexCLIOnlyWhitelist, string(encoded)); err != nil {
		return fmt.Errorf("set %s setting: %w", SettingKeyCodexCLIOnlyWhitelist, err)
	}
	s.codexRestrictionPolicySF.Forget("codex_restriction_policy")
	s.codexRestrictionPolicyCache.Store(&cachedCodexRestrictionPolicy{expiresAt: 0})
	return nil
}

// MigrateCodexBodyFingerprintToSignals 把已废弃的 codex_cli_only_allow_body_engine_fingerprint
// 开关并入引擎指纹信号列表。幂等:信号键已存在(非空)则不动;缺失时写默认种子,
// 并把 body 路径行的 Required 设为旧 body 开关的值(旧 true ⇒ 勾上 body 行)。
func (s *SettingService) MigrateCodexBodyFingerprintToSignals(ctx context.Context) error {
	if s == nil || s.settingRepo == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), codexRestrictionPolicyDBTimeout)
	defer cancel()

	if v, err := s.settingRepo.GetValue(dbCtx, SettingKeyCodexCLIOnlyEngineFingerprintSignals); err == nil && strings.TrimSpace(v) != "" {
		return nil // 已配置/已迁移
	} else if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return fmt.Errorf("get %s setting: %w", SettingKeyCodexCLIOnlyEngineFingerprintSignals, err)
	}

	bodyOn := false
	if v, err := s.settingRepo.GetValue(dbCtx, SettingKeyCodexCLIOnlyAllowBodyEngineFingerprint); err == nil {
		bodyOn = strings.TrimSpace(v) == "true"
	} else if !errors.Is(err, ErrSettingNotFound) {
		return fmt.Errorf("get deprecated %s setting: %w", SettingKeyCodexCLIOnlyAllowBodyEngineFingerprint, err)
	}

	seed := make([]openai.EngineFingerprintSignal, len(openai.DefaultEngineFingerprintSignals))
	copy(seed, openai.DefaultEngineFingerprintSignals)
	if bodyOn {
		for i := range seed {
			if seed[i].Type == openai.FingerprintSignalBodyPath {
				seed[i].Required = true
			}
		}
	}
	encoded, err := json.Marshal(seed)
	if err != nil {
		return fmt.Errorf("marshal %s setting: %w", SettingKeyCodexCLIOnlyEngineFingerprintSignals, err)
	}
	if err := s.settingRepo.Set(dbCtx, SettingKeyCodexCLIOnlyEngineFingerprintSignals, string(encoded)); err != nil {
		return fmt.Errorf("set %s setting: %w", SettingKeyCodexCLIOnlyEngineFingerprintSignals, err)
	}
	s.codexRestrictionPolicySF.Forget("codex_restriction_policy")
	s.codexRestrictionPolicyCache.Store(&cachedCodexRestrictionPolicy{expiresAt: 0})
	return nil
}

func codexClientEntriesContain(entries []openai.AllowedClientEntry, want openai.AllowedClientEntry) bool {
	wantOriginator := strings.TrimSpace(want.Originator)
	if wantOriginator == "" {
		return false
	}
	wantMarkers := normalizedCodexClientMarkers(want.UAContains)
	if len(wantMarkers) == 0 {
		return false
	}
	for _, entry := range entries {
		if !strings.EqualFold(strings.TrimSpace(entry.Originator), wantOriginator) {
			continue
		}
		gotMarkers := normalizedCodexClientMarkers(entry.UAContains)
		if len(gotMarkers) != len(wantMarkers) {
			continue
		}
		matched := true
		for marker := range wantMarkers {
			if _, ok := gotMarkers[marker]; !ok {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func normalizedCodexClientMarkers(markers []string) map[string]struct{} {
	normalized := make(map[string]struct{}, len(markers))
	for _, marker := range markers {
		marker = strings.TrimSpace(marker)
		if marker == "" {
			continue
		}
		normalized[strings.ToLower(marker)] = struct{}{}
	}
	return normalized
}

// GetCodexRestrictionPolicy 读取 codex_cli_only 全局加固策略（黑/白名单、最低版本、引擎指纹门）。
// 仅在调用方已确认账号 codex_cli_only 开启时读取；进程内 atomic.Value 缓存（60s TTL）避免热路径访问 DB。
// 任意键缺失/解析失败 → 安全默认：空名单、空版本、默认种子指纹信号。
func (s *SettingService) GetCodexRestrictionPolicy(ctx context.Context) CodexRestrictionPolicy {
	if cached, ok := s.codexRestrictionPolicyCache.Load().(*cachedCodexRestrictionPolicy); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}
	result, _, _ := s.codexRestrictionPolicySF.Do("codex_restriction_policy", func() (any, error) {
		if cached, ok := s.codexRestrictionPolicyCache.Load().(*cachedCodexRestrictionPolicy); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.value, nil
			}
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), codexRestrictionPolicyDBTimeout)
		defer cancel()

		pol := CodexRestrictionPolicy{EngineFingerprintSignals: openai.DefaultEngineFingerprintSignals} // 安全默认：默认种子指纹信号
		if v, err := s.settingRepo.GetValue(dbCtx, SettingKeyMinCodexVersion); err == nil {
			pol.MinCodexVersion = strings.TrimSpace(v)
		}
		if v, err := s.settingRepo.GetValue(dbCtx, SettingKeyMaxCodexVersion); err == nil {
			pol.MaxCodexVersion = strings.TrimSpace(v)
		}
		if v, err := s.settingRepo.GetValue(dbCtx, SettingKeyCodexCLIOnlyAllowAppServerClients); err == nil {
			pol.AllowAppServerClients = strings.TrimSpace(v) == "true" // 仅显式 "true" 开启
		}
		pol.EngineFingerprintSignals = s.loadEngineFingerprintSignals(dbCtx)
		pol.Whitelist = s.loadCodexClientEntries(dbCtx, SettingKeyCodexCLIOnlyWhitelist)
		pol.Blacklist = s.loadCodexClientEntries(dbCtx, SettingKeyCodexCLIOnlyBlacklist)

		s.codexRestrictionPolicyCache.Store(&cachedCodexRestrictionPolicy{
			value:     pol,
			expiresAt: time.Now().Add(codexRestrictionPolicyCacheTTL).UnixNano(),
		})
		return pol, nil
	})
	if pol, ok := result.(CodexRestrictionPolicy); ok {
		return pol
	}
	return CodexRestrictionPolicy{EngineFingerprintSignals: openai.DefaultEngineFingerprintSignals}
}

// loadCodexClientEntries 读取并解析 []openai.AllowedClientEntry JSON 设置；缺失/空/非法 → nil（安全忽略）。
func (s *SettingService) loadCodexClientEntries(ctx context.Context, key string) []openai.AllowedClientEntry {
	v, err := s.settingRepo.GetValue(ctx, key)
	if err != nil || strings.TrimSpace(v) == "" {
		return nil
	}
	var entries []openai.AllowedClientEntry
	if json.Unmarshal([]byte(v), &entries) != nil {
		return nil
	}
	return entries
}

// loadEngineFingerprintSignals 读取引擎指纹信号列表;缺失/空/非法 → 默认种子。
func (s *SettingService) loadEngineFingerprintSignals(ctx context.Context) []openai.EngineFingerprintSignal {
	v, err := s.settingRepo.GetValue(ctx, SettingKeyCodexCLIOnlyEngineFingerprintSignals)
	if err != nil || strings.TrimSpace(v) == "" {
		return openai.DefaultEngineFingerprintSignals
	}
	sigs, ok := openai.ParseEngineFingerprintSignals(v)
	if !ok {
		return openai.DefaultEngineFingerprintSignals
	}
	return sigs
}

// ValidateCodexClientEntriesJSON 校验 codex_cli_only 名单 JSON 配置（黑名单语义）：
// 空=合法（禁用）；非空须为 []AllowedClientEntry 的 JSON 数组。黑名单是 OR 宽 deny，
// 允许 originator-only 条目，故不校验 ua_contains。白名单请用 ValidateCodexWhitelistEntriesJSON。
func ValidateCodexClientEntriesJSON(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var entries []openai.AllowedClientEntry
	if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
		return fmt.Errorf("must be empty or a valid JSON array of {originator, ua_contains}")
	}
	return nil
}

// ValidateCodexWhitelistEntriesJSON 在 ValidateCodexClientEntriesJSON 的数组结构校验之上，额外要求
// 每条白名单条目「有可能命中」（openai.AllowedClientEntry.IsWhitelistable）。白名单是双因子 AND：
// originator-only、空或含空白 ua_contains 的条目会在运行时静默失效——这里让管理员在写入时即收到反馈，
// 而非存入永不命中的死规则。黑名单（OR 宽 deny）仍用 ValidateCodexClientEntriesJSON。
func ValidateCodexWhitelistEntriesJSON(raw string) error {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var entries []openai.AllowedClientEntry
	if err := json.Unmarshal([]byte(trimmed), &entries); err != nil {
		return fmt.Errorf("must be empty or a valid JSON array of {originator, ua_contains}")
	}
	for i, e := range entries {
		if !e.IsWhitelistable() {
			return fmt.Errorf("entry %d: whitelist requires a non-empty originator and at least one non-empty ua_contains (double-factor AND; otherwise the rule never matches)", i)
		}
	}
	return nil
}

// ValidateEngineFingerprintSignalsJSON 服务层包装,复用 openai 校验逻辑。
func ValidateEngineFingerprintSignalsJSON(raw string) error {
	return openai.ValidateEngineFingerprintSignalsJSON(raw)
}

// SetOnUpdateCallback sets a callback function to be called when settings are updated
// This is used for cache invalidation (e.g., HTML cache in frontend server)
func (s *SettingService) SetOnUpdateCallback(callback func()) {
	s.onUpdate = callback
}

// SetVersion sets the application version for injection into public settings
func (s *SettingService) SetVersion(version string) {
	s.version = version
}

// PublicSettingsInjectionPayload is the JSON shape embedded into HTML as
// `window.__APP_CONFIG__` so the frontend can hydrate feature flags & site
// config before the first XHR finishes.
//
// INVARIANT: every `json` tag here MUST also exist on handler/dto.PublicSettings.
// If you forget a feature-flag field here, the frontend's
// `cachedPublicSettings.xxx_enabled` will be `undefined` on refresh until the
// async `/api/v1/settings/public` call returns — which causes opt-in menus
// (strict `=== true`) to flicker off/on. See
// frontend/src/utils/featureFlags.ts for the matching registry.
//
// A unit test diffs this struct's JSON keys against dto.PublicSettings to catch
// drift automatically (see setting_service_injection_test.go).
type PublicSettingsInjectionPayload struct {
	RegistrationEnabled              bool                     `json:"registration_enabled"`
	EmailVerifyEnabled               bool                     `json:"email_verify_enabled"`
	RegistrationEmailSuffixWhitelist []string                 `json:"registration_email_suffix_whitelist"`
	PromoCodeEnabled                 bool                     `json:"promo_code_enabled"`
	PasswordResetEnabled             bool                     `json:"password_reset_enabled"`
	InvitationCodeEnabled            bool                     `json:"invitation_code_enabled"`
	TotpEnabled                      bool                     `json:"totp_enabled"`
	LoginAgreementEnabled            bool                     `json:"login_agreement_enabled"`
	LoginAgreementMode               string                   `json:"login_agreement_mode"`
	LoginAgreementUpdatedAt          string                   `json:"login_agreement_updated_at"`
	LoginAgreementRevision           string                   `json:"login_agreement_revision"`
	LoginAgreementDocuments          []LoginAgreementDocument `json:"login_agreement_documents"`
	TurnstileEnabled                 bool                     `json:"turnstile_enabled"`
	TurnstileSiteKey                 string                   `json:"turnstile_site_key"`
	SiteName                         string                   `json:"site_name"`
	SiteLogo                         string                   `json:"site_logo"`
	SiteSubtitle                     string                   `json:"site_subtitle"`
	APIBaseURL                       string                   `json:"api_base_url"`
	ContactInfo                      string                   `json:"contact_info"`
	DocURL                           string                   `json:"doc_url"`
	HomeContent                      string                   `json:"home_content"`
	HideCcsImportButton              bool                     `json:"hide_ccs_import_button"`
	PurchaseSubscriptionEnabled      bool                     `json:"purchase_subscription_enabled"`
	PurchaseSubscriptionURL          string                   `json:"purchase_subscription_url"`
	TableDefaultPageSize             int                      `json:"table_default_page_size"`
	TablePageSizeOptions             []int                    `json:"table_page_size_options"`
	CustomMenuItems                  json.RawMessage          `json:"custom_menu_items"`
	CustomEndpoints                  json.RawMessage          `json:"custom_endpoints"`
	LinuxDoOAuthEnabled              bool                     `json:"linuxdo_oauth_enabled"`
	DingTalkOAuthEnabled             bool                     `json:"dingtalk_oauth_enabled"`
	WeChatOAuthEnabled               bool                     `json:"wechat_oauth_enabled"`
	WeChatOAuthOpenEnabled           bool                     `json:"wechat_oauth_open_enabled"`
	WeChatOAuthMPEnabled             bool                     `json:"wechat_oauth_mp_enabled"`
	WeChatOAuthMobileEnabled         bool                     `json:"wechat_oauth_mobile_enabled"`
	OIDCOAuthEnabled                 bool                     `json:"oidc_oauth_enabled"`
	OIDCOAuthProviderName            string                   `json:"oidc_oauth_provider_name"`
	GitHubOAuthEnabled               bool                     `json:"github_oauth_enabled"`
	GoogleOAuthEnabled               bool                     `json:"google_oauth_enabled"`
	BackendModeEnabled               bool                     `json:"backend_mode_enabled"`
	PaymentEnabled                   bool                     `json:"payment_enabled"`
	Version                          string                   `json:"version"`
	BalanceLowNotifyEnabled          bool                     `json:"balance_low_notify_enabled"`
	AccountQuotaNotifyEnabled        bool                     `json:"account_quota_notify_enabled"`
	BalanceLowNotifyThreshold        float64                  `json:"balance_low_notify_threshold"`
	BalanceLowNotifyRechargeURL      string                   `json:"balance_low_notify_recharge_url"`

	// Feature flags — MUST match the opt-in/opt-out registry in
	// frontend/src/utils/featureFlags.ts. Missing a field here is the bug
	// that hid the "可用渠道" menu on page refresh.
	ChannelMonitorEnabled                bool   `json:"channel_monitor_enabled"`
	ChannelMonitorDefaultIntervalSeconds int    `json:"channel_monitor_default_interval_seconds"`
	AvailableChannelsEnabled             bool   `json:"available_channels_enabled"`
	AffiliateEnabled                     bool   `json:"affiliate_enabled"`
	DeviceAutoActivationAffCodes         string `json:"device_auto_activation_aff_codes"`
	RiskControlEnabled                   bool   `json:"risk_control_enabled"`
	AllowUserViewErrorRequests           bool   `json:"allow_user_view_error_requests"`
}

// GetPublicSettingsForInjection returns public settings in a format suitable for HTML injection.
// This implements the web.PublicSettingsProvider interface.
func (s *SettingService) GetPublicSettingsForInjection(ctx context.Context) (any, error) {
	settings, err := s.GetPublicSettings(ctx)
	if err != nil {
		return nil, err
	}

	return &PublicSettingsInjectionPayload{
		RegistrationEnabled:              settings.RegistrationEnabled,
		EmailVerifyEnabled:               settings.EmailVerifyEnabled,
		RegistrationEmailSuffixWhitelist: settings.RegistrationEmailSuffixWhitelist,
		PromoCodeEnabled:                 settings.PromoCodeEnabled,
		PasswordResetEnabled:             settings.PasswordResetEnabled,
		InvitationCodeEnabled:            settings.InvitationCodeEnabled,
		TotpEnabled:                      settings.TotpEnabled,
		LoginAgreementEnabled:            settings.LoginAgreementEnabled,
		LoginAgreementMode:               settings.LoginAgreementMode,
		LoginAgreementUpdatedAt:          settings.LoginAgreementUpdatedAt,
		LoginAgreementRevision:           settings.LoginAgreementRevision,
		LoginAgreementDocuments:          settings.LoginAgreementDocuments,
		TurnstileEnabled:                 settings.TurnstileEnabled,
		TurnstileSiteKey:                 settings.TurnstileSiteKey,
		SiteName:                         settings.SiteName,
		SiteLogo:                         settings.SiteLogo,
		SiteSubtitle:                     settings.SiteSubtitle,
		APIBaseURL:                       settings.APIBaseURL,
		ContactInfo:                      settings.ContactInfo,
		DocURL:                           settings.DocURL,
		HomeContent:                      settings.HomeContent,
		HideCcsImportButton:              settings.HideCcsImportButton,
		PurchaseSubscriptionEnabled:      settings.PurchaseSubscriptionEnabled,
		PurchaseSubscriptionURL:          settings.PurchaseSubscriptionURL,
		TableDefaultPageSize:             settings.TableDefaultPageSize,
		TablePageSizeOptions:             settings.TablePageSizeOptions,
		CustomMenuItems:                  filterUserVisibleMenuItems(settings.CustomMenuItems),
		CustomEndpoints:                  safeRawJSONArray(settings.CustomEndpoints),
		LinuxDoOAuthEnabled:              settings.LinuxDoOAuthEnabled,
		DingTalkOAuthEnabled:             settings.DingTalkOAuthEnabled,
		WeChatOAuthEnabled:               settings.WeChatOAuthEnabled,
		WeChatOAuthOpenEnabled:           settings.WeChatOAuthOpenEnabled,
		WeChatOAuthMPEnabled:             settings.WeChatOAuthMPEnabled,
		WeChatOAuthMobileEnabled:         settings.WeChatOAuthMobileEnabled,
		OIDCOAuthEnabled:                 settings.OIDCOAuthEnabled,
		OIDCOAuthProviderName:            settings.OIDCOAuthProviderName,
		GitHubOAuthEnabled:               settings.GitHubOAuthEnabled,
		GoogleOAuthEnabled:               settings.GoogleOAuthEnabled,
		BackendModeEnabled:               settings.BackendModeEnabled,
		PaymentEnabled:                   settings.PaymentEnabled,
		Version:                          s.version,
		BalanceLowNotifyEnabled:          settings.BalanceLowNotifyEnabled,
		AccountQuotaNotifyEnabled:        settings.AccountQuotaNotifyEnabled,
		BalanceLowNotifyThreshold:        settings.BalanceLowNotifyThreshold,
		BalanceLowNotifyRechargeURL:      settings.BalanceLowNotifyRechargeURL,

		ChannelMonitorEnabled:                settings.ChannelMonitorEnabled,
		ChannelMonitorDefaultIntervalSeconds: settings.ChannelMonitorDefaultIntervalSeconds,
		AvailableChannelsEnabled:             settings.AvailableChannelsEnabled,
		AffiliateEnabled:                     settings.AffiliateEnabled,
		DeviceAutoActivationAffCodes:         settings.DeviceAutoActivationAffCodes,
		RiskControlEnabled:                   settings.RiskControlEnabled,
		AllowUserViewErrorRequests:           settings.AllowUserViewErrorRequests,
	}, nil
}

func DefaultWeChatConnectScopesForMode(mode string) string {
	return defaultWeChatConnectScopeForMode(mode)
}

func (s *SettingService) parseWeChatConnectOAuthConfig(settings map[string]string) (WeChatConnectOAuthConfig, error) {
	cfg := s.effectiveWeChatConnectOAuthConfig(settings)

	if !cfg.Enabled || (!cfg.OpenEnabled && !cfg.MPEnabled) {
		return WeChatConnectOAuthConfig{}, infraerrors.NotFound("OAUTH_DISABLED", "wechat oauth is disabled")
	}
	if cfg.OpenEnabled {
		if cfg.AppIDForMode("open") == "" {
			return WeChatConnectOAuthConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth pc app id not configured")
		}
		if cfg.AppSecretForMode("open") == "" {
			return WeChatConnectOAuthConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth pc app secret not configured")
		}
	}
	if cfg.MPEnabled {
		if cfg.AppIDForMode("mp") == "" {
			return WeChatConnectOAuthConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth official account app id not configured")
		}
		if cfg.AppSecretForMode("mp") == "" {
			return WeChatConnectOAuthConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth official account app secret not configured")
		}
	}
	if cfg.MobileEnabled {
		if cfg.AppIDForMode("mobile") == "" {
			return WeChatConnectOAuthConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth mobile app id not configured")
		}
		if cfg.AppSecretForMode("mobile") == "" {
			return WeChatConnectOAuthConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth mobile app secret not configured")
		}
	}
	if v := strings.TrimSpace(cfg.RedirectURL); v != "" {
		if err := config.ValidateAbsoluteHTTPURL(v); err != nil {
			return WeChatConnectOAuthConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth redirect url invalid")
		}
	}
	if err := config.ValidateFrontendRedirectURL(cfg.FrontendRedirectURL); err != nil {
		return WeChatConnectOAuthConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "wechat oauth frontend redirect url invalid")
	}
	return cfg, nil
}

func (s *SettingService) weChatOAuthCapabilitiesFromSettings(settings map[string]string) (bool, bool, bool, bool) {
	cfg := s.effectiveWeChatConnectOAuthConfig(settings)
	if !cfg.Enabled {
		return false, false, false, false
	}

	openReady := cfg.OpenEnabled && cfg.AppIDForMode("open") != "" && cfg.AppSecretForMode("open") != ""
	mpReady := cfg.MPEnabled && cfg.AppIDForMode("mp") != "" && cfg.AppSecretForMode("mp") != ""
	mobileReady := cfg.MobileEnabled && cfg.AppIDForMode("mobile") != "" && cfg.AppSecretForMode("mobile") != ""

	return openReady || mpReady, openReady, mpReady, mobileReady
}

func (s *SettingService) emailOAuthBaseConfig(provider string) config.EmailOAuthProviderConfig {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		cfg := config.EmailOAuthProviderConfig{
			AuthorizeURL:        defaultGitHubOAuthAuthorize,
			TokenURL:            defaultGitHubOAuthToken,
			UserInfoURL:         defaultGitHubOAuthUserInfo,
			EmailsURL:           defaultGitHubOAuthEmails,
			Scopes:              defaultGitHubOAuthScopes,
			FrontendRedirectURL: defaultGitHubOAuthFrontend,
		}
		if s != nil && s.cfg != nil {
			cfg = mergeEmailOAuthBaseConfig(cfg, s.cfg.GitHubOAuth)
		}
		return cfg
	case "google":
		cfg := config.EmailOAuthProviderConfig{
			AuthorizeURL:        defaultGoogleOAuthAuthorize,
			TokenURL:            defaultGoogleOAuthToken,
			UserInfoURL:         defaultGoogleOAuthUserInfo,
			Scopes:              defaultGoogleOAuthScopes,
			FrontendRedirectURL: defaultGoogleOAuthFrontend,
		}
		if s != nil && s.cfg != nil {
			cfg = mergeEmailOAuthBaseConfig(cfg, s.cfg.GoogleOAuth)
		}
		return cfg
	default:
		return config.EmailOAuthProviderConfig{}
	}
}

func mergeEmailOAuthBaseConfig(base, override config.EmailOAuthProviderConfig) config.EmailOAuthProviderConfig {
	base.Enabled = override.Enabled
	if strings.TrimSpace(override.ClientID) != "" {
		base.ClientID = strings.TrimSpace(override.ClientID)
	}
	if strings.TrimSpace(override.ClientSecret) != "" {
		base.ClientSecret = strings.TrimSpace(override.ClientSecret)
	}
	if strings.TrimSpace(override.AuthorizeURL) != "" {
		base.AuthorizeURL = strings.TrimSpace(override.AuthorizeURL)
	}
	if strings.TrimSpace(override.TokenURL) != "" {
		base.TokenURL = strings.TrimSpace(override.TokenURL)
	}
	if strings.TrimSpace(override.UserInfoURL) != "" {
		base.UserInfoURL = strings.TrimSpace(override.UserInfoURL)
	}
	if strings.TrimSpace(override.EmailsURL) != "" {
		base.EmailsURL = strings.TrimSpace(override.EmailsURL)
	}
	if strings.TrimSpace(override.Scopes) != "" {
		base.Scopes = strings.TrimSpace(override.Scopes)
	}
	if strings.TrimSpace(override.RedirectURL) != "" {
		base.RedirectURL = strings.TrimSpace(override.RedirectURL)
	}
	if strings.TrimSpace(override.FrontendRedirectURL) != "" {
		base.FrontendRedirectURL = strings.TrimSpace(override.FrontendRedirectURL)
	}
	return base
}

func (s *SettingService) emailOAuthPublicEnabled(settings map[string]string, provider string) bool {
	cfg := s.effectiveEmailOAuthConfig(settings, provider)
	return cfg.Enabled && strings.TrimSpace(cfg.ClientID) != "" && strings.TrimSpace(cfg.ClientSecret) != ""
}

func (s *SettingService) effectiveEmailOAuthConfig(settings map[string]string, provider string) config.EmailOAuthProviderConfig {
	cfg := s.emailOAuthBaseConfig(provider)
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "github":
		if raw, ok := settings[SettingKeyGitHubOAuthEnabled]; ok {
			cfg.Enabled = raw == "true"
		}
		cfg.ClientID = firstNonEmpty(settings[SettingKeyGitHubOAuthClientID], cfg.ClientID)
		cfg.ClientSecret = firstNonEmpty(settings[SettingKeyGitHubOAuthClientSecret], cfg.ClientSecret)
		cfg.RedirectURL = firstNonEmpty(settings[SettingKeyGitHubOAuthRedirectURL], cfg.RedirectURL)
		cfg.FrontendRedirectURL = firstNonEmpty(settings[SettingKeyGitHubOAuthFrontendRedirectURL], cfg.FrontendRedirectURL, defaultGitHubOAuthFrontend)
	case "google":
		if raw, ok := settings[SettingKeyGoogleOAuthEnabled]; ok {
			cfg.Enabled = raw == "true"
		}
		cfg.ClientID = firstNonEmpty(settings[SettingKeyGoogleOAuthClientID], cfg.ClientID)
		cfg.ClientSecret = firstNonEmpty(settings[SettingKeyGoogleOAuthClientSecret], cfg.ClientSecret)
		cfg.RedirectURL = firstNonEmpty(settings[SettingKeyGoogleOAuthRedirectURL], cfg.RedirectURL)
		cfg.FrontendRedirectURL = firstNonEmpty(settings[SettingKeyGoogleOAuthFrontendRedirectURL], cfg.FrontendRedirectURL, defaultGoogleOAuthFrontend)
	}
	return cfg
}

// filterUserVisibleMenuItems filters out admin-only menu items from a raw JSON
// array string, returning only items with visibility != "admin".
func filterUserVisibleMenuItems(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return json.RawMessage("[]")
	}
	var items []struct {
		Visibility string `json:"visibility"`
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return json.RawMessage("[]")
	}

	// Parse full items to preserve all fields
	var fullItems []json.RawMessage
	if err := json.Unmarshal([]byte(raw), &fullItems); err != nil {
		return json.RawMessage("[]")
	}

	var filtered []json.RawMessage
	for i, item := range items {
		if item.Visibility != "admin" {
			filtered = append(filtered, fullItems[i])
		}
	}
	if len(filtered) == 0 {
		return json.RawMessage("[]")
	}
	result, err := json.Marshal(filtered)
	if err != nil {
		return json.RawMessage("[]")
	}
	return result
}

// safeRawJSONArray returns raw as json.RawMessage if it's valid JSON, otherwise "[]".
func safeRawJSONArray(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return json.RawMessage("[]")
	}
	if json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	return json.RawMessage("[]")
}

// GetFrameSrcOrigins returns deduplicated http(s) origins from home_content URL,
// purchase_subscription_url, and all custom_menu_items URLs. Used by the router layer for CSP frame-src injection.
func (s *SettingService) GetFrameSrcOrigins(ctx context.Context) ([]string, error) {
	settings, err := s.GetPublicSettings(ctx)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]struct{})
	var origins []string

	addOrigin := func(rawURL string) {
		if origin := extractOriginFromURL(rawURL); origin != "" {
			if _, ok := seen[origin]; !ok {
				seen[origin] = struct{}{}
				origins = append(origins, origin)
			}
		}
	}

	// home content URL (when home_content is set to a URL for iframe embedding)
	addOrigin(settings.HomeContent)

	// purchase subscription URL
	if settings.PurchaseSubscriptionEnabled {
		addOrigin(settings.PurchaseSubscriptionURL)
	}

	// all custom menu items (including admin-only, since CSP must allow all iframes)
	for _, item := range parseCustomMenuItemURLs(settings.CustomMenuItems) {
		addOrigin(item)
	}

	return origins, nil
}

// extractOriginFromURL returns the scheme+host origin from rawURL.
// Only http and https schemes are accepted.
func extractOriginFromURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ""
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return ""
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// parseCustomMenuItemURLs extracts URLs from a raw JSON array of custom menu items.
func parseCustomMenuItemURLs(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "[]" {
		return nil
	}
	var items []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}
	urls := make([]string, 0, len(items))
	for _, item := range items {
		if item.URL != "" {
			urls = append(urls, item.URL)
		}
	}
	return urls
}

func oidcUsePKCECompatibilityDefault(base config.OIDCConnectConfig) bool {
	if base.UsePKCEExplicit {
		return base.UsePKCE
	}
	return true
}

func oidcValidateIDTokenCompatibilityDefault(base config.OIDCConnectConfig) bool {
	if base.ValidateIDTokenExplicit {
		return base.ValidateIDToken
	}
	return true
}

func oidcCompatibilityWriteDefault(base config.OIDCConnectConfig, configured bool, raw string, explicit bool, explicitValue bool) bool {
	if configured {
		return strings.TrimSpace(raw) == "true"
	}
	if explicit {
		return explicitValue
	}
	return false
}

// UpdateSettings 更新系统设置
func (s *SettingService) UpdateSettings(ctx context.Context, settings *SystemSettings) error {
	updates, err := s.buildSystemSettingsUpdates(ctx, settings)
	if err != nil {
		return err
	}

	err = s.settingRepo.SetMultiple(ctx, updates)
	if err == nil {
		s.refreshCachedSettings(settings)
	}
	return err
}

func (s *SettingService) OIDCSecurityWriteDefaults(ctx context.Context) (bool, bool, error) {
	rawSettings, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyOIDCConnectUsePKCE,
		SettingKeyOIDCConnectValidateIDToken,
	})
	if err != nil {
		return false, false, fmt.Errorf("get oidc security write defaults: %w", err)
	}

	base := config.OIDCConnectConfig{}
	if s != nil && s.cfg != nil {
		base = s.cfg.OIDC
	}

	rawUsePKCE, hasUsePKCE := rawSettings[SettingKeyOIDCConnectUsePKCE]
	rawValidateIDToken, hasValidateIDToken := rawSettings[SettingKeyOIDCConnectValidateIDToken]

	return oidcCompatibilityWriteDefault(base, hasUsePKCE, rawUsePKCE, base.UsePKCEExplicit, base.UsePKCE),
		oidcCompatibilityWriteDefault(base, hasValidateIDToken, rawValidateIDToken, base.ValidateIDTokenExplicit, base.ValidateIDToken),
		nil
}

// UpdateSettingsWithAuthSourceDefaults persists system settings and auth-source defaults in a single write.
func (s *SettingService) UpdateSettingsWithAuthSourceDefaults(ctx context.Context, settings *SystemSettings, authDefaults *AuthSourceDefaultSettings) error {
	updates, err := s.buildSystemSettingsUpdates(ctx, settings)
	if err != nil {
		return err
	}

	authSourceUpdates, err := s.buildAuthSourceDefaultUpdates(ctx, authDefaults)
	if err != nil {
		return err
	}
	for key, value := range authSourceUpdates {
		updates[key] = value
	}

	err = s.settingRepo.SetMultiple(ctx, updates)
	if err == nil {
		s.refreshCachedSettings(settings)
	}
	return err
}

func (s *SettingService) buildSystemSettingsUpdates(ctx context.Context, settings *SystemSettings) (map[string]string, error) {
	if err := s.validateDefaultSubscriptionGroups(ctx, settings.DefaultSubscriptions); err != nil {
		return nil, err
	}
	normalizedWhitelist, err := NormalizeRegistrationEmailSuffixWhitelist(settings.RegistrationEmailSuffixWhitelist)
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_REGISTRATION_EMAIL_SUFFIX_WHITELIST", err.Error())
	}
	if normalizedWhitelist == nil {
		normalizedWhitelist = []string{}
	}
	settings.RegistrationEmailSuffixWhitelist = normalizedWhitelist
	alipaySource, err := normalizeVisibleMethodSettingSource("alipay", settings.PaymentVisibleMethodAlipaySource, settings.PaymentVisibleMethodAlipayEnabled)
	if err != nil {
		return nil, err
	}
	wxpaySource, err := normalizeVisibleMethodSettingSource("wxpay", settings.PaymentVisibleMethodWxpaySource, settings.PaymentVisibleMethodWxpayEnabled)
	if err != nil {
		return nil, err
	}
	settings.PaymentVisibleMethodAlipaySource = alipaySource
	settings.PaymentVisibleMethodWxpaySource = wxpaySource
	settings.WeChatConnectAppID = strings.TrimSpace(settings.WeChatConnectAppID)
	settings.WeChatConnectAppSecret = strings.TrimSpace(settings.WeChatConnectAppSecret)
	settings.WeChatConnectOpenAppID = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectOpenAppID, settings.WeChatConnectAppID))
	settings.WeChatConnectOpenAppSecret = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectOpenAppSecret, settings.WeChatConnectAppSecret))
	settings.WeChatConnectMPAppID = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectMPAppID, settings.WeChatConnectAppID))
	settings.WeChatConnectMPAppSecret = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectMPAppSecret, settings.WeChatConnectAppSecret))
	settings.WeChatConnectMobileAppID = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectMobileAppID, settings.WeChatConnectAppID))
	settings.WeChatConnectMobileAppSecret = strings.TrimSpace(firstNonEmpty(settings.WeChatConnectMobileAppSecret, settings.WeChatConnectAppSecret))
	settings.WeChatConnectMode = normalizeWeChatConnectStoredMode(
		settings.WeChatConnectOpenEnabled,
		settings.WeChatConnectMPEnabled,
		settings.WeChatConnectMobileEnabled,
		settings.WeChatConnectMode,
	)
	settings.WeChatConnectScopes = normalizeWeChatConnectScopeSetting(settings.WeChatConnectScopes, settings.WeChatConnectMode)
	settings.WeChatConnectRedirectURL = strings.TrimSpace(settings.WeChatConnectRedirectURL)
	settings.WeChatConnectFrontendRedirectURL = strings.TrimSpace(settings.WeChatConnectFrontendRedirectURL)
	if settings.WeChatConnectFrontendRedirectURL == "" {
		settings.WeChatConnectFrontendRedirectURL = defaultWeChatConnectFrontend
	}
	settings.GitHubOAuthRedirectURL = strings.TrimSpace(settings.GitHubOAuthRedirectURL)
	settings.GitHubOAuthFrontendRedirectURL = strings.TrimSpace(settings.GitHubOAuthFrontendRedirectURL)
	if settings.GitHubOAuthFrontendRedirectURL == "" {
		settings.GitHubOAuthFrontendRedirectURL = defaultGitHubOAuthFrontend
	}
	settings.GoogleOAuthRedirectURL = strings.TrimSpace(settings.GoogleOAuthRedirectURL)
	settings.GoogleOAuthFrontendRedirectURL = strings.TrimSpace(settings.GoogleOAuthFrontendRedirectURL)
	if settings.GoogleOAuthFrontendRedirectURL == "" {
		settings.GoogleOAuthFrontendRedirectURL = defaultGoogleOAuthFrontend
	}

	updates := make(map[string]string)

	// 注册设置
	updates[SettingKeyRegistrationEnabled] = strconv.FormatBool(settings.RegistrationEnabled)
	updates[SettingKeyEmailVerifyEnabled] = strconv.FormatBool(settings.EmailVerifyEnabled)
	registrationEmailSuffixWhitelistJSON, err := json.Marshal(settings.RegistrationEmailSuffixWhitelist)
	if err != nil {
		return nil, fmt.Errorf("marshal registration email suffix whitelist: %w", err)
	}
	updates[SettingKeyRegistrationEmailSuffixWhitelist] = string(registrationEmailSuffixWhitelistJSON)
	updates[SettingKeyPromoCodeEnabled] = strconv.FormatBool(settings.PromoCodeEnabled)
	updates[SettingKeyPasswordResetEnabled] = strconv.FormatBool(settings.PasswordResetEnabled)
	updates[SettingKeyFrontendURL] = settings.FrontendURL
	updates[SettingKeyInvitationCodeEnabled] = strconv.FormatBool(settings.InvitationCodeEnabled)
	updates[SettingKeyTotpEnabled] = strconv.FormatBool(settings.TotpEnabled)
	settings.LoginAgreementMode = normalizeLoginAgreementMode(settings.LoginAgreementMode)
	settings.LoginAgreementUpdatedAt = strings.TrimSpace(settings.LoginAgreementUpdatedAt)
	if settings.LoginAgreementUpdatedAt == "" {
		settings.LoginAgreementUpdatedAt = defaultLoginAgreementDate
	}
	loginAgreementDocumentsJSON, err := marshalLoginAgreementDocuments(settings.LoginAgreementDocuments)
	if err != nil {
		return nil, err
	}
	updates[SettingKeyLoginAgreementEnabled] = strconv.FormatBool(settings.LoginAgreementEnabled)
	updates[SettingKeyLoginAgreementMode] = settings.LoginAgreementMode
	updates[SettingKeyLoginAgreementUpdatedAt] = settings.LoginAgreementUpdatedAt
	updates[SettingKeyLoginAgreementDocuments] = loginAgreementDocumentsJSON

	// 邮件服务设置（只有非空才更新密码）
	updates[SettingKeySMTPHost] = settings.SMTPHost
	updates[SettingKeySMTPPort] = strconv.Itoa(settings.SMTPPort)
	updates[SettingKeySMTPUsername] = settings.SMTPUsername
	if settings.SMTPPassword != "" {
		updates[SettingKeySMTPPassword] = settings.SMTPPassword
	}
	updates[SettingKeySMTPFrom] = settings.SMTPFrom
	updates[SettingKeySMTPFromName] = settings.SMTPFromName
	updates[SettingKeySMTPUseTLS] = strconv.FormatBool(settings.SMTPUseTLS)

	// Cloudflare Turnstile 设置（只有非空才更新密钥）
	updates[SettingKeyTurnstileEnabled] = strconv.FormatBool(settings.TurnstileEnabled)
	updates[SettingKeyTurnstileSiteKey] = settings.TurnstileSiteKey
	if settings.TurnstileSecretKey != "" {
		updates[SettingKeyTurnstileSecretKey] = settings.TurnstileSecretKey
	}
	updates[SettingKeyAPIKeyACLTrustForwardedIP] = strconv.FormatBool(settings.APIKeyACLTrustForwardedIP)

	// LinuxDo Connect OAuth 登录
	updates[SettingKeyLinuxDoConnectEnabled] = strconv.FormatBool(settings.LinuxDoConnectEnabled)
	updates[SettingKeyLinuxDoConnectClientID] = settings.LinuxDoConnectClientID
	updates[SettingKeyLinuxDoConnectRedirectURL] = settings.LinuxDoConnectRedirectURL
	if settings.LinuxDoConnectClientSecret != "" {
		updates[SettingKeyLinuxDoConnectClientSecret] = settings.LinuxDoConnectClientSecret
	}

	// DingTalk Connect OAuth 登录
	settings.DingTalkConnectCorpRestrictionPolicy = CoerceDingTalkCorpPolicyForWrite(settings.DingTalkConnectCorpRestrictionPolicy)
	if settings.DingTalkConnectCorpRestrictionPolicy != "internal_only" {
		settings.DingTalkConnectBypassRegistration = false
		settings.DingTalkConnectSyncCorpEmail = false
		settings.DingTalkConnectSyncDisplayName = false
		settings.DingTalkConnectSyncDept = false
	}
	settings.DingTalkConnectSyncCorpEmailAttrKey = strings.TrimSpace(settings.DingTalkConnectSyncCorpEmailAttrKey)
	if settings.DingTalkConnectSyncCorpEmailAttrKey == "" {
		settings.DingTalkConnectSyncCorpEmailAttrKey = "dingtalk_email"
	}
	settings.DingTalkConnectSyncDisplayNameAttrKey = strings.TrimSpace(settings.DingTalkConnectSyncDisplayNameAttrKey)
	if settings.DingTalkConnectSyncDisplayNameAttrKey == "" {
		settings.DingTalkConnectSyncDisplayNameAttrKey = "dingtalk_name"
	}
	settings.DingTalkConnectSyncDeptAttrKey = strings.TrimSpace(settings.DingTalkConnectSyncDeptAttrKey)
	if settings.DingTalkConnectSyncDeptAttrKey == "" {
		settings.DingTalkConnectSyncDeptAttrKey = "dingtalk_department"
	}
	settings.DingTalkConnectSyncCorpEmailAttrName = strings.TrimSpace(settings.DingTalkConnectSyncCorpEmailAttrName)
	if settings.DingTalkConnectSyncCorpEmailAttrName == "" {
		settings.DingTalkConnectSyncCorpEmailAttrName = "钉钉企业邮箱"
	}
	settings.DingTalkConnectSyncDisplayNameAttrName = strings.TrimSpace(settings.DingTalkConnectSyncDisplayNameAttrName)
	if settings.DingTalkConnectSyncDisplayNameAttrName == "" {
		settings.DingTalkConnectSyncDisplayNameAttrName = "钉钉姓名"
	}
	settings.DingTalkConnectSyncDeptAttrName = strings.TrimSpace(settings.DingTalkConnectSyncDeptAttrName)
	if settings.DingTalkConnectSyncDeptAttrName == "" {
		settings.DingTalkConnectSyncDeptAttrName = "钉钉部门"
	}
	updates[SettingKeyDingTalkConnectEnabled] = strconv.FormatBool(settings.DingTalkConnectEnabled)
	updates[SettingKeyDingTalkConnectClientID] = settings.DingTalkConnectClientID
	updates[SettingKeyDingTalkConnectRedirectURL] = settings.DingTalkConnectRedirectURL
	if settings.DingTalkConnectClientSecret != "" {
		updates[SettingKeyDingTalkConnectClientSecret] = settings.DingTalkConnectClientSecret
	}
	updates[SettingKeyDingTalkConnectCorpRestrictionPolicy] = settings.DingTalkConnectCorpRestrictionPolicy
	updates[SettingKeyDingTalkConnectInternalCorpID] = settings.DingTalkConnectInternalCorpID
	updates[SettingKeyDingTalkConnectBypassRegistration] = strconv.FormatBool(settings.DingTalkConnectBypassRegistration)
	updates[SettingKeyDingTalkConnectSyncCorpEmail] = strconv.FormatBool(settings.DingTalkConnectSyncCorpEmail)
	updates[SettingKeyDingTalkConnectSyncDisplayName] = strconv.FormatBool(settings.DingTalkConnectSyncDisplayName)
	updates[SettingKeyDingTalkConnectSyncDept] = strconv.FormatBool(settings.DingTalkConnectSyncDept)
	updates[SettingKeyDingTalkConnectSyncCorpEmailAttrKey] = settings.DingTalkConnectSyncCorpEmailAttrKey
	updates[SettingKeyDingTalkConnectSyncDisplayNameAttrKey] = settings.DingTalkConnectSyncDisplayNameAttrKey
	updates[SettingKeyDingTalkConnectSyncDeptAttrKey] = settings.DingTalkConnectSyncDeptAttrKey
	updates[SettingKeyDingTalkConnectSyncCorpEmailAttrName] = settings.DingTalkConnectSyncCorpEmailAttrName
	updates[SettingKeyDingTalkConnectSyncDisplayNameAttrName] = settings.DingTalkConnectSyncDisplayNameAttrName
	updates[SettingKeyDingTalkConnectSyncDeptAttrName] = settings.DingTalkConnectSyncDeptAttrName

	// Generic OIDC OAuth 登录
	updates[SettingKeyOIDCConnectEnabled] = strconv.FormatBool(settings.OIDCConnectEnabled)
	updates[SettingKeyOIDCConnectProviderName] = settings.OIDCConnectProviderName
	updates[SettingKeyOIDCConnectClientID] = settings.OIDCConnectClientID
	updates[SettingKeyOIDCConnectIssuerURL] = settings.OIDCConnectIssuerURL
	updates[SettingKeyOIDCConnectDiscoveryURL] = settings.OIDCConnectDiscoveryURL
	updates[SettingKeyOIDCConnectAuthorizeURL] = settings.OIDCConnectAuthorizeURL
	updates[SettingKeyOIDCConnectTokenURL] = settings.OIDCConnectTokenURL
	updates[SettingKeyOIDCConnectUserInfoURL] = settings.OIDCConnectUserInfoURL
	updates[SettingKeyOIDCConnectJWKSURL] = settings.OIDCConnectJWKSURL
	updates[SettingKeyOIDCConnectScopes] = settings.OIDCConnectScopes
	updates[SettingKeyOIDCConnectRedirectURL] = settings.OIDCConnectRedirectURL
	updates[SettingKeyOIDCConnectFrontendRedirectURL] = settings.OIDCConnectFrontendRedirectURL
	updates[SettingKeyOIDCConnectTokenAuthMethod] = settings.OIDCConnectTokenAuthMethod
	updates[SettingKeyOIDCConnectUsePKCE] = strconv.FormatBool(settings.OIDCConnectUsePKCE)
	updates[SettingKeyOIDCConnectValidateIDToken] = strconv.FormatBool(settings.OIDCConnectValidateIDToken)
	updates[SettingKeyOIDCConnectAllowedSigningAlgs] = settings.OIDCConnectAllowedSigningAlgs
	updates[SettingKeyOIDCConnectClockSkewSeconds] = strconv.Itoa(settings.OIDCConnectClockSkewSeconds)
	updates[SettingKeyOIDCConnectRequireEmailVerified] = strconv.FormatBool(settings.OIDCConnectRequireEmailVerified)
	updates[SettingKeyOIDCConnectUserInfoEmailPath] = settings.OIDCConnectUserInfoEmailPath
	updates[SettingKeyOIDCConnectUserInfoIDPath] = settings.OIDCConnectUserInfoIDPath
	updates[SettingKeyOIDCConnectUserInfoUsernamePath] = settings.OIDCConnectUserInfoUsernamePath
	if settings.OIDCConnectClientSecret != "" {
		updates[SettingKeyOIDCConnectClientSecret] = settings.OIDCConnectClientSecret
	}

	// GitHub / Google 邮箱快捷登录
	updates[SettingKeyGitHubOAuthEnabled] = strconv.FormatBool(settings.GitHubOAuthEnabled)
	updates[SettingKeyGitHubOAuthClientID] = strings.TrimSpace(settings.GitHubOAuthClientID)
	updates[SettingKeyGitHubOAuthRedirectURL] = settings.GitHubOAuthRedirectURL
	updates[SettingKeyGitHubOAuthFrontendRedirectURL] = settings.GitHubOAuthFrontendRedirectURL
	if settings.GitHubOAuthClientSecret != "" {
		updates[SettingKeyGitHubOAuthClientSecret] = strings.TrimSpace(settings.GitHubOAuthClientSecret)
	}
	updates[SettingKeyGoogleOAuthEnabled] = strconv.FormatBool(settings.GoogleOAuthEnabled)
	updates[SettingKeyGoogleOAuthClientID] = strings.TrimSpace(settings.GoogleOAuthClientID)
	updates[SettingKeyGoogleOAuthRedirectURL] = settings.GoogleOAuthRedirectURL
	updates[SettingKeyGoogleOAuthFrontendRedirectURL] = settings.GoogleOAuthFrontendRedirectURL
	if settings.GoogleOAuthClientSecret != "" {
		updates[SettingKeyGoogleOAuthClientSecret] = strings.TrimSpace(settings.GoogleOAuthClientSecret)
	}

	// WeChat Connect OAuth 登录
	updates[SettingKeyWeChatConnectEnabled] = strconv.FormatBool(settings.WeChatConnectEnabled)
	updates[SettingKeyWeChatConnectAppID] = settings.WeChatConnectAppID
	updates[SettingKeyWeChatConnectOpenAppID] = settings.WeChatConnectOpenAppID
	updates[SettingKeyWeChatConnectMPAppID] = settings.WeChatConnectMPAppID
	updates[SettingKeyWeChatConnectMobileAppID] = settings.WeChatConnectMobileAppID
	updates[SettingKeyWeChatConnectOpenEnabled] = strconv.FormatBool(settings.WeChatConnectOpenEnabled)
	updates[SettingKeyWeChatConnectMPEnabled] = strconv.FormatBool(settings.WeChatConnectMPEnabled)
	updates[SettingKeyWeChatConnectMobileEnabled] = strconv.FormatBool(settings.WeChatConnectMobileEnabled)
	updates[SettingKeyWeChatConnectMode] = settings.WeChatConnectMode
	updates[SettingKeyWeChatConnectScopes] = settings.WeChatConnectScopes
	updates[SettingKeyWeChatConnectRedirectURL] = settings.WeChatConnectRedirectURL
	updates[SettingKeyWeChatConnectFrontendRedirectURL] = settings.WeChatConnectFrontendRedirectURL
	if settings.WeChatConnectAppSecret != "" {
		updates[SettingKeyWeChatConnectAppSecret] = settings.WeChatConnectAppSecret
	}
	if settings.WeChatConnectOpenAppSecret != "" {
		updates[SettingKeyWeChatConnectOpenAppSecret] = settings.WeChatConnectOpenAppSecret
	}
	if settings.WeChatConnectMPAppSecret != "" {
		updates[SettingKeyWeChatConnectMPAppSecret] = settings.WeChatConnectMPAppSecret
	}
	if settings.WeChatConnectMobileAppSecret != "" {
		updates[SettingKeyWeChatConnectMobileAppSecret] = settings.WeChatConnectMobileAppSecret
	}

	// OEM设置
	updates[SettingKeySiteName] = settings.SiteName
	updates[SettingKeySiteLogo] = settings.SiteLogo
	updates[SettingKeySiteSubtitle] = settings.SiteSubtitle
	updates[SettingKeyAPIBaseURL] = settings.APIBaseURL
	updates[SettingKeyContactInfo] = settings.ContactInfo
	updates[SettingKeyDocURL] = settings.DocURL
	updates[SettingKeyHomeContent] = settings.HomeContent
	updates[SettingKeyHideCcsImportButton] = strconv.FormatBool(settings.HideCcsImportButton)
	updates[SettingKeyPurchaseSubscriptionEnabled] = strconv.FormatBool(settings.PurchaseSubscriptionEnabled)
	updates[SettingKeyPurchaseSubscriptionURL] = strings.TrimSpace(settings.PurchaseSubscriptionURL)
	tableDefaultPageSize, tablePageSizeOptions := normalizeTablePreferences(
		settings.TableDefaultPageSize,
		settings.TablePageSizeOptions,
	)
	updates[SettingKeyTableDefaultPageSize] = strconv.Itoa(tableDefaultPageSize)
	tablePageSizeOptionsJSON, err := json.Marshal(tablePageSizeOptions)
	if err != nil {
		return nil, fmt.Errorf("marshal table page size options: %w", err)
	}
	updates[SettingKeyTablePageSizeOptions] = string(tablePageSizeOptionsJSON)
	updates[SettingKeyCustomMenuItems] = settings.CustomMenuItems
	updates[SettingKeyCustomEndpoints] = settings.CustomEndpoints

	// 默认配置
	updates[SettingKeyDefaultConcurrency] = strconv.Itoa(settings.DefaultConcurrency)
	updates[SettingKeyDefaultBalance] = strconv.FormatFloat(settings.DefaultBalance, 'f', 8, 64)
	updates[SettingKeyDeviceClaimBonusBalance] = strconv.FormatFloat(settings.DeviceClaimBonusBalance, 'f', 8, 64)
	updates[SettingKeyDeviceAutoActivationAffCodes] = strings.TrimSpace(settings.DeviceAutoActivationAffCodes)
	settings.AffiliateRebateRate = clampAffiliateRebateRate(settings.AffiliateRebateRate)
	updates[SettingKeyAffiliateRebateRate] = strconv.FormatFloat(settings.AffiliateRebateRate, 'f', 8, 64)
	if settings.AffiliateRebateFreezeHours < 0 {
		settings.AffiliateRebateFreezeHours = AffiliateRebateFreezeHoursDefault
	}
	if settings.AffiliateRebateFreezeHours > AffiliateRebateFreezeHoursMax {
		settings.AffiliateRebateFreezeHours = AffiliateRebateFreezeHoursMax
	}
	updates[SettingKeyAffiliateRebateFreezeHours] = strconv.Itoa(settings.AffiliateRebateFreezeHours)
	if settings.AffiliateRebateDurationDays < 0 {
		settings.AffiliateRebateDurationDays = AffiliateRebateDurationDaysDefault
	}
	if settings.AffiliateRebateDurationDays > AffiliateRebateDurationDaysMax {
		settings.AffiliateRebateDurationDays = AffiliateRebateDurationDaysMax
	}
	updates[SettingKeyAffiliateRebateDurationDays] = strconv.Itoa(settings.AffiliateRebateDurationDays)
	if settings.AffiliateRebatePerInviteeCap < 0 {
		settings.AffiliateRebatePerInviteeCap = AffiliateRebatePerInviteeCapDefault
	}
	updates[SettingKeyAffiliateRebatePerInviteeCap] = strconv.FormatFloat(settings.AffiliateRebatePerInviteeCap, 'f', 8, 64)
	updates[SettingKeyDefaultUserRPMLimit] = strconv.Itoa(settings.DefaultUserRPMLimit)
	updates[SettingKeyAffiliateRebateRate] = strconv.FormatFloat(clampAffiliateRebateRate(settings.AffiliateRebateRate), 'f', 4, 64)
	defaultSubsJSON, err := json.Marshal(settings.DefaultSubscriptions)
	if err != nil {
		return nil, fmt.Errorf("marshal default subscriptions: %w", err)
	}
	updates[SettingKeyDefaultSubscriptions] = string(defaultSubsJSON)

	// Model fallback configuration
	updates[SettingKeyEnableModelFallback] = strconv.FormatBool(settings.EnableModelFallback)
	updates[SettingKeyFallbackModelAnthropic] = settings.FallbackModelAnthropic
	updates[SettingKeyFallbackModelOpenAI] = settings.FallbackModelOpenAI
	updates[SettingKeyFallbackModelGemini] = settings.FallbackModelGemini
	updates[SettingKeyFallbackModelAntigravity] = settings.FallbackModelAntigravity

	// Identity patch configuration (Claude -> Gemini)
	updates[SettingKeyEnableIdentityPatch] = strconv.FormatBool(settings.EnableIdentityPatch)
	updates[SettingKeyIdentityPatchPrompt] = settings.IdentityPatchPrompt

	// Ops monitoring (vNext)
	updates[SettingKeyOpsMonitoringEnabled] = strconv.FormatBool(settings.OpsMonitoringEnabled)
	updates[SettingKeyOpsRealtimeMonitoringEnabled] = strconv.FormatBool(settings.OpsRealtimeMonitoringEnabled)
	updates[SettingKeyOpsQueryModeDefault] = string(ParseOpsQueryMode(settings.OpsQueryModeDefault))
	if settings.OpsMetricsIntervalSeconds > 0 {
		updates[SettingKeyOpsMetricsIntervalSeconds] = strconv.Itoa(settings.OpsMetricsIntervalSeconds)
	}

	// Channel monitor feature switch
	updates[SettingKeyChannelMonitorEnabled] = strconv.FormatBool(settings.ChannelMonitorEnabled)
	if v := clampChannelMonitorInterval(settings.ChannelMonitorDefaultIntervalSeconds); v > 0 {
		updates[SettingKeyChannelMonitorDefaultIntervalSeconds] = strconv.Itoa(v)
	}

	// Available channels feature switch
	updates[SettingKeyAvailableChannelsEnabled] = strconv.FormatBool(settings.AvailableChannelsEnabled)

	// Affiliate (邀请返利) feature switch
	updates[SettingKeyAffiliateEnabled] = strconv.FormatBool(settings.AffiliateEnabled)

	// 风控中心功能开关
	updates[SettingKeyRiskControlEnabled] = strconv.FormatBool(settings.RiskControlEnabled)

	// cyber 会话屏蔽开关 + TTL
	updates[SettingKeyCyberSessionBlockEnabled] = strconv.FormatBool(settings.CyberSessionBlockEnabled)
	if settings.CyberSessionBlockTTLSeconds > 0 {
		updates[SettingKeyCyberSessionBlockTTLSeconds] = strconv.Itoa(settings.CyberSessionBlockTTLSeconds)
	}

	// Claude Code version check
	updates[SettingKeyMinClaudeCodeVersion] = settings.MinClaudeCodeVersion
	updates[SettingKeyMaxClaudeCodeVersion] = settings.MaxClaudeCodeVersion

	// Antigravity runtime request settings
	// 分组隔离
	updates[SettingKeyAllowUngroupedKeyScheduling] = strconv.FormatBool(settings.AllowUngroupedKeyScheduling)

	// Backend Mode
	updates[SettingKeyBackendModeEnabled] = strconv.FormatBool(settings.BackendModeEnabled)

	// Gateway forwarding behavior
	updates[SettingKeyEnableFingerprintUnification] = strconv.FormatBool(settings.EnableFingerprintUnification)
	updates[SettingKeyEnableMetadataPassthrough] = strconv.FormatBool(settings.EnableMetadataPassthrough)
	updates[SettingKeyEnableCCHSigning] = strconv.FormatBool(settings.EnableCCHSigning)
	updates[SettingKeyEnableClaudeOAuthSystemPromptInjection] = strconv.FormatBool(settings.EnableClaudeOAuthSystemPromptInjection)
	updates[SettingKeyClaudeOAuthSystemPrompt] = settings.ClaudeOAuthSystemPrompt
	if err := ValidateClaudeOAuthSystemPromptBlocksConfig(settings.ClaudeOAuthSystemPromptBlocks); err != nil {
		return nil, err
	}
	updates[SettingKeyClaudeOAuthSystemPromptBlocks] = settings.ClaudeOAuthSystemPromptBlocks
	updates[SettingKeyEnableAnthropicCacheTTL1hInjection] = strconv.FormatBool(settings.EnableAnthropicCacheTTL1hInjection)
	updates[SettingKeyRewriteMessageCacheControl] = strconv.FormatBool(settings.RewriteMessageCacheControl)
	updates[SettingKeyAntigravityUserAgentVersion] = antigravity.NormalizeUserAgentVersion(settings.AntigravityUserAgentVersion)
	updates[SettingKeyOpenAICodexUserAgent] = strings.TrimSpace(settings.OpenAICodexUserAgent)
	// codex_cli_only 加固
	updates[SettingKeyMinCodexVersion] = strings.TrimSpace(settings.MinCodexVersion)
	updates[SettingKeyMaxCodexVersion] = strings.TrimSpace(settings.MaxCodexVersion)
	updates[SettingKeyCodexCLIOnlyBlacklist] = strings.TrimSpace(settings.CodexCLIOnlyBlacklist)
	updates[SettingKeyCodexCLIOnlyWhitelist] = strings.TrimSpace(settings.CodexCLIOnlyWhitelist)
	updates[SettingKeyCodexCLIOnlyAllowAppServerClients] = strconv.FormatBool(settings.CodexCLIOnlyAllowAppServerClients)
	updates[SettingKeyCodexCLIOnlyEngineFingerprintSignals] = strings.TrimSpace(settings.CodexCLIOnlyEngineFingerprintSignals)
	updates[SettingPaymentVisibleMethodAlipaySource] = settings.PaymentVisibleMethodAlipaySource
	updates[SettingPaymentVisibleMethodWxpaySource] = settings.PaymentVisibleMethodWxpaySource
	updates[SettingPaymentVisibleMethodAlipayEnabled] = strconv.FormatBool(settings.PaymentVisibleMethodAlipayEnabled)
	updates[SettingPaymentVisibleMethodWxpayEnabled] = strconv.FormatBool(settings.PaymentVisibleMethodWxpayEnabled)
	updates[openAIAdvancedSchedulerSettingKey] = strconv.FormatBool(settings.OpenAIAdvancedSchedulerEnabled)

	// 余额、订阅到期与账号限额通知
	updates[SettingKeyBalanceLowNotifyEnabled] = strconv.FormatBool(settings.BalanceLowNotifyEnabled)
	updates[SettingKeyBalanceLowNotifyThreshold] = strconv.FormatFloat(settings.BalanceLowNotifyThreshold, 'f', 8, 64)
	updates[SettingKeyBalanceLowNotifyRechargeURL] = settings.BalanceLowNotifyRechargeURL
	updates[SettingKeySubscriptionExpiryNotifyEnabled] = strconv.FormatBool(settings.SubscriptionExpiryNotifyEnabled)
	updates[SettingKeyAccountQuotaNotifyEnabled] = strconv.FormatBool(settings.AccountQuotaNotifyEnabled)
	updates[SettingKeyAccountQuotaNotifyEmails] = MarshalNotifyEmails(settings.AccountQuotaNotifyEmails)

	// 系统全局 platform quota：整体替换语义（null/缺省 = 不限制）。
	if settings.DefaultPlatformQuotas != nil {
		if err := validateDefaultPlatformQuotaMap(settings.DefaultPlatformQuotas); err != nil {
			return nil, err
		}
		blob, err := json.Marshal(settings.DefaultPlatformQuotas)
		if err != nil {
			return nil, fmt.Errorf("marshal default platform quotas: %w", err)
		}
		updates[SettingKeyDefaultPlatformQuotas] = string(blob)
	}

	updates[SettingKeyAllowUserViewErrorRequests] = strconv.FormatBool(settings.AllowUserViewErrorRequests)

	// Telegram bot notifications
	if settings.TelegramBotToken != "" {
		updates[SettingTelegramBotToken] = settings.TelegramBotToken
	} else if !settings.TelegramBotTokenConfigured {
		updates[SettingTelegramBotToken] = ""
	}
	updates[SettingTelegramChatID] = settings.TelegramChatID
	updates[SettingTelegramNotifyNewUser] = strconv.FormatBool(settings.TelegramNotifyNewUser)
	updates[SettingTelegramNotifyAccountError] = strconv.FormatBool(settings.TelegramNotifyAccountError)
	updates[SettingTelegramNotifyAccountExpired] = strconv.FormatBool(settings.TelegramNotifyAccountExpired)
	updates[SettingTelegramNotifyPaymentSuccess] = strconv.FormatBool(settings.TelegramNotifyPaymentSuccess)
	updates[SettingTelegramNotifyPaymentFailed] = strconv.FormatBool(settings.TelegramNotifyPaymentFailed)
	updates[SettingTelegramNotifyRefund] = strconv.FormatBool(settings.TelegramNotifyRefund)
	updates[SettingTelegramNotifySubExpired] = strconv.FormatBool(settings.TelegramNotifySubExpired)
	updates[SettingTelegramNotifyBalanceLow] = strconv.FormatBool(settings.TelegramNotifyBalanceLow)
	updates[SettingTelegramNotifyOpsAlert] = strconv.FormatBool(settings.TelegramNotifyOpsAlert)
	updates[SettingTelegramNotifyProxyExpired] = strconv.FormatBool(settings.TelegramNotifyProxyExpired)

	return updates, nil
}

// validateDefaultPlatformQuotaMap 校验 platform quota map 的合法性：
// 平台名须在 AllowedQuotaPlatforms 白名单内，每个非 nil 上限须 finite 且 >= 0。
// 系统层和 auth-source 层共用此 helper。
func validateDefaultPlatformQuotaMap(m map[string]*DefaultPlatformQuotaSetting) error {
	for platform, pq := range m {
		if !IsAllowedQuotaPlatform(platform) {
			return infraerrors.BadRequest("INVALID_DEFAULT_PLATFORM_QUOTA", fmt.Sprintf("unknown platform %q", platform))
		}
		if pq == nil {
			continue
		}
		for _, v := range []*float64{pq.DailyLimitUSD, pq.WeeklyLimitUSD, pq.MonthlyLimitUSD} {
			if v != nil && (*v < 0 || math.IsNaN(*v) || math.IsInf(*v, 0)) {
				return infraerrors.BadRequest("INVALID_DEFAULT_PLATFORM_QUOTA", "platform quota limit must be a finite non-negative number")
			}
		}
	}
	return nil
}

func (s *SettingService) buildAuthSourceDefaultUpdates(ctx context.Context, settings *AuthSourceDefaultSettings) (map[string]string, error) {
	if settings == nil {
		return nil, nil
	}

	for _, subscriptions := range [][]DefaultSubscriptionSetting{
		settings.Email.Subscriptions,
		settings.LinuxDo.Subscriptions,
		settings.OIDC.Subscriptions,
		settings.WeChat.Subscriptions,
		settings.GitHub.Subscriptions,
		settings.Google.Subscriptions,
		settings.DingTalk.Subscriptions,
	} {
		if err := s.validateDefaultSubscriptionGroups(ctx, subscriptions); err != nil {
			return nil, err
		}
	}

	// 校验各 auth source 的 platform quota map（改动 C：对等系统层校验）
	for _, pgs := range []struct {
		name string
		pq   map[string]*DefaultPlatformQuotaSetting
	}{
		{"email", settings.Email.PlatformQuotas},
		{"linuxdo", settings.LinuxDo.PlatformQuotas},
		{"oidc", settings.OIDC.PlatformQuotas},
		{"wechat", settings.WeChat.PlatformQuotas},
		{"github", settings.GitHub.PlatformQuotas},
		{"google", settings.Google.PlatformQuotas},
		{"dingtalk", settings.DingTalk.PlatformQuotas},
	} {
		if pgs.pq != nil {
			if err := validateDefaultPlatformQuotaMap(pgs.pq); err != nil {
				return nil, err
			}
		}
	}

	updates := make(map[string]string, 36)
	writeProviderDefaultGrantUpdates(updates, emailAuthSourceDefaultKeys, settings.Email)
	writeProviderDefaultGrantUpdates(updates, linuxDoAuthSourceDefaultKeys, settings.LinuxDo)
	writeProviderDefaultGrantUpdates(updates, oidcAuthSourceDefaultKeys, settings.OIDC)
	writeProviderDefaultGrantUpdates(updates, weChatAuthSourceDefaultKeys, settings.WeChat)
	writeProviderDefaultGrantUpdates(updates, gitHubAuthSourceDefaultKeys, settings.GitHub)
	writeProviderDefaultGrantUpdates(updates, googleAuthSourceDefaultKeys, settings.Google)
	writeProviderDefaultGrantUpdates(updates, dingTalkAuthSourceDefaultKeys, settings.DingTalk)
	updates[SettingKeyForceEmailOnThirdPartySignup] = strconv.FormatBool(settings.ForceEmailOnThirdPartySignup)
	return updates, nil
}

func (s *SettingService) refreshCachedSettings(settings *SystemSettings) {
	if settings == nil {
		return
	}

	// 先使 inflight singleflight 失效，再刷新缓存，缩小旧值覆盖新值的竞态窗口
	versionBoundsSF.Forget("version_bounds")
	versionBoundsCache.Store(&cachedVersionBounds{
		min:       settings.MinClaudeCodeVersion,
		max:       settings.MaxClaudeCodeVersion,
		expiresAt: time.Now().Add(versionBoundsCacheTTL).UnixNano(),
	})
	backendModeSF.Forget("backend_mode")
	backendModeCache.Store(&cachedBackendMode{
		value:     settings.BackendModeEnabled,
		expiresAt: time.Now().Add(backendModeCacheTTL).UnixNano(),
	})
	gatewayForwardingSF.Forget("gateway_forwarding")
	gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
		fingerprintUnification:           settings.EnableFingerprintUnification,
		metadataPassthrough:              settings.EnableMetadataPassthrough,
		cchSigning:                       settings.EnableCCHSigning,
		claudeOAuthSystemPromptInjection: settings.EnableClaudeOAuthSystemPromptInjection,
		claudeOAuthSystemPrompt:          settings.ClaudeOAuthSystemPrompt,
		claudeOAuthSystemPromptBlocks:    settings.ClaudeOAuthSystemPromptBlocks,
		anthropicCacheTTL1hInjection:     settings.EnableAnthropicCacheTTL1hInjection,
		rewriteMessageCacheControl:       settings.RewriteMessageCacheControl,
		expiresAt:                        time.Now().Add(gatewayForwardingCacheTTL).UnixNano(),
	})
	s.antigravityUAVersionSF.Forget("antigravity_user_agent_version")
	antigravityUserAgentVersion := antigravity.NormalizeUserAgentVersion(settings.AntigravityUserAgentVersion)
	if antigravityUserAgentVersion == "" {
		antigravityUserAgentVersion = antigravity.GetDefaultUserAgentVersion()
	}
	s.antigravityUAVersionCache.Store(&cachedAntigravityUserAgentVersion{
		version:   antigravityUserAgentVersion,
		expiresAt: time.Now().Add(antigravityUserAgentVersionCacheTTL).UnixNano(),
	})
	s.openAICodexUASF.Forget("openai_codex_user_agent")
	codexUA := strings.TrimSpace(settings.OpenAICodexUserAgent)
	if codexUA == "" {
		codexUA = DefaultOpenAICodexUserAgent
	}
	s.openAICodexUACache.Store(&cachedOpenAICodexUserAgent{
		value:     codexUA,
		expiresAt: time.Now().Add(openAICodexUserAgentCacheTTL).UnixNano(),
	})
	openAIAdvancedSchedulerSettingSF.Forget(openAIAdvancedSchedulerSettingKey)
	openAIAdvancedSchedulerSettingCache.Store(&cachedOpenAIAdvancedSchedulerSetting{
		enabled:   settings.OpenAIAdvancedSchedulerEnabled,
		expiresAt: time.Now().Add(openAIAdvancedSchedulerSettingCacheTTL).UnixNano(),
	})
	// Invalidate the quota auto-pause cache and let the next read trigger a fresh load.
	// We can't know from here whether ops_advanced_settings was also touched, so be
	// defensive: store an expired entry — GetOpenAIQuotaAutoPauseSettings will serve
	// stale and kick off an async refresh, never blocking the request that follows.
	s.openAIQuotaAutoPauseSettingsSF.Forget(openAIQuotaAutoPauseSettingsRefreshKey)
	if cached, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings); cached != nil {
		s.openAIQuotaAutoPauseSettingsCache.Store(&cachedOpenAIQuotaAutoPauseSettings{
			settings:  cached.settings,
			expiresAt: 0,
		})
	}
	if s.cfg != nil {
		s.cfg.SetTrustForwardedIPForAPIKeyACL(settings.APIKeyACLTrustForwardedIP)
	}
	// codex_cli_only 加固策略缓存：设置更新后强制下次重载（涉及 4 个键 + JSON 解析，直接置过期）。
	s.codexRestrictionPolicySF.Forget("codex_restriction_policy")
	s.codexRestrictionPolicyCache.Store(&cachedCodexRestrictionPolicy{expiresAt: 0})
	if s.onUpdate != nil {
		s.onUpdate() // Invalidate cache after settings update
	}
}

func (s *SettingService) defaultRewriteMessageCacheControl() bool {
	return false
}

func (s *SettingService) validateDefaultSubscriptionGroups(ctx context.Context, items []DefaultSubscriptionSetting) error {
	if len(items) == 0 {
		return nil
	}

	checked := make(map[int64]struct{}, len(items))
	for _, item := range items {
		if item.GroupID <= 0 {
			continue
		}
		if _, ok := checked[item.GroupID]; ok {
			return ErrDefaultSubGroupDuplicate.WithMetadata(map[string]string{
				"group_id": strconv.FormatInt(item.GroupID, 10),
			})
		}
		checked[item.GroupID] = struct{}{}
		if s.defaultSubGroupReader == nil {
			continue
		}

		group, err := s.defaultSubGroupReader.GetByID(ctx, item.GroupID)
		if err != nil {
			if errors.Is(err, ErrGroupNotFound) {
				return ErrDefaultSubGroupInvalid.WithMetadata(map[string]string{
					"group_id": strconv.FormatInt(item.GroupID, 10),
				})
			}
			return fmt.Errorf("get default subscription group %d: %w", item.GroupID, err)
		}
		if !group.IsSubscriptionType() {
			return ErrDefaultSubGroupInvalid.WithMetadata(map[string]string{
				"group_id": strconv.FormatInt(item.GroupID, 10),
			})
		}
	}

	return nil
}

func (s *SettingService) GetEmailOAuthProviderConfig(ctx context.Context, provider string) (config.EmailOAuthProviderConfig, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider != "github" && provider != "google" {
		return config.EmailOAuthProviderConfig{}, infraerrors.NotFound("OAUTH_PROVIDER_NOT_FOUND", "oauth provider not found")
	}
	keys := []string{
		SettingKeyGitHubOAuthEnabled,
		SettingKeyGitHubOAuthClientID,
		SettingKeyGitHubOAuthClientSecret,
		SettingKeyGitHubOAuthRedirectURL,
		SettingKeyGitHubOAuthFrontendRedirectURL,
		SettingKeyGoogleOAuthEnabled,
		SettingKeyGoogleOAuthClientID,
		SettingKeyGoogleOAuthClientSecret,
		SettingKeyGoogleOAuthRedirectURL,
		SettingKeyGoogleOAuthFrontendRedirectURL,
	}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return config.EmailOAuthProviderConfig{}, fmt.Errorf("get email oauth settings: %w", err)
	}
	cfg := s.effectiveEmailOAuthConfig(settings, provider)
	if !cfg.Enabled {
		return config.EmailOAuthProviderConfig{}, infraerrors.NotFound("OAUTH_DISABLED", "oauth login is disabled")
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return config.EmailOAuthProviderConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth client id not configured")
	}
	if strings.TrimSpace(cfg.ClientSecret) == "" {
		return config.EmailOAuthProviderConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth client secret not configured")
	}
	for label, rawURL := range map[string]string{
		"authorize": cfg.AuthorizeURL,
		"token":     cfg.TokenURL,
		"userinfo":  cfg.UserInfoURL,
		"redirect":  cfg.RedirectURL,
	} {
		if strings.TrimSpace(rawURL) == "" {
			return config.EmailOAuthProviderConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth "+label+" url not configured")
		}
		if err := config.ValidateAbsoluteHTTPURL(rawURL); err != nil {
			return config.EmailOAuthProviderConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth "+label+" url invalid")
		}
	}
	if strings.TrimSpace(cfg.EmailsURL) != "" {
		if err := config.ValidateAbsoluteHTTPURL(cfg.EmailsURL); err != nil {
			return config.EmailOAuthProviderConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth emails url invalid")
		}
	}
	if err := config.ValidateFrontendRedirectURL(cfg.FrontendRedirectURL); err != nil {
		return config.EmailOAuthProviderConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth frontend redirect url invalid")
	}
	return cfg, nil
}

// IsRegistrationEnabled 检查是否开放注册
func (s *SettingService) IsRegistrationEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRegistrationEnabled)
	if err != nil {
		// 安全默认：如果设置不存在或查询出错，默认关闭注册
		return false
	}
	return value == "true"
}

// IsBackendModeEnabled checks if backend mode is enabled
// Uses in-process atomic.Value cache with 60s TTL, zero-lock hot path
func (s *SettingService) IsBackendModeEnabled(ctx context.Context) bool {
	if cached, ok := backendModeCache.Load().(*cachedBackendMode); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.value
		}
	}
	result, _, _ := backendModeSF.Do("backend_mode", func() (any, error) {
		if cached, ok := backendModeCache.Load().(*cachedBackendMode); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return cached.value, nil
			}
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), backendModeDBTimeout)
		defer cancel()
		value, err := s.settingRepo.GetValue(dbCtx, SettingKeyBackendModeEnabled)
		if err != nil {
			if errors.Is(err, ErrSettingNotFound) {
				// Setting not yet created (fresh install) - default to disabled with full TTL
				backendModeCache.Store(&cachedBackendMode{
					value:     false,
					expiresAt: time.Now().Add(backendModeCacheTTL).UnixNano(),
				})
				return false, nil
			}
			slog.Warn("failed to get backend_mode_enabled setting", "error", err)
			backendModeCache.Store(&cachedBackendMode{
				value:     false,
				expiresAt: time.Now().Add(backendModeErrorTTL).UnixNano(),
			})
			return false, nil
		}
		enabled := value == "true"
		backendModeCache.Store(&cachedBackendMode{
			value:     enabled,
			expiresAt: time.Now().Add(backendModeCacheTTL).UnixNano(),
		})
		return enabled, nil
	})
	if val, ok := result.(bool); ok {
		return val
	}
	return false
}

type gatewayForwardingSettingsResult struct {
	fp, mp, cch, claudeOAuthSystemPromptInjection, cacheTTL1h, rewriteMessageCacheControl bool
	claudeOAuthSystemPrompt, claudeOAuthSystemPromptBlocks                                string
}

func (s *SettingService) getGatewayForwardingSettingsCached(ctx context.Context) gatewayForwardingSettingsResult {
	if cached, ok := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
		if time.Now().UnixNano() < cached.expiresAt {
			return gatewayForwardingSettingsResult{
				fp:                               cached.fingerprintUnification,
				mp:                               cached.metadataPassthrough,
				cch:                              cached.cchSigning,
				claudeOAuthSystemPromptInjection: cached.claudeOAuthSystemPromptInjection,
				claudeOAuthSystemPrompt:          cached.claudeOAuthSystemPrompt,
				claudeOAuthSystemPromptBlocks:    cached.claudeOAuthSystemPromptBlocks,
				cacheTTL1h:                       cached.anthropicCacheTTL1hInjection,
				rewriteMessageCacheControl:       cached.rewriteMessageCacheControl,
			}
		}
	}
	val, _, _ := gatewayForwardingSF.Do("gateway_forwarding", func() (any, error) {
		if cached, ok := gatewayForwardingCache.Load().(*cachedGatewayForwardingSettings); ok && cached != nil {
			if time.Now().UnixNano() < cached.expiresAt {
				return gatewayForwardingSettingsResult{
					fp:                               cached.fingerprintUnification,
					mp:                               cached.metadataPassthrough,
					cch:                              cached.cchSigning,
					claudeOAuthSystemPromptInjection: cached.claudeOAuthSystemPromptInjection,
					claudeOAuthSystemPrompt:          cached.claudeOAuthSystemPrompt,
					claudeOAuthSystemPromptBlocks:    cached.claudeOAuthSystemPromptBlocks,
					cacheTTL1h:                       cached.anthropicCacheTTL1hInjection,
					rewriteMessageCacheControl:       cached.rewriteMessageCacheControl,
				}, nil
			}
		}
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), gatewayForwardingDBTimeout)
		defer cancel()
		values, err := s.settingRepo.GetMultiple(dbCtx, []string{
			SettingKeyEnableFingerprintUnification,
			SettingKeyEnableMetadataPassthrough,
			SettingKeyEnableCCHSigning,
			SettingKeyEnableClaudeOAuthSystemPromptInjection,
			SettingKeyClaudeOAuthSystemPrompt,
			SettingKeyClaudeOAuthSystemPromptBlocks,
			SettingKeyEnableAnthropicCacheTTL1hInjection,
			SettingKeyRewriteMessageCacheControl,
		})
		if err != nil {
			slog.Warn("failed to get gateway forwarding settings", "error", err)
			gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
				fingerprintUnification:           true,
				metadataPassthrough:              false,
				cchSigning:                       false,
				claudeOAuthSystemPromptInjection: true,
				anthropicCacheTTL1hInjection:     false,
				rewriteMessageCacheControl:       s.defaultRewriteMessageCacheControl(),
				expiresAt:                        time.Now().Add(gatewayForwardingErrorTTL).UnixNano(),
			})
			return gatewayForwardingSettingsResult{fp: true, claudeOAuthSystemPromptInjection: true, rewriteMessageCacheControl: s.defaultRewriteMessageCacheControl()}, nil
		}
		fp := true
		if v, ok := values[SettingKeyEnableFingerprintUnification]; ok && v != "" {
			fp = v == "true"
		}
		mp := values[SettingKeyEnableMetadataPassthrough] == "true"
		cch := values[SettingKeyEnableCCHSigning] == "true"
		systemPromptInjection := true
		if v, ok := values[SettingKeyEnableClaudeOAuthSystemPromptInjection]; ok && v != "" {
			systemPromptInjection = v == "true"
		}
		systemPrompt := values[SettingKeyClaudeOAuthSystemPrompt]
		systemPromptBlocks := values[SettingKeyClaudeOAuthSystemPromptBlocks]
		cacheTTL1h := values[SettingKeyEnableAnthropicCacheTTL1hInjection] == "true"
		rewriteMessageCacheControl := s.defaultRewriteMessageCacheControl()
		if v, ok := values[SettingKeyRewriteMessageCacheControl]; ok && v != "" {
			rewriteMessageCacheControl = v == "true"
		}
		gatewayForwardingCache.Store(&cachedGatewayForwardingSettings{
			fingerprintUnification:           fp,
			metadataPassthrough:              mp,
			cchSigning:                       cch,
			claudeOAuthSystemPromptInjection: systemPromptInjection,
			claudeOAuthSystemPrompt:          systemPrompt,
			claudeOAuthSystemPromptBlocks:    systemPromptBlocks,
			anthropicCacheTTL1hInjection:     cacheTTL1h,
			rewriteMessageCacheControl:       rewriteMessageCacheControl,
			expiresAt:                        time.Now().Add(gatewayForwardingCacheTTL).UnixNano(),
		})
		return gatewayForwardingSettingsResult{
			fp:                               fp,
			mp:                               mp,
			cch:                              cch,
			claudeOAuthSystemPromptInjection: systemPromptInjection,
			claudeOAuthSystemPrompt:          systemPrompt,
			claudeOAuthSystemPromptBlocks:    systemPromptBlocks,
			cacheTTL1h:                       cacheTTL1h,
			rewriteMessageCacheControl:       rewriteMessageCacheControl,
		}, nil
	})
	if r, ok := val.(gatewayForwardingSettingsResult); ok {
		return r
	}
	return gatewayForwardingSettingsResult{fp: true, claudeOAuthSystemPromptInjection: true}
}

// GetGatewayForwardingSettings returns cached gateway forwarding settings.
// Uses in-process atomic.Value cache with 60s TTL, zero-lock hot path.
// Returns (fingerprintUnification, metadataPassthrough, cchSigning).
func (s *SettingService) GetGatewayForwardingSettings(ctx context.Context) (fingerprintUnification, metadataPassthrough, cchSigning bool) {
	result := s.getGatewayForwardingSettingsCached(ctx)
	return result.fp, result.mp, result.cch
}

// IsAnthropicCacheTTL1hInjectionEnabled 检查是否对 Anthropic OAuth/SetupToken 请求体注入 1h cache_control ttl。
func (s *SettingService) IsAnthropicCacheTTL1hInjectionEnabled(ctx context.Context) bool {
	return s.getGatewayForwardingSettingsCached(ctx).cacheTTL1h
}

// IsRewriteMessageCacheControlEnabled 检查是否启用 messages cache_control 改写。
func (s *SettingService) IsRewriteMessageCacheControlEnabled(ctx context.Context) bool {
	return s.getGatewayForwardingSettingsCached(ctx).rewriteMessageCacheControl
}

// GetClaudeOAuthSystemPromptInjectionSettings returns the Claude OAuth mimic
// system block switch, legacy custom expansion prompt, and configurable blocks JSON.
// Empty values mean use the built-in Claude Code default blocks.
func (s *SettingService) GetClaudeOAuthSystemPromptInjectionSettings(ctx context.Context) (enabled bool, prompt string, blocks string) {
	result := s.getGatewayForwardingSettingsCached(ctx)
	return result.claudeOAuthSystemPromptInjection, result.claudeOAuthSystemPrompt, result.claudeOAuthSystemPromptBlocks
}

// IsEmailVerifyEnabled 检查是否开启邮件验证
func (s *SettingService) IsEmailVerifyEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyEmailVerifyEnabled)
	if err != nil {
		return false
	}
	return value == "true"
}

// GetRegistrationEmailSuffixWhitelist returns normalized registration email suffix whitelist.
func (s *SettingService) GetRegistrationEmailSuffixWhitelist(ctx context.Context) []string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRegistrationEmailSuffixWhitelist)
	if err != nil {
		return []string{}
	}
	return ParseRegistrationEmailSuffixWhitelist(value)
}

// IsPromoCodeEnabled 检查是否启用优惠码功能
func (s *SettingService) IsPromoCodeEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyPromoCodeEnabled)
	if err != nil {
		return true // 默认启用
	}
	return value != "false"
}

// IsInvitationCodeEnabled 检查是否启用邀请码注册功能
func (s *SettingService) IsInvitationCodeEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyInvitationCodeEnabled)
	if err != nil {
		return false // 默认关闭
	}
	return value == "true"
}

// GetCustomMenuItemsRaw returns the raw JSON string of custom_menu_items setting.
func (s *SettingService) GetCustomMenuItemsRaw(ctx context.Context) string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyCustomMenuItems)
	if err != nil {
		return "[]"
	}
	return value
}

// IsAffiliateEnabled 检查是否启用邀请返利功能（总开关）
func (s *SettingService) IsAffiliateEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateEnabled)
	if err != nil {
		return false // 默认关闭
	}
	return value == "true"
}

// GetAffiliateRebateRatePercent 读取并 clamp 全局返利比例。
// 解析失败、缺失或越界都回退到 AffiliateRebateRateDefault — 该比例从不抛错，
// 调用方只关心一个可用的数值。
func (s *SettingService) GetAffiliateRebateRatePercent(ctx context.Context) float64 {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateRebateRate)
	if err != nil {
		return AffiliateRebateRateDefault
	}
	rate, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) {
		return AffiliateRebateRateDefault
	}
	return clampAffiliateRebateRate(rate)
}

// GetAffiliateRebateFreezeHours 返回返利冻结期（小时）。
// 返回 0 表示不冻结（向后兼容）。
func (s *SettingService) GetAffiliateRebateFreezeHours(ctx context.Context) int {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateRebateFreezeHours)
	if err != nil {
		return AffiliateRebateFreezeHoursDefault
	}
	hours, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || hours < 0 {
		return AffiliateRebateFreezeHoursDefault
	}
	if hours > AffiliateRebateFreezeHoursMax {
		return AffiliateRebateFreezeHoursMax
	}
	return hours
}

// GetAffiliateRebateDurationDays 返回返利有效期（天）。
// 返回 0 表示永久有效。
func (s *SettingService) GetAffiliateRebateDurationDays(ctx context.Context) int {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateRebateDurationDays)
	if err != nil {
		return AffiliateRebateDurationDaysDefault
	}
	days, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || days < 0 {
		return AffiliateRebateDurationDaysDefault
	}
	if days > AffiliateRebateDurationDaysMax {
		return AffiliateRebateDurationDaysMax
	}
	return days
}

// GetAffiliateRebatePerInviteeCap 返回单人返利上限。
// 返回 0 表示无上限。
func (s *SettingService) GetAffiliateRebatePerInviteeCap(ctx context.Context) float64 {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAffiliateRebatePerInviteeCap)
	if err != nil {
		return AffiliateRebatePerInviteeCapDefault
	}
	cap, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	if err != nil || cap < 0 || math.IsNaN(cap) || math.IsInf(cap, 0) {
		return AffiliateRebatePerInviteeCapDefault
	}
	return cap
}

// IsPasswordResetEnabled 检查是否启用密码重置功能
// 要求：必须同时开启邮件验证
func (s *SettingService) IsPasswordResetEnabled(ctx context.Context) bool {
	// Password reset requires email verification to be enabled
	if !s.IsEmailVerifyEnabled(ctx) {
		return false
	}
	value, err := s.settingRepo.GetValue(ctx, SettingKeyPasswordResetEnabled)
	if err != nil {
		return false // 默认关闭
	}
	return value == "true"
}

// IsTotpEnabled 检查是否启用 TOTP 双因素认证功能
func (s *SettingService) IsTotpEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyTotpEnabled)
	if err != nil {
		return false // 默认关闭
	}
	return value == "true"
}

// IsTotpEncryptionKeyConfigured 检查 TOTP 加密密钥是否已手动配置
// 只有手动配置了密钥才允许在管理后台启用 TOTP 功能
func (s *SettingService) IsTotpEncryptionKeyConfigured() bool {
	return s.cfg.Totp.EncryptionKeyConfigured
}

// GetSiteName 获取网站名称
func (s *SettingService) GetSiteName(ctx context.Context) string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeySiteName)
	if err != nil || value == "" {
		return "Sub2API"
	}
	return value
}

// GetDefaultConcurrency 获取默认并发量
func (s *SettingService) GetDefaultConcurrency(ctx context.Context) int {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyDefaultConcurrency)
	if err != nil {
		return s.cfg.Default.UserConcurrency
	}
	if v, err := strconv.Atoi(value); err == nil && v > 0 {
		return v
	}
	return s.cfg.Default.UserConcurrency
}

// GetDefaultBalance 获取默认余额
func (s *SettingService) GetDefaultBalance(ctx context.Context) float64 {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyDefaultBalance)
	if err != nil {
		return s.cfg.Default.UserBalance
	}
	if v, err := strconv.ParseFloat(value, 64); err == nil && v >= 0 {
		return v
	}
	return s.cfg.Default.UserBalance
}

// GetDefaultUserRPMLimit 获取新用户默认 RPM 限制（0 = 不限制）。未配置则返回 0。
func (s *SettingService) GetDefaultUserRPMLimit(ctx context.Context) int {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyDefaultUserRPMLimit)
	if err != nil || value == "" {
		return 0
	}
	if v, err := strconv.Atoi(value); err == nil && v >= 0 {
		return v
	}
	return 0
}

// GetDefaultSubscriptions 获取新用户默认订阅配置列表。
func (s *SettingService) GetDefaultSubscriptions(ctx context.Context) []DefaultSubscriptionSetting {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyDefaultSubscriptions)
	if err != nil {
		return nil
	}
	return parseDefaultSubscriptions(value)
}

func (s *SettingService) GetAuthSourceDefaultSettings(ctx context.Context) (*AuthSourceDefaultSettings, error) {
	keys := []string{
		SettingKeyAuthSourceDefaultEmailBalance,
		SettingKeyAuthSourceDefaultEmailConcurrency,
		SettingKeyAuthSourceDefaultEmailSubscriptions,
		SettingKeyAuthSourceDefaultEmailGrantOnSignup,
		SettingKeyAuthSourceDefaultEmailGrantOnFirstBind,
		SettingKeyAuthSourceDefaultLinuxDoBalance,
		SettingKeyAuthSourceDefaultLinuxDoConcurrency,
		SettingKeyAuthSourceDefaultLinuxDoSubscriptions,
		SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup,
		SettingKeyAuthSourceDefaultLinuxDoGrantOnFirstBind,
		SettingKeyAuthSourceDefaultOIDCBalance,
		SettingKeyAuthSourceDefaultOIDCConcurrency,
		SettingKeyAuthSourceDefaultOIDCSubscriptions,
		SettingKeyAuthSourceDefaultOIDCGrantOnSignup,
		SettingKeyAuthSourceDefaultOIDCGrantOnFirstBind,
		SettingKeyAuthSourceDefaultWeChatBalance,
		SettingKeyAuthSourceDefaultWeChatConcurrency,
		SettingKeyAuthSourceDefaultWeChatSubscriptions,
		SettingKeyAuthSourceDefaultWeChatGrantOnSignup,
		SettingKeyAuthSourceDefaultWeChatGrantOnFirstBind,
		SettingKeyAuthSourceDefaultGitHubBalance,
		SettingKeyAuthSourceDefaultGitHubConcurrency,
		SettingKeyAuthSourceDefaultGitHubSubscriptions,
		SettingKeyAuthSourceDefaultGitHubGrantOnSignup,
		SettingKeyAuthSourceDefaultGitHubGrantOnFirstBind,
		SettingKeyAuthSourceDefaultGoogleBalance,
		SettingKeyAuthSourceDefaultGoogleConcurrency,
		SettingKeyAuthSourceDefaultGoogleSubscriptions,
		SettingKeyAuthSourceDefaultGoogleGrantOnSignup,
		SettingKeyAuthSourceDefaultGoogleGrantOnFirstBind,
		SettingKeyAuthSourceDefaultDingTalkBalance,
		SettingKeyAuthSourceDefaultDingTalkConcurrency,
		SettingKeyAuthSourceDefaultDingTalkSubscriptions,
		SettingKeyAuthSourceDefaultDingTalkGrantOnSignup,
		SettingKeyAuthSourceDefaultDingTalkGrantOnFirstBind,
		SettingKeyAuthSourcePlatformQuotas("email"),
		SettingKeyAuthSourcePlatformQuotas("linuxdo"),
		SettingKeyAuthSourcePlatformQuotas("oidc"),
		SettingKeyAuthSourcePlatformQuotas("wechat"),
		SettingKeyAuthSourcePlatformQuotas("github"),
		SettingKeyAuthSourcePlatformQuotas("google"),
		SettingKeyAuthSourcePlatformQuotas("dingtalk"),
		SettingKeyForceEmailOnThirdPartySignup,
	}

	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return nil, fmt.Errorf("get auth source default settings: %w", err)
	}

	return &AuthSourceDefaultSettings{
		Email:                        parseProviderDefaultGrantSettings(settings, emailAuthSourceDefaultKeys),
		LinuxDo:                      parseProviderDefaultGrantSettings(settings, linuxDoAuthSourceDefaultKeys),
		OIDC:                         parseProviderDefaultGrantSettings(settings, oidcAuthSourceDefaultKeys),
		WeChat:                       parseProviderDefaultGrantSettings(settings, weChatAuthSourceDefaultKeys),
		GitHub:                       parseProviderDefaultGrantSettings(settings, gitHubAuthSourceDefaultKeys),
		Google:                       parseProviderDefaultGrantSettings(settings, googleAuthSourceDefaultKeys),
		DingTalk:                     parseProviderDefaultGrantSettings(settings, dingTalkAuthSourceDefaultKeys),
		ForceEmailOnThirdPartySignup: settings[SettingKeyForceEmailOnThirdPartySignup] == "true",
	}, nil
}

func (s *SettingService) ResolveAuthSourceGrantSettings(ctx context.Context, signupSource string, firstBind bool) (ProviderDefaultGrantSettings, bool, error) {
	result := ProviderDefaultGrantSettings{
		Balance:       s.GetDefaultBalance(ctx),
		Concurrency:   s.GetDefaultConcurrency(ctx),
		Subscriptions: s.GetDefaultSubscriptions(ctx),
	}

	defaults, err := s.GetAuthSourceDefaultSettings(ctx)
	if err != nil {
		return result, false, err
	}

	providerDefaults, ok := authSourceSignupSettings(defaults, signupSource)
	if !ok {
		return result, false, nil
	}

	enabled := providerDefaults.GrantOnSignup
	if firstBind {
		enabled = providerDefaults.GrantOnFirstBind
	}
	if !enabled {
		return result, false, nil
	}

	return mergeProviderDefaultGrantSettings(result, providerDefaults), true, nil
}

func (s *SettingService) UpdateAuthSourceDefaultSettings(ctx context.Context, settings *AuthSourceDefaultSettings) error {
	updates, err := s.buildAuthSourceDefaultUpdates(ctx, settings)
	if err != nil {
		return err
	}
	if len(updates) == 0 {
		return nil
	}

	if err := s.settingRepo.SetMultiple(ctx, updates); err != nil {
		return fmt.Errorf("update auth source default settings: %w", err)
	}
	return nil
}

// InitializeDefaultSettings 初始化默认设置
func (s *SettingService) InitializeDefaultSettings(ctx context.Context) error {
	// 检查是否已有设置
	_, err := s.settingRepo.GetValue(ctx, SettingKeyRegistrationEnabled)
	if err == nil {
		// 已有设置，不需要初始化
		return nil
	}
	if !errors.Is(err, ErrSettingNotFound) {
		return fmt.Errorf("check existing settings: %w", err)
	}

	oidcUsePKCEDefault := true
	oidcValidateIDTokenDefault := true
	if s != nil && s.cfg != nil {
		if s.cfg.OIDC.UsePKCEExplicit {
			oidcUsePKCEDefault = s.cfg.OIDC.UsePKCE
		}
		if s.cfg.OIDC.ValidateIDTokenExplicit {
			oidcValidateIDTokenDefault = s.cfg.OIDC.ValidateIDToken
		}
	}
	loginAgreementDocumentsJSON, err := marshalLoginAgreementDocuments(defaultLoginAgreementDocuments())
	if err != nil {
		return err
	}

	// 初始化默认设置
	defaults := map[string]string{
		SettingKeyRegistrationEnabled:                       "true",
		SettingKeyEmailVerifyEnabled:                        "false",
		SettingKeyRegistrationEmailSuffixWhitelist:          "[]",
		SettingKeyPromoCodeEnabled:                          "true", // 默认启用优惠码功能
		SettingKeyLoginAgreementEnabled:                     "false",
		SettingKeyLoginAgreementMode:                        defaultLoginAgreementMode,
		SettingKeyLoginAgreementUpdatedAt:                   defaultLoginAgreementDate,
		SettingKeyLoginAgreementDocuments:                   loginAgreementDocumentsJSON,
		SettingKeyAPIKeyACLTrustForwardedIP:                 "false",
		SettingKeySiteName:                                  "Sub2API",
		SettingKeySiteLogo:                                  "",
		SettingKeyPurchaseSubscriptionEnabled:               "false",
		SettingKeyPurchaseSubscriptionURL:                   "",
		SettingKeyTableDefaultPageSize:                      "20",
		SettingKeyTablePageSizeOptions:                      "[10,20,50,100]",
		SettingKeyCustomMenuItems:                           "[]",
		SettingKeyCustomEndpoints:                           "[]",
		SettingKeyWeChatConnectEnabled:                      "false",
		SettingKeyWeChatConnectAppID:                        "",
		SettingKeyWeChatConnectAppSecret:                    "",
		SettingKeyWeChatConnectOpenAppID:                    "",
		SettingKeyWeChatConnectOpenAppSecret:                "",
		SettingKeyWeChatConnectMPAppID:                      "",
		SettingKeyWeChatConnectMPAppSecret:                  "",
		SettingKeyWeChatConnectMobileAppID:                  "",
		SettingKeyWeChatConnectMobileAppSecret:              "",
		SettingKeyWeChatConnectOpenEnabled:                  "false",
		SettingKeyWeChatConnectMPEnabled:                    "false",
		SettingKeyWeChatConnectMobileEnabled:                "false",
		SettingKeyWeChatConnectMode:                         "open",
		SettingKeyWeChatConnectScopes:                       "snsapi_login",
		SettingKeyWeChatConnectRedirectURL:                  "",
		SettingKeyWeChatConnectFrontendRedirectURL:          defaultWeChatConnectFrontend,
		SettingKeyGitHubOAuthEnabled:                        "false",
		SettingKeyGitHubOAuthClientID:                       "",
		SettingKeyGitHubOAuthClientSecret:                   "",
		SettingKeyGitHubOAuthRedirectURL:                    "",
		SettingKeyGitHubOAuthFrontendRedirectURL:            defaultGitHubOAuthFrontend,
		SettingKeyGoogleOAuthEnabled:                        "false",
		SettingKeyGoogleOAuthClientID:                       "",
		SettingKeyGoogleOAuthClientSecret:                   "",
		SettingKeyGoogleOAuthRedirectURL:                    "",
		SettingKeyGoogleOAuthFrontendRedirectURL:            defaultGoogleOAuthFrontend,
		SettingKeyOIDCConnectEnabled:                        "false",
		SettingKeyOIDCConnectProviderName:                   "OIDC",
		SettingKeyOIDCConnectClientID:                       "",
		SettingKeyOIDCConnectClientSecret:                   "",
		SettingKeyOIDCConnectIssuerURL:                      "",
		SettingKeyOIDCConnectDiscoveryURL:                   "",
		SettingKeyOIDCConnectAuthorizeURL:                   "",
		SettingKeyOIDCConnectTokenURL:                       "",
		SettingKeyOIDCConnectUserInfoURL:                    "",
		SettingKeyOIDCConnectJWKSURL:                        "",
		SettingKeyOIDCConnectScopes:                         "openid email profile",
		SettingKeyOIDCConnectRedirectURL:                    "",
		SettingKeyOIDCConnectFrontendRedirectURL:            "/auth/oidc/callback",
		SettingKeyOIDCConnectTokenAuthMethod:                "client_secret_post",
		SettingKeyOIDCConnectUsePKCE:                        strconv.FormatBool(oidcUsePKCEDefault),
		SettingKeyOIDCConnectValidateIDToken:                strconv.FormatBool(oidcValidateIDTokenDefault),
		SettingKeyOIDCConnectAllowedSigningAlgs:             "RS256,ES256,PS256",
		SettingKeyOIDCConnectClockSkewSeconds:               "120",
		SettingKeyOIDCConnectRequireEmailVerified:           "false",
		SettingKeyOIDCConnectUserInfoEmailPath:              "",
		SettingKeyOIDCConnectUserInfoIDPath:                 "",
		SettingKeyOIDCConnectUserInfoUsernamePath:           "",
		SettingKeyDefaultConcurrency:                        strconv.Itoa(s.cfg.Default.UserConcurrency),
		SettingKeyDefaultBalance:                            strconv.FormatFloat(s.cfg.Default.UserBalance, 'f', 8, 64),
		SettingKeyDeviceClaimBonusBalance:                   "0",
		SettingKeyAffiliateRebateRate:                       strconv.FormatFloat(AffiliateRebateRateDefault, 'f', 8, 64),
		SettingKeyAffiliateRebateFreezeHours:                strconv.Itoa(AffiliateRebateFreezeHoursDefault),
		SettingKeyAffiliateRebateDurationDays:               strconv.Itoa(AffiliateRebateDurationDaysDefault),
		SettingKeyAffiliateRebatePerInviteeCap:              strconv.FormatFloat(AffiliateRebatePerInviteeCapDefault, 'f', 2, 64),
		SettingKeyDefaultUserRPMLimit:                       "0",
		SettingKeyDefaultSubscriptions:                      "[]",
		SettingKeyAuthSourceDefaultEmailBalance:             "0",
		SettingKeyAuthSourceDefaultEmailConcurrency:         "5",
		SettingKeyAuthSourceDefaultEmailSubscriptions:       "[]",
		SettingKeyAuthSourceDefaultEmailGrantOnSignup:       "false",
		SettingKeyAuthSourceDefaultEmailGrantOnFirstBind:    "false",
		SettingKeyAuthSourceDefaultLinuxDoBalance:           "0",
		SettingKeyAuthSourceDefaultLinuxDoConcurrency:       "5",
		SettingKeyAuthSourceDefaultLinuxDoSubscriptions:     "[]",
		SettingKeyAuthSourceDefaultLinuxDoGrantOnSignup:     "false",
		SettingKeyAuthSourceDefaultLinuxDoGrantOnFirstBind:  "false",
		SettingKeyAuthSourceDefaultOIDCBalance:              "0",
		SettingKeyAuthSourceDefaultOIDCConcurrency:          "5",
		SettingKeyAuthSourceDefaultOIDCSubscriptions:        "[]",
		SettingKeyAuthSourceDefaultOIDCGrantOnSignup:        "false",
		SettingKeyAuthSourceDefaultOIDCGrantOnFirstBind:     "false",
		SettingKeyAuthSourceDefaultWeChatBalance:            "0",
		SettingKeyAuthSourceDefaultWeChatConcurrency:        "5",
		SettingKeyAuthSourceDefaultWeChatSubscriptions:      "[]",
		SettingKeyAuthSourceDefaultWeChatGrantOnSignup:      "false",
		SettingKeyAuthSourceDefaultWeChatGrantOnFirstBind:   "false",
		SettingKeyAuthSourceDefaultGitHubBalance:            "0",
		SettingKeyAuthSourceDefaultGitHubConcurrency:        "5",
		SettingKeyAuthSourceDefaultGitHubSubscriptions:      "[]",
		SettingKeyAuthSourceDefaultGitHubGrantOnSignup:      "false",
		SettingKeyAuthSourceDefaultGitHubGrantOnFirstBind:   "false",
		SettingKeyAuthSourceDefaultGoogleBalance:            "0",
		SettingKeyAuthSourceDefaultGoogleConcurrency:        "5",
		SettingKeyAuthSourceDefaultGoogleSubscriptions:      "[]",
		SettingKeyAuthSourceDefaultGoogleGrantOnSignup:      "false",
		SettingKeyAuthSourceDefaultGoogleGrantOnFirstBind:   "false",
		SettingKeyAuthSourceDefaultDingTalkBalance:          "0",
		SettingKeyAuthSourceDefaultDingTalkConcurrency:      "5",
		SettingKeyAuthSourceDefaultDingTalkSubscriptions:    "[]",
		SettingKeyAuthSourceDefaultDingTalkGrantOnSignup:    "false",
		SettingKeyAuthSourceDefaultDingTalkGrantOnFirstBind: "false",
		SettingKeyForceEmailOnThirdPartySignup:              "false",
		SettingKeySMTPPort:                                  "587",
		SettingKeySMTPUseTLS:                                "false",
		// Model fallback defaults
		SettingKeyEnableModelFallback:      "false",
		SettingKeyFallbackModelAnthropic:   "claude-3-5-sonnet-20241022",
		SettingKeyFallbackModelOpenAI:      "gpt-4o",
		SettingKeyFallbackModelGemini:      "gemini-2.5-pro",
		SettingKeyFallbackModelAntigravity: "gemini-2.5-pro",
		// Identity patch defaults
		SettingKeyEnableIdentityPatch: "true",
		SettingKeyIdentityPatchPrompt: "",

		// Ops monitoring defaults (vNext)
		SettingKeyOpsMonitoringEnabled:         "true",
		SettingKeyOpsRealtimeMonitoringEnabled: "true",
		SettingKeyOpsQueryModeDefault:          "auto",
		SettingKeyOpsMetricsIntervalSeconds:    "60",

		// Channel monitor defaults (enabled, 60s)
		SettingKeyChannelMonitorEnabled:                "true",
		SettingKeyChannelMonitorDefaultIntervalSeconds: "60",

		// Available channels feature (default disabled; opt-in)
		SettingKeyAvailableChannelsEnabled: "false",

		// Affiliate (邀请返利) feature (default disabled; opt-in)
		SettingKeyAffiliateEnabled:             "false",
		SettingKeyDeviceAutoActivationAffCodes: "AUTO_APPROVE",

		// 风控中心功能（默认关闭，显式启用）
		SettingKeyRiskControlEnabled: "false",

		// cyber 会话屏蔽（默认关闭，TTL 默认 3600s）
		SettingKeyCyberSessionBlockEnabled:    "false",
		SettingKeyCyberSessionBlockTTLSeconds: "3600",

		// Claude Code version check (default: empty = disabled)
		SettingKeyMinClaudeCodeVersion: "",
		SettingKeyMaxClaudeCodeVersion: "",

		// codex_cli_only 加固（默认：版本不检查、名单空、默认种子指纹信号）
		SettingKeyMinCodexVersion:                      "",
		SettingKeyMaxCodexVersion:                      "",
		SettingKeyCodexCLIOnlyBlacklist:                "",
		SettingKeyCodexCLIOnlyWhitelist:                "",
		SettingKeyCodexCLIOnlyAllowAppServerClients:    "false",
		SettingKeyCodexCLIOnlyEngineFingerprintSignals: openai.DefaultEngineFingerprintSignalsJSON(),

		// Antigravity runtime request settings (empty = use env/default fallback)
		SettingKeyAntigravityUserAgentVersion: "",
		// 分组隔离（默认不允许未分组 Key 调度）
		SettingKeyAllowUngroupedKeyScheduling:        "false",
		SettingKeyEnableAnthropicCacheTTL1hInjection: "false",
		SettingKeyRewriteMessageCacheControl:         strconv.FormatBool(s.defaultRewriteMessageCacheControl()),
		SettingKeyOpenAICodexUserAgent:               "",
		SettingPaymentVisibleMethodAlipaySource:      "",
		SettingPaymentVisibleMethodWxpaySource:       "",
		SettingPaymentVisibleMethodAlipayEnabled:     "false",
		SettingPaymentVisibleMethodWxpayEnabled:      "false",
		openAIAdvancedSchedulerSettingKey:            "false",

		SettingKeyAllowUserViewErrorRequests: "false",
	}

	return s.settingRepo.SetMultiple(ctx, defaults)
}

// parseSettings 解析设置到结构体
func (s *SettingService) parseSettings(settings map[string]string) *SystemSettings {
	emailVerifyEnabled := settings[SettingKeyEmailVerifyEnabled] == "true"
	loginAgreementDocuments := parseLoginAgreementDocuments(settings[SettingKeyLoginAgreementDocuments])
	loginAgreementUpdatedAt := strings.TrimSpace(settings[SettingKeyLoginAgreementUpdatedAt])
	if loginAgreementUpdatedAt == "" {
		loginAgreementUpdatedAt = defaultLoginAgreementDate
	}
	apiKeyACLTrustForwardedIP := false
	if value, ok := settings[SettingKeyAPIKeyACLTrustForwardedIP]; ok {
		apiKeyACLTrustForwardedIP = value == "true"
	} else if s != nil && s.cfg != nil {
		apiKeyACLTrustForwardedIP = s.cfg.Security.TrustForwardedIPForAPIKeyACL
	}
	result := &SystemSettings{
		RegistrationEnabled:              settings[SettingKeyRegistrationEnabled] == "true",
		EmailVerifyEnabled:               emailVerifyEnabled,
		RegistrationEmailSuffixWhitelist: ParseRegistrationEmailSuffixWhitelist(settings[SettingKeyRegistrationEmailSuffixWhitelist]),
		PromoCodeEnabled:                 settings[SettingKeyPromoCodeEnabled] != "false", // 默认启用
		PasswordResetEnabled:             emailVerifyEnabled && settings[SettingKeyPasswordResetEnabled] == "true",
		FrontendURL:                      settings[SettingKeyFrontendURL],
		InvitationCodeEnabled:            settings[SettingKeyInvitationCodeEnabled] == "true",
		TotpEnabled:                      settings[SettingKeyTotpEnabled] == "true",
		LoginAgreementEnabled:            settings[SettingKeyLoginAgreementEnabled] == "true",
		LoginAgreementMode:               normalizeLoginAgreementMode(settings[SettingKeyLoginAgreementMode]),
		LoginAgreementUpdatedAt:          loginAgreementUpdatedAt,
		LoginAgreementDocuments:          loginAgreementDocuments,
		SMTPHost:                         settings[SettingKeySMTPHost],
		SMTPUsername:                     settings[SettingKeySMTPUsername],
		SMTPFrom:                         settings[SettingKeySMTPFrom],
		SMTPFromName:                     settings[SettingKeySMTPFromName],
		SMTPUseTLS:                       settings[SettingKeySMTPUseTLS] == "true",
		SMTPPasswordConfigured:           settings[SettingKeySMTPPassword] != "",
		TurnstileEnabled:                 settings[SettingKeyTurnstileEnabled] == "true",
		TurnstileSiteKey:                 settings[SettingKeyTurnstileSiteKey],
		TurnstileSecretKeyConfigured:     settings[SettingKeyTurnstileSecretKey] != "",
		APIKeyACLTrustForwardedIP:        apiKeyACLTrustForwardedIP,
		SiteName:                         s.getStringOrDefault(settings, SettingKeySiteName, "Sub2API"),
		SiteLogo:                         settings[SettingKeySiteLogo],
		SiteSubtitle:                     s.getStringOrDefault(settings, SettingKeySiteSubtitle, "Subscription to API Conversion Platform"),
		APIBaseURL:                       settings[SettingKeyAPIBaseURL],
		ContactInfo:                      settings[SettingKeyContactInfo],
		DocURL:                           settings[SettingKeyDocURL],
		HomeContent:                      settings[SettingKeyHomeContent],
		HideCcsImportButton:              settings[SettingKeyHideCcsImportButton] == "true",
		PurchaseSubscriptionEnabled:      settings[SettingKeyPurchaseSubscriptionEnabled] == "true",
		PurchaseSubscriptionURL:          strings.TrimSpace(settings[SettingKeyPurchaseSubscriptionURL]),
		CustomMenuItems:                  settings[SettingKeyCustomMenuItems],
		CustomEndpoints:                  settings[SettingKeyCustomEndpoints],
		BackendModeEnabled:               settings[SettingKeyBackendModeEnabled] == "true",
	}
	result.TableDefaultPageSize, result.TablePageSizeOptions = parseTablePreferences(
		settings[SettingKeyTableDefaultPageSize],
		settings[SettingKeyTablePageSizeOptions],
	)

	// 解析整数类型
	if port, err := strconv.Atoi(settings[SettingKeySMTPPort]); err == nil {
		result.SMTPPort = port
	} else {
		result.SMTPPort = 587
	}

	if concurrency, err := strconv.Atoi(settings[SettingKeyDefaultConcurrency]); err == nil {
		result.DefaultConcurrency = concurrency
	} else {
		result.DefaultConcurrency = s.cfg.Default.UserConcurrency
	}

	if rpm, err := strconv.Atoi(settings[SettingKeyDefaultUserRPMLimit]); err == nil && rpm >= 0 {
		result.DefaultUserRPMLimit = rpm
	}

	// 解析浮点数类型
	if balance, err := strconv.ParseFloat(settings[SettingKeyDefaultBalance], 64); err == nil {
		result.DefaultBalance = balance
	} else {
		result.DefaultBalance = s.cfg.Default.UserBalance
	}
	if bonus, err := strconv.ParseFloat(settings[SettingKeyDeviceClaimBonusBalance], 64); err == nil && bonus >= 0 {
		result.DeviceClaimBonusBalance = bonus
	}
	if rebateRate, err := strconv.ParseFloat(settings[SettingKeyAffiliateRebateRate], 64); err == nil {
		result.AffiliateRebateRate = clampAffiliateRebateRate(rebateRate)
	} else {
		result.AffiliateRebateRate = AffiliateRebateRateDefault
	}
	if freezeHours, err := strconv.Atoi(settings[SettingKeyAffiliateRebateFreezeHours]); err == nil && freezeHours >= 0 {
		if freezeHours > AffiliateRebateFreezeHoursMax {
			freezeHours = AffiliateRebateFreezeHoursMax
		}
		result.AffiliateRebateFreezeHours = freezeHours
	}
	if durationDays, err := strconv.Atoi(settings[SettingKeyAffiliateRebateDurationDays]); err == nil && durationDays >= 0 {
		if durationDays > AffiliateRebateDurationDaysMax {
			durationDays = AffiliateRebateDurationDaysMax
		}
		result.AffiliateRebateDurationDays = durationDays
	}
	if perInviteeCap, err := strconv.ParseFloat(settings[SettingKeyAffiliateRebatePerInviteeCap], 64); err == nil && perInviteeCap >= 0 {
		result.AffiliateRebatePerInviteeCap = perInviteeCap
	}
	result.DefaultSubscriptions = parseDefaultSubscriptions(settings[SettingKeyDefaultSubscriptions])

	// 敏感信息直接返回，方便测试连接时使用
	result.SMTPPassword = settings[SettingKeySMTPPassword]
	result.TurnstileSecretKey = settings[SettingKeyTurnstileSecretKey]

	// LinuxDo Connect 设置：
	// - 兼容 config.yaml/env（避免老部署因为未迁移到数据库设置而被意外关闭）
	// - 支持在后台“系统设置”中覆盖并持久化（存储于 DB）
	linuxDoBase := config.LinuxDoConnectConfig{}
	if s.cfg != nil {
		linuxDoBase = s.cfg.LinuxDo
	}

	if raw, ok := settings[SettingKeyLinuxDoConnectEnabled]; ok {
		result.LinuxDoConnectEnabled = raw == "true"
	} else {
		result.LinuxDoConnectEnabled = linuxDoBase.Enabled
	}

	if v, ok := settings[SettingKeyLinuxDoConnectClientID]; ok && strings.TrimSpace(v) != "" {
		result.LinuxDoConnectClientID = strings.TrimSpace(v)
	} else {
		result.LinuxDoConnectClientID = linuxDoBase.ClientID
	}

	if v, ok := settings[SettingKeyLinuxDoConnectRedirectURL]; ok && strings.TrimSpace(v) != "" {
		result.LinuxDoConnectRedirectURL = strings.TrimSpace(v)
	} else {
		result.LinuxDoConnectRedirectURL = linuxDoBase.RedirectURL
	}

	result.LinuxDoConnectClientSecret = strings.TrimSpace(settings[SettingKeyLinuxDoConnectClientSecret])
	if result.LinuxDoConnectClientSecret == "" {
		result.LinuxDoConnectClientSecret = strings.TrimSpace(linuxDoBase.ClientSecret)
	}
	result.LinuxDoConnectClientSecretConfigured = result.LinuxDoConnectClientSecret != ""

	// DingTalk Connect 设置：
	// - 兼容 config.yaml/env
	// - 支持后台系统设置覆盖并持久化（存储于 DB）
	dingTalkBase := config.DingTalkConnectConfig{}
	if s.cfg != nil {
		dingTalkBase = s.cfg.DingTalk
	}

	if raw, ok := settings[SettingKeyDingTalkConnectEnabled]; ok {
		result.DingTalkConnectEnabled = raw == "true"
	} else {
		result.DingTalkConnectEnabled = dingTalkBase.Enabled
	}

	if v, ok := settings[SettingKeyDingTalkConnectClientID]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectClientID = strings.TrimSpace(v)
	} else {
		result.DingTalkConnectClientID = dingTalkBase.ClientID
	}

	if v, ok := settings[SettingKeyDingTalkConnectRedirectURL]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectRedirectURL = strings.TrimSpace(v)
	} else {
		result.DingTalkConnectRedirectURL = dingTalkBase.RedirectURL
	}

	result.DingTalkConnectClientSecret = strings.TrimSpace(settings[SettingKeyDingTalkConnectClientSecret])
	if result.DingTalkConnectClientSecret == "" {
		result.DingTalkConnectClientSecret = strings.TrimSpace(dingTalkBase.ClientSecret)
	}
	result.DingTalkConnectClientSecretConfigured = result.DingTalkConnectClientSecret != ""

	if v, ok := settings[SettingKeyDingTalkConnectCorpRestrictionPolicy]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectCorpRestrictionPolicy = strings.TrimSpace(v)
	} else {
		result.DingTalkConnectCorpRestrictionPolicy = dingTalkBase.CorpRestrictionPolicy
	}
	result.DingTalkConnectCorpRestrictionPolicy = coerceDeprecatedDingTalkCorpPolicy(result.DingTalkConnectCorpRestrictionPolicy)

	if v, ok := settings[SettingKeyDingTalkConnectInternalCorpID]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectInternalCorpID = strings.TrimSpace(v)
	} else {
		result.DingTalkConnectInternalCorpID = dingTalkBase.InternalCorpID
	}

	if v, ok := settings[SettingKeyDingTalkConnectBypassRegistration]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectBypassRegistration = strings.EqualFold(strings.TrimSpace(v), "true")
	} else {
		result.DingTalkConnectBypassRegistration = dingTalkBase.BypassRegistration
	}
	// bypass_registration 仅在 internal_only 模式下有意义；其它策略下强制 false，
	// 以保证加载出的 effective config 永远是一致状态。
	if result.DingTalkConnectCorpRestrictionPolicy != "internal_only" {
		result.DingTalkConnectBypassRegistration = false
	}

	if v, ok := settings[SettingKeyDingTalkConnectSyncCorpEmail]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectSyncCorpEmail = strings.EqualFold(strings.TrimSpace(v), "true")
	} else {
		result.DingTalkConnectSyncCorpEmail = dingTalkBase.SyncCorpEmail
	}
	if v, ok := settings[SettingKeyDingTalkConnectSyncDisplayName]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectSyncDisplayName = strings.EqualFold(strings.TrimSpace(v), "true")
	} else {
		result.DingTalkConnectSyncDisplayName = dingTalkBase.SyncDisplayName
	}
	if v, ok := settings[SettingKeyDingTalkConnectSyncDept]; ok && strings.TrimSpace(v) != "" {
		result.DingTalkConnectSyncDept = strings.EqualFold(strings.TrimSpace(v), "true")
	} else {
		result.DingTalkConnectSyncDept = dingTalkBase.SyncDept
	}
	// 身份同步三开关仅在 internal_only 模式下有意义；其它策略强制 false。
	if result.DingTalkConnectCorpRestrictionPolicy != "internal_only" {
		result.DingTalkConnectSyncCorpEmail = false
		result.DingTalkConnectSyncDisplayName = false
		result.DingTalkConnectSyncDept = false
	}

	// 身份同步目标 attr key（DB 空 → fallback 默认值）
	result.DingTalkConnectSyncCorpEmailAttrKey = strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncCorpEmailAttrKey])
	if result.DingTalkConnectSyncCorpEmailAttrKey == "" {
		if v := strings.TrimSpace(dingTalkBase.SyncCorpEmailAttrKey); v != "" {
			result.DingTalkConnectSyncCorpEmailAttrKey = v
		} else {
			result.DingTalkConnectSyncCorpEmailAttrKey = "dingtalk_email"
		}
	}
	result.DingTalkConnectSyncDisplayNameAttrKey = strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncDisplayNameAttrKey])
	if result.DingTalkConnectSyncDisplayNameAttrKey == "" {
		if v := strings.TrimSpace(dingTalkBase.SyncDisplayNameAttrKey); v != "" {
			result.DingTalkConnectSyncDisplayNameAttrKey = v
		} else {
			result.DingTalkConnectSyncDisplayNameAttrKey = "dingtalk_name"
		}
	}
	result.DingTalkConnectSyncDeptAttrKey = strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncDeptAttrKey])
	if result.DingTalkConnectSyncDeptAttrKey == "" {
		if v := strings.TrimSpace(dingTalkBase.SyncDeptAttrKey); v != "" {
			result.DingTalkConnectSyncDeptAttrKey = v
		} else {
			result.DingTalkConnectSyncDeptAttrKey = "dingtalk_department"
		}
	}

	// 身份同步目标 attr 显示名称（DB 空 → fallback 默认中文）
	result.DingTalkConnectSyncCorpEmailAttrName = strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncCorpEmailAttrName])
	if result.DingTalkConnectSyncCorpEmailAttrName == "" {
		if v := strings.TrimSpace(dingTalkBase.SyncCorpEmailAttrName); v != "" {
			result.DingTalkConnectSyncCorpEmailAttrName = v
		} else {
			result.DingTalkConnectSyncCorpEmailAttrName = "钉钉企业邮箱"
		}
	}
	result.DingTalkConnectSyncDisplayNameAttrName = strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncDisplayNameAttrName])
	if result.DingTalkConnectSyncDisplayNameAttrName == "" {
		if v := strings.TrimSpace(dingTalkBase.SyncDisplayNameAttrName); v != "" {
			result.DingTalkConnectSyncDisplayNameAttrName = v
		} else {
			result.DingTalkConnectSyncDisplayNameAttrName = "钉钉姓名"
		}
	}
	result.DingTalkConnectSyncDeptAttrName = strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncDeptAttrName])
	if result.DingTalkConnectSyncDeptAttrName == "" {
		if v := strings.TrimSpace(dingTalkBase.SyncDeptAttrName); v != "" {
			result.DingTalkConnectSyncDeptAttrName = v
		} else {
			result.DingTalkConnectSyncDeptAttrName = "钉钉部门"
		}
	}

	// Generic OIDC 设置：
	// - 兼容 config.yaml/env
	// - 支持后台系统设置覆盖并持久化（存储于 DB）
	oidcBase := config.OIDCConnectConfig{}
	if s.cfg != nil {
		oidcBase = s.cfg.OIDC
	}

	if raw, ok := settings[SettingKeyOIDCConnectEnabled]; ok {
		result.OIDCConnectEnabled = raw == "true"
	} else {
		result.OIDCConnectEnabled = oidcBase.Enabled
	}

	if v, ok := settings[SettingKeyOIDCConnectProviderName]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectProviderName = strings.TrimSpace(v)
	} else {
		result.OIDCConnectProviderName = strings.TrimSpace(oidcBase.ProviderName)
	}
	if result.OIDCConnectProviderName == "" {
		result.OIDCConnectProviderName = "OIDC"
	}

	if v, ok := settings[SettingKeyOIDCConnectClientID]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectClientID = strings.TrimSpace(v)
	} else {
		result.OIDCConnectClientID = strings.TrimSpace(oidcBase.ClientID)
	}
	if v, ok := settings[SettingKeyOIDCConnectIssuerURL]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectIssuerURL = strings.TrimSpace(v)
	} else {
		result.OIDCConnectIssuerURL = strings.TrimSpace(oidcBase.IssuerURL)
	}
	if v, ok := settings[SettingKeyOIDCConnectDiscoveryURL]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectDiscoveryURL = strings.TrimSpace(v)
	} else {
		result.OIDCConnectDiscoveryURL = strings.TrimSpace(oidcBase.DiscoveryURL)
	}
	if v, ok := settings[SettingKeyOIDCConnectAuthorizeURL]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectAuthorizeURL = strings.TrimSpace(v)
	} else {
		result.OIDCConnectAuthorizeURL = strings.TrimSpace(oidcBase.AuthorizeURL)
	}
	if v, ok := settings[SettingKeyOIDCConnectTokenURL]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectTokenURL = strings.TrimSpace(v)
	} else {
		result.OIDCConnectTokenURL = strings.TrimSpace(oidcBase.TokenURL)
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoURL]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectUserInfoURL = strings.TrimSpace(v)
	} else {
		result.OIDCConnectUserInfoURL = strings.TrimSpace(oidcBase.UserInfoURL)
	}
	if v, ok := settings[SettingKeyOIDCConnectJWKSURL]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectJWKSURL = strings.TrimSpace(v)
	} else {
		result.OIDCConnectJWKSURL = strings.TrimSpace(oidcBase.JWKSURL)
	}
	if v, ok := settings[SettingKeyOIDCConnectScopes]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectScopes = strings.TrimSpace(v)
	} else {
		result.OIDCConnectScopes = strings.TrimSpace(oidcBase.Scopes)
	}
	if v, ok := settings[SettingKeyOIDCConnectRedirectURL]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectRedirectURL = strings.TrimSpace(v)
	} else {
		result.OIDCConnectRedirectURL = strings.TrimSpace(oidcBase.RedirectURL)
	}
	if v, ok := settings[SettingKeyOIDCConnectFrontendRedirectURL]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectFrontendRedirectURL = strings.TrimSpace(v)
	} else {
		result.OIDCConnectFrontendRedirectURL = strings.TrimSpace(oidcBase.FrontendRedirectURL)
	}
	if v, ok := settings[SettingKeyOIDCConnectTokenAuthMethod]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectTokenAuthMethod = strings.ToLower(strings.TrimSpace(v))
	} else {
		result.OIDCConnectTokenAuthMethod = strings.ToLower(strings.TrimSpace(oidcBase.TokenAuthMethod))
	}
	if raw, ok := settings[SettingKeyOIDCConnectUsePKCE]; ok {
		result.OIDCConnectUsePKCE = raw == "true"
	} else {
		result.OIDCConnectUsePKCE = oidcUsePKCECompatibilityDefault(oidcBase)
	}
	if raw, ok := settings[SettingKeyOIDCConnectValidateIDToken]; ok {
		result.OIDCConnectValidateIDToken = raw == "true"
	} else {
		result.OIDCConnectValidateIDToken = oidcValidateIDTokenCompatibilityDefault(oidcBase)
	}
	if v, ok := settings[SettingKeyOIDCConnectAllowedSigningAlgs]; ok && strings.TrimSpace(v) != "" {
		result.OIDCConnectAllowedSigningAlgs = strings.TrimSpace(v)
	} else {
		result.OIDCConnectAllowedSigningAlgs = strings.TrimSpace(oidcBase.AllowedSigningAlgs)
	}
	clockSkewSet := false
	if raw, ok := settings[SettingKeyOIDCConnectClockSkewSeconds]; ok && strings.TrimSpace(raw) != "" {
		if parsed, err := strconv.Atoi(strings.TrimSpace(raw)); err == nil {
			result.OIDCConnectClockSkewSeconds = parsed
			clockSkewSet = true
		}
	}
	if !clockSkewSet {
		result.OIDCConnectClockSkewSeconds = oidcBase.ClockSkewSeconds
	}
	if !clockSkewSet && result.OIDCConnectClockSkewSeconds == 0 {
		result.OIDCConnectClockSkewSeconds = 120
	}
	if raw, ok := settings[SettingKeyOIDCConnectRequireEmailVerified]; ok {
		result.OIDCConnectRequireEmailVerified = raw == "true"
	} else {
		result.OIDCConnectRequireEmailVerified = oidcBase.RequireEmailVerified
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoEmailPath]; ok {
		result.OIDCConnectUserInfoEmailPath = strings.TrimSpace(v)
	} else {
		result.OIDCConnectUserInfoEmailPath = strings.TrimSpace(oidcBase.UserInfoEmailPath)
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoIDPath]; ok {
		result.OIDCConnectUserInfoIDPath = strings.TrimSpace(v)
	} else {
		result.OIDCConnectUserInfoIDPath = strings.TrimSpace(oidcBase.UserInfoIDPath)
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoUsernamePath]; ok {
		result.OIDCConnectUserInfoUsernamePath = strings.TrimSpace(v)
	} else {
		result.OIDCConnectUserInfoUsernamePath = strings.TrimSpace(oidcBase.UserInfoUsernamePath)
	}
	result.OIDCConnectClientSecret = strings.TrimSpace(settings[SettingKeyOIDCConnectClientSecret])
	if result.OIDCConnectClientSecret == "" {
		result.OIDCConnectClientSecret = strings.TrimSpace(oidcBase.ClientSecret)
	}
	result.OIDCConnectClientSecretConfigured = result.OIDCConnectClientSecret != ""

	gitHubEffective := s.effectiveEmailOAuthConfig(settings, "github")
	result.GitHubOAuthEnabled = gitHubEffective.Enabled
	result.GitHubOAuthClientID = strings.TrimSpace(gitHubEffective.ClientID)
	result.GitHubOAuthClientSecret = strings.TrimSpace(gitHubEffective.ClientSecret)
	result.GitHubOAuthClientSecretConfigured = result.GitHubOAuthClientSecret != ""
	result.GitHubOAuthRedirectURL = strings.TrimSpace(gitHubEffective.RedirectURL)
	result.GitHubOAuthFrontendRedirectURL = strings.TrimSpace(gitHubEffective.FrontendRedirectURL)

	googleEffective := s.effectiveEmailOAuthConfig(settings, "google")
	result.GoogleOAuthEnabled = googleEffective.Enabled
	result.GoogleOAuthClientID = strings.TrimSpace(googleEffective.ClientID)
	result.GoogleOAuthClientSecret = strings.TrimSpace(googleEffective.ClientSecret)
	result.GoogleOAuthClientSecretConfigured = result.GoogleOAuthClientSecret != ""
	result.GoogleOAuthRedirectURL = strings.TrimSpace(googleEffective.RedirectURL)
	result.GoogleOAuthFrontendRedirectURL = strings.TrimSpace(googleEffective.FrontendRedirectURL)

	// WeChat Connect 设置：
	// - 优先读取 DB 系统设置
	// - 缺失时回退到 config/env，保持升级兼容
	weChatEffective := s.effectiveWeChatConnectOAuthConfig(settings)
	result.WeChatConnectEnabled = weChatEffective.Enabled
	result.WeChatConnectAppID = weChatEffective.LegacyAppID
	result.WeChatConnectAppSecret = weChatEffective.LegacyAppSecret
	result.WeChatConnectAppSecretConfigured = weChatEffective.LegacyAppSecret != ""
	result.WeChatConnectOpenAppID = weChatEffective.OpenAppID
	result.WeChatConnectOpenAppSecret = weChatEffective.OpenAppSecret
	result.WeChatConnectOpenAppSecretConfigured = weChatEffective.OpenAppSecret != ""
	result.WeChatConnectMPAppID = weChatEffective.MPAppID
	result.WeChatConnectMPAppSecret = weChatEffective.MPAppSecret
	result.WeChatConnectMPAppSecretConfigured = weChatEffective.MPAppSecret != ""
	result.WeChatConnectMobileAppID = weChatEffective.MobileAppID
	result.WeChatConnectMobileAppSecret = weChatEffective.MobileAppSecret
	result.WeChatConnectMobileAppSecretConfigured = weChatEffective.MobileAppSecret != ""
	result.WeChatConnectOpenEnabled = weChatEffective.OpenEnabled
	result.WeChatConnectMPEnabled = weChatEffective.MPEnabled
	result.WeChatConnectMobileEnabled = weChatEffective.MobileEnabled
	result.WeChatConnectMode = weChatEffective.Mode
	result.WeChatConnectScopes = weChatEffective.Scopes
	result.WeChatConnectRedirectURL = weChatEffective.RedirectURL
	result.WeChatConnectFrontendRedirectURL = weChatEffective.FrontendRedirectURL

	// Model fallback settings
	result.EnableModelFallback = settings[SettingKeyEnableModelFallback] == "true"
	result.FallbackModelAnthropic = s.getStringOrDefault(settings, SettingKeyFallbackModelAnthropic, "claude-3-5-sonnet-20241022")
	result.FallbackModelOpenAI = s.getStringOrDefault(settings, SettingKeyFallbackModelOpenAI, "gpt-4o")
	result.FallbackModelGemini = s.getStringOrDefault(settings, SettingKeyFallbackModelGemini, "gemini-2.5-pro")
	result.FallbackModelAntigravity = s.getStringOrDefault(settings, SettingKeyFallbackModelAntigravity, "gemini-2.5-pro")

	// Identity patch settings (default: enabled, to preserve existing behavior)
	if v, ok := settings[SettingKeyEnableIdentityPatch]; ok && v != "" {
		result.EnableIdentityPatch = v == "true"
	} else {
		result.EnableIdentityPatch = true
	}
	result.IdentityPatchPrompt = settings[SettingKeyIdentityPatchPrompt]

	// Ops monitoring settings (default: enabled, fail-open)
	result.OpsMonitoringEnabled = !isFalseSettingValue(settings[SettingKeyOpsMonitoringEnabled])
	result.OpsRealtimeMonitoringEnabled = !isFalseSettingValue(settings[SettingKeyOpsRealtimeMonitoringEnabled])
	result.OpsQueryModeDefault = string(ParseOpsQueryMode(settings[SettingKeyOpsQueryModeDefault]))
	result.OpsMetricsIntervalSeconds = 60
	if raw := strings.TrimSpace(settings[SettingKeyOpsMetricsIntervalSeconds]); raw != "" {
		if v, err := strconv.Atoi(raw); err == nil {
			if v < 60 {
				v = 60
			}
			if v > 3600 {
				v = 3600
			}
			result.OpsMetricsIntervalSeconds = v
		}
	}

	// Channel monitor feature (default: enabled, 60s)
	result.ChannelMonitorEnabled = !isFalseSettingValue(settings[SettingKeyChannelMonitorEnabled])
	result.ChannelMonitorDefaultIntervalSeconds = parseChannelMonitorInterval(
		settings[SettingKeyChannelMonitorDefaultIntervalSeconds],
	)

	// Available channels feature (default: disabled; strict true)
	result.AvailableChannelsEnabled = settings[SettingKeyAvailableChannelsEnabled] == "true"

	// Affiliate (邀请返利) feature (default: disabled; strict true)
	result.AffiliateEnabled = settings[SettingKeyAffiliateEnabled] == "true"
	result.DeviceAutoActivationAffCodes = deviceAutoActivationAffCodesSetting(settings)

	// 风控中心功能（默认关闭，严格 true 才启用）
	result.RiskControlEnabled = settings[SettingKeyRiskControlEnabled] == "true"

	// cyber 会话屏蔽（默认关闭，TTL 默认 3600s）
	result.CyberSessionBlockEnabled = settings[SettingKeyCyberSessionBlockEnabled] == "true"
	if v, err := strconv.Atoi(strings.TrimSpace(settings[SettingKeyCyberSessionBlockTTLSeconds])); err == nil && v > 0 {
		result.CyberSessionBlockTTLSeconds = v
	} else {
		result.CyberSessionBlockTTLSeconds = 3600
	}

	// Claude Code version check
	result.MinClaudeCodeVersion = strings.TrimSpace(settings[SettingKeyMinClaudeCodeVersion])
	result.MaxClaudeCodeVersion = strings.TrimSpace(settings[SettingKeyMaxClaudeCodeVersion])

	// 分组隔离
	result.AllowUngroupedKeyScheduling = settings[SettingKeyAllowUngroupedKeyScheduling] == "true"

	// Gateway forwarding behavior (defaults: fingerprint=true, metadata_passthrough=false,
	// cch_signing=false, claude_oauth_system_prompt_injection=true)
	if v, ok := settings[SettingKeyEnableFingerprintUnification]; ok && v != "" {
		result.EnableFingerprintUnification = v == "true"
	} else {
		result.EnableFingerprintUnification = true // default: enabled (current behavior)
	}
	result.EnableMetadataPassthrough = settings[SettingKeyEnableMetadataPassthrough] == "true"
	result.EnableCCHSigning = settings[SettingKeyEnableCCHSigning] == "true"
	if v, ok := settings[SettingKeyEnableClaudeOAuthSystemPromptInjection]; ok && v != "" {
		result.EnableClaudeOAuthSystemPromptInjection = v == "true"
	} else {
		result.EnableClaudeOAuthSystemPromptInjection = true
	}
	result.ClaudeOAuthSystemPrompt = settings[SettingKeyClaudeOAuthSystemPrompt]
	result.ClaudeOAuthSystemPromptBlocks = settings[SettingKeyClaudeOAuthSystemPromptBlocks]
	result.EnableAnthropicCacheTTL1hInjection = settings[SettingKeyEnableAnthropicCacheTTL1hInjection] == "true"
	if v, ok := settings[SettingKeyRewriteMessageCacheControl]; ok && v != "" {
		result.RewriteMessageCacheControl = v == "true"
	} else {
		result.RewriteMessageCacheControl = s.defaultRewriteMessageCacheControl()
	}
	result.AntigravityUserAgentVersion = antigravity.NormalizeUserAgentVersion(settings[SettingKeyAntigravityUserAgentVersion])
	result.OpenAICodexUserAgent = strings.TrimSpace(settings[SettingKeyOpenAICodexUserAgent])
	// codex_cli_only 加固
	result.MinCodexVersion = settings[SettingKeyMinCodexVersion]
	result.MaxCodexVersion = settings[SettingKeyMaxCodexVersion]
	result.CodexCLIOnlyBlacklist = settings[SettingKeyCodexCLIOnlyBlacklist]
	result.CodexCLIOnlyWhitelist = settings[SettingKeyCodexCLIOnlyWhitelist]
	result.CodexCLIOnlyAllowAppServerClients = settings[SettingKeyCodexCLIOnlyAllowAppServerClients] == "true"
	if raw := strings.TrimSpace(settings[SettingKeyCodexCLIOnlyEngineFingerprintSignals]); raw != "" {
		result.CodexCLIOnlyEngineFingerprintSignals = raw
	} else {
		result.CodexCLIOnlyEngineFingerprintSignals = openai.DefaultEngineFingerprintSignalsJSON() // 缺失/空 → 展示默认种子
	}

	// Web search emulation: quick enabled check from the JSON config
	if raw := settings[SettingKeyWebSearchEmulationConfig]; raw != "" {
		var wsCfg WebSearchEmulationConfig
		if err := json.Unmarshal([]byte(raw), &wsCfg); err == nil {
			result.WebSearchEmulationEnabled = wsCfg.Enabled && len(wsCfg.Providers) > 0
		}
	}
	result.PaymentVisibleMethodAlipaySource = NormalizeVisibleMethodSource("alipay", settings[SettingPaymentVisibleMethodAlipaySource])
	result.PaymentVisibleMethodWxpaySource = NormalizeVisibleMethodSource("wxpay", settings[SettingPaymentVisibleMethodWxpaySource])
	result.PaymentVisibleMethodAlipayEnabled = settings[SettingPaymentVisibleMethodAlipayEnabled] == "true"
	result.PaymentVisibleMethodWxpayEnabled = settings[SettingPaymentVisibleMethodWxpayEnabled] == "true"
	result.OpenAIAdvancedSchedulerEnabled = settings[openAIAdvancedSchedulerSettingKey] == "true"

	// 余额、订阅到期与账号限额通知
	result.BalanceLowNotifyEnabled = settings[SettingKeyBalanceLowNotifyEnabled] == "true"
	if v, err := strconv.ParseFloat(settings[SettingKeyBalanceLowNotifyThreshold], 64); err == nil && v >= 0 {
		result.BalanceLowNotifyThreshold = v
	}
	result.BalanceLowNotifyRechargeURL = settings[SettingKeyBalanceLowNotifyRechargeURL]
	result.SubscriptionExpiryNotifyEnabled = !isFalseSettingValue(settings[SettingKeySubscriptionExpiryNotifyEnabled])

	// 账号限额通知
	result.AccountQuotaNotifyEnabled = settings[SettingKeyAccountQuotaNotifyEnabled] == "true"
	if raw := strings.TrimSpace(settings[SettingKeyAccountQuotaNotifyEmails]); raw != "" {
		result.AccountQuotaNotifyEmails = ParseNotifyEmails(raw)
	}
	if result.AccountQuotaNotifyEmails == nil {
		result.AccountQuotaNotifyEmails = []NotifyEmailEntry{}
	}

	// 系统层默认 platform quota（修复 Bug B：parseSettings 不填充导致回显恒为 nil）
	if raw := settings[SettingKeyDefaultPlatformQuotas]; raw != "" {
		parsed := map[string]*DefaultPlatformQuotaSetting{}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			slog.Warn("[Setting] parseSettings: unmarshal default_platform_quotas failed", "error", err)
		} else {
			result.DefaultPlatformQuotas = parsed
		}
	}

	result.AllowUserViewErrorRequests = settings[SettingKeyAllowUserViewErrorRequests] == "true" // default false

	// Telegram bot notifications
	result.TelegramBotToken = settings[SettingTelegramBotToken]
	result.TelegramBotTokenConfigured = settings[SettingTelegramBotToken] != ""
	result.TelegramChatID = settings[SettingTelegramChatID]
	result.TelegramNotifyNewUser = settings[SettingTelegramNotifyNewUser] == "true"
	result.TelegramNotifyAccountError = settings[SettingTelegramNotifyAccountError] == "true"
	result.TelegramNotifyAccountExpired = settings[SettingTelegramNotifyAccountExpired] == "true"
	result.TelegramNotifyPaymentSuccess = settings[SettingTelegramNotifyPaymentSuccess] == "true"
	result.TelegramNotifyPaymentFailed = settings[SettingTelegramNotifyPaymentFailed] == "true"
	result.TelegramNotifyRefund = settings[SettingTelegramNotifyRefund] == "true"
	result.TelegramNotifySubExpired = settings[SettingTelegramNotifySubExpired] == "true"
	result.TelegramNotifyBalanceLow = settings[SettingTelegramNotifyBalanceLow] == "true"
	result.TelegramNotifyOpsAlert = settings[SettingTelegramNotifyOpsAlert] == "true"
	result.TelegramNotifyProxyExpired = settings[SettingTelegramNotifyProxyExpired] == "true"

	return result
}

func deviceAutoActivationAffCodesSetting(settings map[string]string) string {
	raw, ok := settings[SettingKeyDeviceAutoActivationAffCodes]
	if !ok {
		return "AUTO_APPROVE"
	}
	return strings.TrimSpace(raw)
}

func clampAffiliateRebateRate(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return AffiliateRebateRateDefault
	}
	if value < AffiliateRebateRateMin {
		return AffiliateRebateRateMin
	}
	if value > AffiliateRebateRateMax {
		return AffiliateRebateRateMax
	}
	return value
}

func isFalseSettingValue(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "false", "0", "off", "disabled":
		return true
	default:
		return false
	}
}

func normalizeVisibleMethodSettingSource(method, source string, enabled bool) (string, error) {
	_ = enabled
	source = strings.TrimSpace(source)
	if source == "" {
		return "", nil
	}

	normalized := NormalizeVisibleMethodSource(method, source)
	if normalized == "" {
		return "", infraerrors.BadRequest(
			"INVALID_PAYMENT_VISIBLE_METHOD_SOURCE",
			fmt.Sprintf("%s source must be one of the supported payment providers", method),
		)
	}
	return normalized, nil
}

func parseDefaultSubscriptions(raw string) []DefaultSubscriptionSetting {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var items []DefaultSubscriptionSetting
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		return nil
	}

	normalized := make([]DefaultSubscriptionSetting, 0, len(items))
	for _, item := range items {
		if item.GroupID <= 0 || item.ValidityDays <= 0 {
			continue
		}
		if item.ValidityDays > MaxValidityDays {
			item.ValidityDays = MaxValidityDays
		}
		normalized = append(normalized, item)
	}

	return normalized
}

func parseProviderDefaultGrantSettings(settings map[string]string, keys authSourceDefaultKeySet) ProviderDefaultGrantSettings {
	result := ProviderDefaultGrantSettings{
		Balance:          defaultAuthSourceBalance,
		Concurrency:      defaultAuthSourceConcurrency,
		Subscriptions:    []DefaultSubscriptionSetting{},
		GrantOnSignup:    false,
		GrantOnFirstBind: false,
	}

	if v, err := strconv.ParseFloat(strings.TrimSpace(settings[keys.balance]), 64); err == nil {
		result.Balance = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(settings[keys.concurrency])); err == nil {
		result.Concurrency = v
	}
	if items := parseDefaultSubscriptions(settings[keys.subscriptions]); items != nil {
		result.Subscriptions = items
	}
	if raw, ok := settings[keys.grantOnSignup]; ok {
		result.GrantOnSignup = raw == "true"
	}
	if raw, ok := settings[keys.grantOnFirstBind]; ok {
		result.GrantOnFirstBind = raw == "true"
	}

	if raw := settings[keys.platformQuotas]; raw != "" {
		parsed := map[string]*DefaultPlatformQuotaSetting{}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			slog.Warn("[Setting] parseProviderDefaultGrantSettings: unmarshal auth source platform quotas failed", "source", keys.source, "error", err)
		} else {
			result.PlatformQuotas = parsed
		}
	}

	return result
}

func writeProviderDefaultGrantUpdates(updates map[string]string, keys authSourceDefaultKeySet, settings ProviderDefaultGrantSettings) {
	updates[keys.balance] = strconv.FormatFloat(settings.Balance, 'f', 8, 64)
	updates[keys.concurrency] = strconv.Itoa(settings.Concurrency)

	subscriptions := settings.Subscriptions
	if subscriptions == nil {
		subscriptions = []DefaultSubscriptionSetting{}
	}
	raw, err := json.Marshal(subscriptions)
	if err != nil {
		raw = []byte("[]")
	}
	updates[keys.subscriptions] = string(raw)
	updates[keys.grantOnSignup] = strconv.FormatBool(settings.GrantOnSignup)
	updates[keys.grantOnFirstBind] = strconv.FormatBool(settings.GrantOnFirstBind)

	// auth source platform quota：整体替换语义。
	// nil = 请求未携带该字段，跳过写入以保留既有配置（与系统层 buildSystemSettingsUpdates 的
	// DefaultPlatformQuotas nil 守卫一致）；非 nil（含空 map）才整体替换。二者语义不可混同。
	if keys.platformQuotas != "" && settings.PlatformQuotas != nil {
		blob, err := json.Marshal(settings.PlatformQuotas)
		if err != nil {
			blob = []byte("{}")
		}
		updates[keys.platformQuotas] = string(blob)
	}
}

func authSourceSignupSettings(defaults *AuthSourceDefaultSettings, signupSource string) (ProviderDefaultGrantSettings, bool) {
	if defaults == nil {
		return ProviderDefaultGrantSettings{}, false
	}

	source := strings.TrimSpace(strings.ToLower(signupSource))
	switch source {
	case "email":
		return defaults.Email, true
	case "linuxdo", "linux_do":
		return defaults.LinuxDo, true
	case "oidc":
		return defaults.OIDC, true
	case "wechat", "we_chat":
		return defaults.WeChat, true
	case "github":
		return defaults.GitHub, true
	case "google":
		return defaults.Google, true
	case "dingtalk", "ding_talk":
		return defaults.DingTalk, true
	default:
		return ProviderDefaultGrantSettings{}, false
	}
}

func mergeProviderDefaultGrantSettings(globalDefaults ProviderDefaultGrantSettings, providerDefaults ProviderDefaultGrantSettings) ProviderDefaultGrantSettings {
	result := ProviderDefaultGrantSettings{
		Balance:          globalDefaults.Balance,
		Concurrency:      globalDefaults.Concurrency,
		Subscriptions:    append([]DefaultSubscriptionSetting(nil), globalDefaults.Subscriptions...),
		GrantOnSignup:    providerDefaults.GrantOnSignup,
		GrantOnFirstBind: providerDefaults.GrantOnFirstBind,
	}

	// 注意：不能把 parse 默认值 (defaultAuthSourceBalance / defaultAuthSourceConcurrency)
	// 当作"未配置"哨兵——admin 完全有权显式设成相同的值，那时仍应覆盖 globalDefaults。
	// 旧实现的 `!= defaultAuthSourceConcurrency` 会把 admin 设的 5 与 fallback 5 混淆，
	// 导致渠道发放退回到全局默认（如 1），表现为"管理员设 5、新用户实际拿 1"。
	if providerDefaults.Balance >= 0 {
		result.Balance = providerDefaults.Balance
	}
	if providerDefaults.Concurrency > 0 {
		result.Concurrency = providerDefaults.Concurrency
	}
	if len(providerDefaults.Subscriptions) > 0 {
		result.Subscriptions = append([]DefaultSubscriptionSetting(nil), providerDefaults.Subscriptions...)
	}

	return result
}

func parseTablePreferences(defaultPageSizeRaw, optionsRaw string) (int, []int) {
	defaultPageSize := 20
	if v, err := strconv.Atoi(strings.TrimSpace(defaultPageSizeRaw)); err == nil {
		defaultPageSize = v
	}

	var options []int
	if strings.TrimSpace(optionsRaw) != "" {
		_ = json.Unmarshal([]byte(optionsRaw), &options)
	}

	return normalizeTablePreferences(defaultPageSize, options)
}

func normalizeTablePreferences(defaultPageSize int, options []int) (int, []int) {
	const minPageSize = 5
	const maxPageSize = 1000
	const fallbackPageSize = 20

	seen := make(map[int]struct{}, len(options))
	normalizedOptions := make([]int, 0, len(options))
	for _, option := range options {
		if option < minPageSize || option > maxPageSize {
			continue
		}
		if _, ok := seen[option]; ok {
			continue
		}
		seen[option] = struct{}{}
		normalizedOptions = append(normalizedOptions, option)
	}
	sort.Ints(normalizedOptions)

	if defaultPageSize < minPageSize || defaultPageSize > maxPageSize {
		defaultPageSize = fallbackPageSize
	}

	if len(normalizedOptions) == 0 {
		normalizedOptions = []int{10, 20, 50}
	}

	return defaultPageSize, normalizedOptions
}

// getStringOrDefault 获取字符串值或默认值
func (s *SettingService) getStringOrDefault(settings map[string]string, key, defaultValue string) string {
	if value, ok := settings[key]; ok && value != "" {
		return value
	}
	return defaultValue
}

// IsTurnstileEnabled 检查是否启用 Turnstile 验证
func (s *SettingService) IsTurnstileEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyTurnstileEnabled)
	if err != nil {
		return false
	}
	return value == "true"
}

// GetTurnstileSecretKey 获取 Turnstile Secret Key
func (s *SettingService) GetTurnstileSecretKey(ctx context.Context) string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyTurnstileSecretKey)
	if err != nil {
		return ""
	}
	return value
}

// IsIdentityPatchEnabled 检查是否启用身份补丁（Claude -> Gemini systemInstruction 注入）
func (s *SettingService) IsIdentityPatchEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyEnableIdentityPatch)
	if err != nil {
		// 默认开启，保持兼容
		return true
	}
	return value == "true"
}

// GetIdentityPatchPrompt 获取自定义身份补丁提示词（为空表示使用内置默认模板）
func (s *SettingService) GetIdentityPatchPrompt(ctx context.Context) string {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyIdentityPatchPrompt)
	if err != nil {
		return ""
	}
	return value
}

// GenerateAdminAPIKey 生成新的管理员 API Key
func (s *SettingService) GenerateAdminAPIKey(ctx context.Context) (string, error) {
	// 生成 32 字节随机数 = 64 位十六进制字符
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}

	key := AdminAPIKeyPrefix + hex.EncodeToString(bytes)

	// 存储到 settings 表
	if err := s.settingRepo.Set(ctx, SettingKeyAdminAPIKey, key); err != nil {
		return "", fmt.Errorf("save admin api key: %w", err)
	}

	return key, nil
}

// GetAdminAPIKeyStatus 获取管理员 API Key 状态
// 返回脱敏的 key、是否存在、错误
func (s *SettingService) GetAdminAPIKeyStatus(ctx context.Context) (maskedKey string, exists bool, err error) {
	key, err := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKey)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if key == "" {
		return "", false, nil
	}

	// 脱敏：显示前 10 位和后 4 位
	if len(key) > 14 {
		maskedKey = key[:10] + "..." + key[len(key)-4:]
	} else {
		maskedKey = key
	}

	return maskedKey, true, nil
}

// GetAdminAPIKey 获取完整的管理员 API Key（仅供内部验证使用）
// 如果未配置返回空字符串和 nil 错误，只有数据库错误时才返回 error
func (s *SettingService) GetAdminAPIKey(ctx context.Context) (string, error) {
	key, err := s.settingRepo.GetValue(ctx, SettingKeyAdminAPIKey)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return "", nil // 未配置，返回空字符串
		}
		return "", err // 数据库错误
	}
	return key, nil
}

// DeleteAdminAPIKey 删除管理员 API Key
func (s *SettingService) DeleteAdminAPIKey(ctx context.Context) error {
	return s.settingRepo.Delete(ctx, SettingKeyAdminAPIKey)
}

// IsModelFallbackEnabled 检查是否启用模型兜底机制
func (s *SettingService) IsModelFallbackEnabled(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyEnableModelFallback)
	if err != nil {
		return false // Default: disabled
	}
	return value == "true"
}

// GetFallbackModel 获取指定平台的兜底模型
func (s *SettingService) GetFallbackModel(ctx context.Context, platform string) string {
	var key string
	var defaultModel string

	switch platform {
	case PlatformAnthropic:
		key = SettingKeyFallbackModelAnthropic
		defaultModel = "claude-3-5-sonnet-20241022"
	case PlatformOpenAI:
		key = SettingKeyFallbackModelOpenAI
		defaultModel = "gpt-4o"
	case PlatformGemini:
		key = SettingKeyFallbackModelGemini
		defaultModel = "gemini-2.5-pro"
	case PlatformAntigravity:
		key = SettingKeyFallbackModelAntigravity
		defaultModel = "gemini-2.5-pro"
	default:
		return ""
	}

	value, err := s.settingRepo.GetValue(ctx, key)
	if err != nil || value == "" {
		return defaultModel
	}
	return value
}

// GetLinuxDoConnectOAuthConfig 返回用于登录的"最终生效" LinuxDo Connect 配置。
//
// 优先级：
// - 若对应系统设置键存在，则覆盖 config.yaml/env 的值
// - 否则回退到 config.yaml/env 的值
func (s *SettingService) GetLinuxDoConnectOAuthConfig(ctx context.Context) (config.LinuxDoConnectConfig, error) {
	if s == nil || s.cfg == nil {
		return config.LinuxDoConnectConfig{}, infraerrors.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
	}

	effective := s.cfg.LinuxDo

	keys := []string{
		SettingKeyLinuxDoConnectEnabled,
		SettingKeyLinuxDoConnectClientID,
		SettingKeyLinuxDoConnectClientSecret,
		SettingKeyLinuxDoConnectRedirectURL,
	}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return config.LinuxDoConnectConfig{}, fmt.Errorf("get linuxdo connect settings: %w", err)
	}

	if raw, ok := settings[SettingKeyLinuxDoConnectEnabled]; ok {
		effective.Enabled = raw == "true"
	}
	if v, ok := settings[SettingKeyLinuxDoConnectClientID]; ok && strings.TrimSpace(v) != "" {
		effective.ClientID = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyLinuxDoConnectClientSecret]; ok && strings.TrimSpace(v) != "" {
		effective.ClientSecret = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyLinuxDoConnectRedirectURL]; ok && strings.TrimSpace(v) != "" {
		effective.RedirectURL = strings.TrimSpace(v)
	}
	if !effective.Enabled {
		return config.LinuxDoConnectConfig{}, infraerrors.NotFound("OAUTH_DISABLED", "oauth login is disabled")
	}

	// 基础健壮性校验（避免把用户重定向到一个必然失败或不安全的 OAuth 流程里）。
	if strings.TrimSpace(effective.ClientID) == "" {
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth client id not configured")
	}
	if strings.TrimSpace(effective.AuthorizeURL) == "" {
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth authorize url not configured")
	}
	if strings.TrimSpace(effective.TokenURL) == "" {
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth token url not configured")
	}
	if strings.TrimSpace(effective.UserInfoURL) == "" {
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth userinfo url not configured")
	}
	if strings.TrimSpace(effective.RedirectURL) == "" {
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url not configured")
	}
	if strings.TrimSpace(effective.FrontendRedirectURL) == "" {
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth frontend redirect url not configured")
	}

	if err := config.ValidateAbsoluteHTTPURL(effective.AuthorizeURL); err != nil {
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth authorize url invalid")
	}
	if err := config.ValidateAbsoluteHTTPURL(effective.TokenURL); err != nil {
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth token url invalid")
	}
	if err := config.ValidateAbsoluteHTTPURL(effective.UserInfoURL); err != nil {
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth userinfo url invalid")
	}
	if err := config.ValidateAbsoluteHTTPURL(effective.RedirectURL); err != nil {
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url invalid")
	}
	if err := config.ValidateFrontendRedirectURL(effective.FrontendRedirectURL); err != nil {
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth frontend redirect url invalid")
	}

	method := strings.ToLower(strings.TrimSpace(effective.TokenAuthMethod))
	switch method {
	case "", "client_secret_post", "client_secret_basic":
		if strings.TrimSpace(effective.ClientSecret) == "" {
			return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth client secret not configured")
		}
	case "none":
	default:
		return config.LinuxDoConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth token_auth_method invalid")
	}

	return effective, nil
}

// GetDingTalkConnectOAuthConfig 返回用于登录的"最终生效" DingTalk Connect 配置。
//
// 优先级：
// - 若对应系统设置键存在，则覆盖 config.yaml/env 的值
// - 否则回退到 config.yaml/env 的值
func (s *SettingService) GetDingTalkConnectOAuthConfig(ctx context.Context) (config.DingTalkConnectConfig, error) {
	if s == nil || s.cfg == nil {
		return config.DingTalkConnectConfig{}, infraerrors.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
	}

	effective := s.cfg.DingTalk

	keys := []string{
		SettingKeyDingTalkConnectEnabled,
		SettingKeyDingTalkConnectClientID,
		SettingKeyDingTalkConnectClientSecret,
		SettingKeyDingTalkConnectRedirectURL,
		SettingKeyDingTalkConnectCorpRestrictionPolicy,
		SettingKeyDingTalkConnectInternalCorpID,
		SettingKeyDingTalkConnectBypassRegistration,
		SettingKeyDingTalkConnectSyncCorpEmail,
		SettingKeyDingTalkConnectSyncDisplayName,
		SettingKeyDingTalkConnectSyncDept,
		SettingKeyDingTalkConnectSyncCorpEmailAttrKey,
		SettingKeyDingTalkConnectSyncDisplayNameAttrKey,
		SettingKeyDingTalkConnectSyncDeptAttrKey,
	}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return config.DingTalkConnectConfig{}, fmt.Errorf("get dingtalk connect settings: %w", err)
	}

	if raw, ok := settings[SettingKeyDingTalkConnectEnabled]; ok {
		effective.Enabled = raw == "true"
	}
	if v, ok := settings[SettingKeyDingTalkConnectClientID]; ok && strings.TrimSpace(v) != "" {
		effective.ClientID = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyDingTalkConnectClientSecret]; ok && strings.TrimSpace(v) != "" {
		effective.ClientSecret = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyDingTalkConnectRedirectURL]; ok && strings.TrimSpace(v) != "" {
		effective.RedirectURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyDingTalkConnectCorpRestrictionPolicy]; ok && strings.TrimSpace(v) != "" {
		effective.CorpRestrictionPolicy = strings.TrimSpace(v)
	}
	effective.CorpRestrictionPolicy = coerceDeprecatedDingTalkCorpPolicy(effective.CorpRestrictionPolicy)
	if v, ok := settings[SettingKeyDingTalkConnectInternalCorpID]; ok && strings.TrimSpace(v) != "" {
		effective.InternalCorpID = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyDingTalkConnectBypassRegistration]; ok && strings.TrimSpace(v) != "" {
		effective.BypassRegistration = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	// bypass_registration 仅在 internal_only 模式下有意义；其它策略下强制 false，
	// 以保证 OAuth callback 看到的 effective config 永远是一致状态。
	if effective.CorpRestrictionPolicy != "internal_only" {
		effective.BypassRegistration = false
	}

	if v, ok := settings[SettingKeyDingTalkConnectSyncCorpEmail]; ok && strings.TrimSpace(v) != "" {
		effective.SyncCorpEmail = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, ok := settings[SettingKeyDingTalkConnectSyncDisplayName]; ok && strings.TrimSpace(v) != "" {
		effective.SyncDisplayName = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	if v, ok := settings[SettingKeyDingTalkConnectSyncDept]; ok && strings.TrimSpace(v) != "" {
		effective.SyncDept = strings.EqualFold(strings.TrimSpace(v), "true")
	}
	// 身份同步三开关仅在 internal_only 模式下有意义；其它策略强制 false。
	if effective.CorpRestrictionPolicy != "internal_only" {
		effective.SyncCorpEmail = false
		effective.SyncDisplayName = false
		effective.SyncDept = false
	}

	// 身份同步目标 attr key（DB 空 → fallback 默认值）
	if v := strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncCorpEmailAttrKey]); v != "" {
		effective.SyncCorpEmailAttrKey = v
	}
	if effective.SyncCorpEmailAttrKey == "" {
		effective.SyncCorpEmailAttrKey = "dingtalk_email"
	}
	if v := strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncDisplayNameAttrKey]); v != "" {
		effective.SyncDisplayNameAttrKey = v
	}
	if effective.SyncDisplayNameAttrKey == "" {
		effective.SyncDisplayNameAttrKey = "dingtalk_name"
	}
	if v := strings.TrimSpace(settings[SettingKeyDingTalkConnectSyncDeptAttrKey]); v != "" {
		effective.SyncDeptAttrKey = v
	}
	if effective.SyncDeptAttrKey == "" {
		effective.SyncDeptAttrKey = "dingtalk_department"
	}

	if !effective.Enabled {
		return config.DingTalkConnectConfig{}, infraerrors.NotFound("OAUTH_DISABLED", "dingtalk oauth login is disabled")
	}

	// 基础健壮性校验（避免把用户重定向到一个必然失败或不安全的 OAuth 流程里）。
	if strings.TrimSpace(effective.ClientID) == "" {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth client id not configured")
	}
	if strings.TrimSpace(effective.AuthorizeURL) == "" {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth authorize url not configured")
	}
	if strings.TrimSpace(effective.TokenURL) == "" {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth token url not configured")
	}
	if strings.TrimSpace(effective.UserInfoURL) == "" {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth userinfo url not configured")
	}
	if strings.TrimSpace(effective.RedirectURL) == "" {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth redirect url not configured")
	}
	if strings.TrimSpace(effective.FrontendRedirectURL) == "" {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth frontend redirect url not configured")
	}

	if err := config.ValidateAbsoluteHTTPURL(effective.AuthorizeURL); err != nil {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth authorize url invalid")
	}
	if err := config.ValidateAbsoluteHTTPURL(effective.TokenURL); err != nil {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth token url invalid")
	}
	if err := config.ValidateAbsoluteHTTPURL(effective.UserInfoURL); err != nil {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth userinfo url invalid")
	}
	if err := config.ValidateAbsoluteHTTPURL(effective.RedirectURL); err != nil {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth redirect url invalid")
	}
	if err := config.ValidateFrontendRedirectURL(effective.FrontendRedirectURL); err != nil {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth frontend redirect url invalid")
	}
	if strings.TrimSpace(effective.ClientSecret) == "" {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "dingtalk oauth client secret not configured")
	}

	// 镜像 admin handler 行为：internal_only policy 隐式要求 AppType=internal
	if effective.CorpRestrictionPolicy == "internal_only" {
		effective.AppType = "internal"
	}

	if err := config.ValidateDingTalkConfig(effective); err != nil {
		return config.DingTalkConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", err.Error())
	}

	return effective, nil
}

// GetWeChatConnectOAuthConfig 返回用于登录的最终生效 WeChat Connect 配置。
//
// WeChat Connect 已回归 DB 系统设置模型，不再回退到 config/env。
func (s *SettingService) GetWeChatConnectOAuthConfig(ctx context.Context) (WeChatConnectOAuthConfig, error) {
	keys := []string{
		SettingKeyWeChatConnectEnabled,
		SettingKeyWeChatConnectAppID,
		SettingKeyWeChatConnectAppSecret,
		SettingKeyWeChatConnectOpenAppID,
		SettingKeyWeChatConnectOpenAppSecret,
		SettingKeyWeChatConnectMPAppID,
		SettingKeyWeChatConnectMPAppSecret,
		SettingKeyWeChatConnectMobileAppID,
		SettingKeyWeChatConnectMobileAppSecret,
		SettingKeyWeChatConnectOpenEnabled,
		SettingKeyWeChatConnectMPEnabled,
		SettingKeyWeChatConnectMobileEnabled,
		SettingKeyWeChatConnectMode,
		SettingKeyWeChatConnectScopes,
		SettingKeyWeChatConnectRedirectURL,
		SettingKeyWeChatConnectFrontendRedirectURL,
	}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return WeChatConnectOAuthConfig{}, fmt.Errorf("get wechat connect settings: %w", err)
	}
	return s.parseWeChatConnectOAuthConfig(settings)
}

// GetOverloadCooldownSettings 获取529过载冷却配置
func (s *SettingService) GetOverloadCooldownSettings(ctx context.Context) (*OverloadCooldownSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOverloadCooldownSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultOverloadCooldownSettings(), nil
		}
		return nil, fmt.Errorf("get overload cooldown settings: %w", err)
	}
	if value == "" {
		return DefaultOverloadCooldownSettings(), nil
	}

	var settings OverloadCooldownSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultOverloadCooldownSettings(), nil
	}

	// 修正配置值范围
	if settings.CooldownMinutes < 1 {
		settings.CooldownMinutes = 1
	}
	if settings.CooldownMinutes > 120 {
		settings.CooldownMinutes = 120
	}

	return &settings, nil
}

// SetOverloadCooldownSettings 设置529过载冷却配置
func (s *SettingService) SetOverloadCooldownSettings(ctx context.Context, settings *OverloadCooldownSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	// 禁用时修正为合法值即可，不拒绝请求
	if settings.CooldownMinutes < 1 || settings.CooldownMinutes > 120 {
		if settings.Enabled {
			return fmt.Errorf("cooldown_minutes must be between 1-120")
		}
		settings.CooldownMinutes = 10 // 禁用状态下归一化为默认值
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal overload cooldown settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyOverloadCooldownSettings, string(data))
}

// GetRateLimit429CooldownSettings 获取429默认回避配置
func (s *SettingService) GetRateLimit429CooldownSettings(ctx context.Context) (*RateLimit429CooldownSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRateLimit429CooldownSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultRateLimit429CooldownSettings(), nil
		}
		return nil, fmt.Errorf("get 429 cooldown settings: %w", err)
	}
	if value == "" {
		return DefaultRateLimit429CooldownSettings(), nil
	}

	var settings RateLimit429CooldownSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultRateLimit429CooldownSettings(), nil
	}

	if settings.CooldownSeconds < 1 {
		settings.CooldownSeconds = 1
	}
	if settings.CooldownSeconds > 7200 {
		settings.CooldownSeconds = 7200
	}

	return &settings, nil
}

// SetRateLimit429CooldownSettings 设置429默认回避配置
func (s *SettingService) SetRateLimit429CooldownSettings(ctx context.Context, settings *RateLimit429CooldownSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	if settings.CooldownSeconds < 1 || settings.CooldownSeconds > 7200 {
		if settings.Enabled {
			return fmt.Errorf("cooldown_seconds must be between 1-7200")
		}
		settings.CooldownSeconds = 5
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal 429 cooldown settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyRateLimit429CooldownSettings, string(data))
}

// GetOIDCConnectOAuthConfig 返回用于登录的“最终生效” OIDC 配置。
//
// 优先级：
// - 若对应系统设置键存在，则覆盖 config.yaml/env 的值
// - 否则回退到 config.yaml/env 的值
func (s *SettingService) GetOIDCConnectOAuthConfig(ctx context.Context) (config.OIDCConnectConfig, error) {
	if s == nil || s.cfg == nil {
		return config.OIDCConnectConfig{}, infraerrors.ServiceUnavailable("CONFIG_NOT_READY", "config not loaded")
	}

	effective := s.cfg.OIDC

	keys := []string{
		SettingKeyOIDCConnectEnabled,
		SettingKeyOIDCConnectProviderName,
		SettingKeyOIDCConnectClientID,
		SettingKeyOIDCConnectClientSecret,
		SettingKeyOIDCConnectIssuerURL,
		SettingKeyOIDCConnectDiscoveryURL,
		SettingKeyOIDCConnectAuthorizeURL,
		SettingKeyOIDCConnectTokenURL,
		SettingKeyOIDCConnectUserInfoURL,
		SettingKeyOIDCConnectJWKSURL,
		SettingKeyOIDCConnectScopes,
		SettingKeyOIDCConnectRedirectURL,
		SettingKeyOIDCConnectFrontendRedirectURL,
		SettingKeyOIDCConnectTokenAuthMethod,
		SettingKeyOIDCConnectUsePKCE,
		SettingKeyOIDCConnectValidateIDToken,
		SettingKeyOIDCConnectAllowedSigningAlgs,
		SettingKeyOIDCConnectClockSkewSeconds,
		SettingKeyOIDCConnectRequireEmailVerified,
		SettingKeyOIDCConnectUserInfoEmailPath,
		SettingKeyOIDCConnectUserInfoIDPath,
		SettingKeyOIDCConnectUserInfoUsernamePath,
	}
	settings, err := s.settingRepo.GetMultiple(ctx, keys)
	if err != nil {
		return config.OIDCConnectConfig{}, fmt.Errorf("get oidc connect settings: %w", err)
	}

	if raw, ok := settings[SettingKeyOIDCConnectEnabled]; ok {
		effective.Enabled = raw == "true"
	}
	if v, ok := settings[SettingKeyOIDCConnectProviderName]; ok && strings.TrimSpace(v) != "" {
		effective.ProviderName = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectClientID]; ok && strings.TrimSpace(v) != "" {
		effective.ClientID = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectClientSecret]; ok && strings.TrimSpace(v) != "" {
		effective.ClientSecret = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectIssuerURL]; ok && strings.TrimSpace(v) != "" {
		effective.IssuerURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectDiscoveryURL]; ok && strings.TrimSpace(v) != "" {
		effective.DiscoveryURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectAuthorizeURL]; ok && strings.TrimSpace(v) != "" {
		effective.AuthorizeURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectTokenURL]; ok && strings.TrimSpace(v) != "" {
		effective.TokenURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoURL]; ok && strings.TrimSpace(v) != "" {
		effective.UserInfoURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectJWKSURL]; ok && strings.TrimSpace(v) != "" {
		effective.JWKSURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectScopes]; ok && strings.TrimSpace(v) != "" {
		effective.Scopes = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectRedirectURL]; ok && strings.TrimSpace(v) != "" {
		effective.RedirectURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectFrontendRedirectURL]; ok && strings.TrimSpace(v) != "" {
		effective.FrontendRedirectURL = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectTokenAuthMethod]; ok && strings.TrimSpace(v) != "" {
		effective.TokenAuthMethod = strings.ToLower(strings.TrimSpace(v))
	}
	if raw, ok := settings[SettingKeyOIDCConnectUsePKCE]; ok {
		effective.UsePKCE = raw == "true"
	} else {
		effective.UsePKCE = oidcUsePKCECompatibilityDefault(effective)
	}
	if raw, ok := settings[SettingKeyOIDCConnectValidateIDToken]; ok {
		effective.ValidateIDToken = raw == "true"
	} else {
		effective.ValidateIDToken = oidcValidateIDTokenCompatibilityDefault(effective)
	}
	if v, ok := settings[SettingKeyOIDCConnectAllowedSigningAlgs]; ok && strings.TrimSpace(v) != "" {
		effective.AllowedSigningAlgs = strings.TrimSpace(v)
	}
	if raw, ok := settings[SettingKeyOIDCConnectClockSkewSeconds]; ok && strings.TrimSpace(raw) != "" {
		if parsed, parseErr := strconv.Atoi(strings.TrimSpace(raw)); parseErr == nil {
			effective.ClockSkewSeconds = parsed
		}
	}
	if raw, ok := settings[SettingKeyOIDCConnectRequireEmailVerified]; ok {
		effective.RequireEmailVerified = raw == "true"
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoEmailPath]; ok {
		effective.UserInfoEmailPath = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoIDPath]; ok {
		effective.UserInfoIDPath = strings.TrimSpace(v)
	}
	if v, ok := settings[SettingKeyOIDCConnectUserInfoUsernamePath]; ok {
		effective.UserInfoUsernamePath = strings.TrimSpace(v)
	}

	if !effective.Enabled {
		return config.OIDCConnectConfig{}, infraerrors.NotFound("OAUTH_DISABLED", "oauth login is disabled")
	}
	if strings.TrimSpace(effective.ProviderName) == "" {
		effective.ProviderName = "OIDC"
	}
	if strings.TrimSpace(effective.ClientID) == "" {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth client id not configured")
	}
	if strings.TrimSpace(effective.IssuerURL) == "" {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth issuer url not configured")
	}
	if strings.TrimSpace(effective.RedirectURL) == "" {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url not configured")
	}
	if strings.TrimSpace(effective.FrontendRedirectURL) == "" {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth frontend redirect url not configured")
	}
	if !scopesContainOpenID(effective.Scopes) {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth scopes must contain openid")
	}
	if effective.ClockSkewSeconds < 0 || effective.ClockSkewSeconds > 600 {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth clock skew must be between 0 and 600")
	}

	if err := config.ValidateAbsoluteHTTPURL(effective.IssuerURL); err != nil {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth issuer url invalid")
	}

	discoveryURL := strings.TrimSpace(effective.DiscoveryURL)
	if discoveryURL == "" {
		discoveryURL = oidcDefaultDiscoveryURL(effective.IssuerURL)
		effective.DiscoveryURL = discoveryURL
	}
	if discoveryURL != "" {
		if err := config.ValidateAbsoluteHTTPURL(discoveryURL); err != nil {
			return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth discovery url invalid")
		}
	}

	needsDiscovery := strings.TrimSpace(effective.AuthorizeURL) == "" ||
		strings.TrimSpace(effective.TokenURL) == "" ||
		(effective.ValidateIDToken && strings.TrimSpace(effective.JWKSURL) == "")
	if needsDiscovery && discoveryURL != "" {
		metadata, resolveErr := oidcResolveProviderMetadata(ctx, discoveryURL)
		if resolveErr != nil {
			return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth discovery resolve failed").WithCause(resolveErr)
		}
		if strings.TrimSpace(effective.AuthorizeURL) == "" {
			effective.AuthorizeURL = strings.TrimSpace(metadata.AuthorizationEndpoint)
		}
		if strings.TrimSpace(effective.TokenURL) == "" {
			effective.TokenURL = strings.TrimSpace(metadata.TokenEndpoint)
		}
		if strings.TrimSpace(effective.UserInfoURL) == "" {
			effective.UserInfoURL = strings.TrimSpace(metadata.UserInfoEndpoint)
		}
		if strings.TrimSpace(effective.JWKSURL) == "" {
			effective.JWKSURL = strings.TrimSpace(metadata.JWKSURI)
		}
	}

	if strings.TrimSpace(effective.AuthorizeURL) == "" {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth authorize url not configured")
	}
	if strings.TrimSpace(effective.TokenURL) == "" {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth token url not configured")
	}
	if err := config.ValidateAbsoluteHTTPURL(effective.AuthorizeURL); err != nil {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth authorize url invalid")
	}
	if err := config.ValidateAbsoluteHTTPURL(effective.TokenURL); err != nil {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth token url invalid")
	}
	if v := strings.TrimSpace(effective.UserInfoURL); v != "" {
		if err := config.ValidateAbsoluteHTTPURL(v); err != nil {
			return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth userinfo url invalid")
		}
	}
	if effective.ValidateIDToken {
		if strings.TrimSpace(effective.JWKSURL) == "" {
			return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth jwks url not configured")
		}
		if strings.TrimSpace(effective.AllowedSigningAlgs) == "" {
			return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth signing algs not configured")
		}
	}
	if v := strings.TrimSpace(effective.JWKSURL); v != "" {
		if err := config.ValidateAbsoluteHTTPURL(v); err != nil {
			return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth jwks url invalid")
		}
	}
	if err := config.ValidateAbsoluteHTTPURL(effective.RedirectURL); err != nil {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth redirect url invalid")
	}
	if err := config.ValidateFrontendRedirectURL(effective.FrontendRedirectURL); err != nil {
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth frontend redirect url invalid")
	}

	method := strings.ToLower(strings.TrimSpace(effective.TokenAuthMethod))
	switch method {
	case "", "client_secret_post", "client_secret_basic":
		if strings.TrimSpace(effective.ClientSecret) == "" {
			return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth client secret not configured")
		}
	case "none":
	default:
		return config.OIDCConnectConfig{}, infraerrors.InternalServer("OAUTH_CONFIG_INVALID", "oauth token_auth_method invalid")
	}

	return effective, nil
}

func scopesContainOpenID(scopes string) bool {
	for _, scope := range strings.Fields(strings.ToLower(strings.TrimSpace(scopes))) {
		if scope == "openid" {
			return true
		}
	}
	return false
}

type oidcProviderMetadata struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

func oidcDefaultDiscoveryURL(issuerURL string) string {
	issuerURL = strings.TrimSpace(issuerURL)
	if issuerURL == "" {
		return ""
	}
	return strings.TrimRight(issuerURL, "/") + "/.well-known/openid-configuration"
}

func oidcResolveProviderMetadata(ctx context.Context, discoveryURL string) (*oidcProviderMetadata, error) {
	discoveryURL = strings.TrimSpace(discoveryURL)
	if discoveryURL == "" {
		return nil, fmt.Errorf("discovery url is empty")
	}

	resp, err := req.C().
		SetTimeout(15*time.Second).
		R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		Get(discoveryURL)
	if err != nil {
		return nil, fmt.Errorf("request discovery document: %w", err)
	}
	if !resp.IsSuccessState() {
		return nil, fmt.Errorf("discovery request failed: status=%d", resp.StatusCode)
	}

	metadata := &oidcProviderMetadata{}
	if err := json.Unmarshal(resp.Bytes(), metadata); err != nil {
		return nil, fmt.Errorf("parse discovery document: %w", err)
	}
	return metadata, nil
}

// GetStreamTimeoutSettings 获取流超时处理配置
func (s *SettingService) GetStreamTimeoutSettings(ctx context.Context) (*StreamTimeoutSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyStreamTimeoutSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultStreamTimeoutSettings(), nil
		}
		return nil, fmt.Errorf("get stream timeout settings: %w", err)
	}
	if value == "" {
		return DefaultStreamTimeoutSettings(), nil
	}

	var settings StreamTimeoutSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultStreamTimeoutSettings(), nil
	}

	// 验证并修正配置值
	if settings.TempUnschedMinutes < 1 {
		settings.TempUnschedMinutes = 1
	}
	if settings.TempUnschedMinutes > 60 {
		settings.TempUnschedMinutes = 60
	}
	if settings.ThresholdCount < 1 {
		settings.ThresholdCount = 1
	}
	if settings.ThresholdCount > 10 {
		settings.ThresholdCount = 10
	}
	if settings.ThresholdWindowMinutes < 1 {
		settings.ThresholdWindowMinutes = 1
	}
	if settings.ThresholdWindowMinutes > 60 {
		settings.ThresholdWindowMinutes = 60
	}

	// 验证 action
	switch settings.Action {
	case StreamTimeoutActionTempUnsched, StreamTimeoutActionError, StreamTimeoutActionNone:
		// valid
	default:
		settings.Action = StreamTimeoutActionTempUnsched
	}

	return &settings, nil
}

// IsUngroupedKeySchedulingAllowed 查询是否允许未分组 Key 调度
func (s *SettingService) IsUngroupedKeySchedulingAllowed(ctx context.Context) bool {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyAllowUngroupedKeyScheduling)
	if err != nil {
		return false // fail-closed: 查询失败时默认不允许
	}
	return value == "true"
}

// GetClaudeCodeVersionBounds 获取 Claude Code 版本号上下限要求
// 使用进程内 atomic.Value 缓存，60 秒 TTL，热路径零锁开销
// singleflight 防止缓存过期时 thundering herd
// 返回空字符串表示不做对应方向的版本检查
func (s *SettingService) GetClaudeCodeVersionBounds(ctx context.Context) (min, max string) {
	if cached, ok := versionBoundsCache.Load().(*cachedVersionBounds); ok {
		if time.Now().UnixNano() < cached.expiresAt {
			return cached.min, cached.max
		}
	}
	// singleflight: 同一时刻只有一个 goroutine 查询 DB，其余复用结果
	type bounds struct{ min, max string }
	result, err, _ := versionBoundsSF.Do("version_bounds", func() (any, error) {
		// 二次检查，避免排队的 goroutine 重复查询
		if cached, ok := versionBoundsCache.Load().(*cachedVersionBounds); ok {
			if time.Now().UnixNano() < cached.expiresAt {
				return bounds{cached.min, cached.max}, nil
			}
		}
		// 使用独立 context：断开请求取消链，避免客户端断连导致空值被长期缓存
		dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), versionBoundsDBTimeout)
		defer cancel()
		values, err := s.settingRepo.GetMultiple(dbCtx, []string{
			SettingKeyMinClaudeCodeVersion,
			SettingKeyMaxClaudeCodeVersion,
		})
		if err != nil {
			// fail-open: DB 错误时不阻塞请求，但记录日志并使用短 TTL 快速重试
			slog.Warn("failed to get claude code version bounds setting, skipping version check", "error", err)
			versionBoundsCache.Store(&cachedVersionBounds{
				min:       "",
				max:       "",
				expiresAt: time.Now().Add(versionBoundsErrorTTL).UnixNano(),
			})
			return bounds{"", ""}, nil
		}
		b := bounds{
			min: values[SettingKeyMinClaudeCodeVersion],
			max: values[SettingKeyMaxClaudeCodeVersion],
		}
		versionBoundsCache.Store(&cachedVersionBounds{
			min:       b.min,
			max:       b.max,
			expiresAt: time.Now().Add(versionBoundsCacheTTL).UnixNano(),
		})
		return b, nil
	})
	if err != nil {
		return "", ""
	}
	b, ok := result.(bounds)
	if !ok {
		return "", ""
	}
	return b.min, b.max
}

// GetOpenAIQuotaAutoPauseSettings returns the current global default quota auto-pause
// settings. It is invoked on the OpenAI scheduling hot path (once per request) and is
// therefore designed to never block on the DB:
//
//   - Fresh cached value → returned immediately.
//   - Stale or empty cache → the last known value is returned, and a background
//     goroutine refreshes the cache via singleflight (stale-while-revalidate).
//   - First call with no cache yet → zero defaults are returned and the same async
//     refresh is kicked off; the next call gets the freshly populated value.
//
// Callers that need the freshly persisted value synchronously (tests, post-update
// confirmation, optional startup warm-up) should call WarmOpenAIQuotaAutoPauseSettings.
func (s *SettingService) GetOpenAIQuotaAutoPauseSettings(ctx context.Context) OpsOpenAIAccountQuotaAutoPauseSettings {
	if s == nil {
		return OpsOpenAIAccountQuotaAutoPauseSettings{}
	}
	cached, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings)
	now := time.Now().UnixNano()
	if cached != nil && now < cached.expiresAt {
		return cached.settings
	}
	// Stale or unset: trigger background refresh without blocking this request.
	// singleflight.DoChan dedupes concurrent refreshes; we deliberately ignore the
	// returned channel — the result is observable via the atomic cache.
	s.openAIQuotaAutoPauseSettingsSF.DoChan(openAIQuotaAutoPauseSettingsRefreshKey, func() (any, error) {
		s.refreshOpenAIQuotaAutoPauseSettings(context.Background())
		return nil, nil
	})
	if cached != nil {
		return cached.settings // serve stale value while revalidating
	}
	return OpsOpenAIAccountQuotaAutoPauseSettings{}
}

// WarmOpenAIQuotaAutoPauseSettings synchronously loads the quota auto-pause settings
// into the in-memory cache. Useful for application startup (so the first request hits
// a warm cache) and for tests that need deterministic reads immediately after
// constructing the service.
func (s *SettingService) WarmOpenAIQuotaAutoPauseSettings(ctx context.Context) OpsOpenAIAccountQuotaAutoPauseSettings {
	if s == nil {
		return OpsOpenAIAccountQuotaAutoPauseSettings{}
	}
	s.refreshOpenAIQuotaAutoPauseSettings(ctx)
	cached, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings)
	if cached == nil {
		return OpsOpenAIAccountQuotaAutoPauseSettings{}
	}
	return cached.settings
}

// refreshOpenAIQuotaAutoPauseSettings reads the latest settings from the DB and stores
// them into the in-memory cache. On error it stores the prior value (or zero defaults
// if nothing is cached yet) with the shorter error TTL so the next refresh comes
// sooner. Always uses its own timeout-bounded context to keep refresh latency
// predictable regardless of the caller.
func (s *SettingService) refreshOpenAIQuotaAutoPauseSettings(ctx context.Context) {
	if s == nil || s.settingRepo == nil {
		return
	}
	dbCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), openAIQuotaAutoPauseSettingsDBTimeout)
	defer cancel()

	settings := OpsOpenAIAccountQuotaAutoPauseSettings{}
	ttl := openAIQuotaAutoPauseSettingsCacheTTL
	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyOpsAdvancedSettings)
	if err == nil {
		cfg := defaultOpsAdvancedSettings()
		if strings.TrimSpace(raw) != "" {
			if jsonErr := json.Unmarshal([]byte(raw), cfg); jsonErr == nil {
				normalizeOpsAdvancedSettings(cfg)
			}
		}
		settings = cfg.OpenAIAccountQuotaAutoPause
	} else if !errors.Is(err, ErrSettingNotFound) {
		// Real error: keep serving prior value but refresh sooner.
		if prior, _ := s.openAIQuotaAutoPauseSettingsCache.Load().(*cachedOpenAIQuotaAutoPauseSettings); prior != nil {
			settings = prior.settings
		}
		ttl = openAIQuotaAutoPauseSettingsErrorTTL
	}

	s.openAIQuotaAutoPauseSettingsCache.Store(&cachedOpenAIQuotaAutoPauseSettings{
		settings:  settings,
		expiresAt: time.Now().Add(ttl).UnixNano(),
	})
}

// SetOpenAIQuotaAutoPauseSettings writes the given settings directly into the in-memory
// cache. Called from settings-write code paths so that the next read reflects the new
// value immediately, without waiting for the background refresh.
func (s *SettingService) SetOpenAIQuotaAutoPauseSettings(settings OpsOpenAIAccountQuotaAutoPauseSettings) {
	if s == nil {
		return
	}
	s.openAIQuotaAutoPauseSettingsCache.Store(&cachedOpenAIQuotaAutoPauseSettings{
		settings:  settings,
		expiresAt: time.Now().Add(openAIQuotaAutoPauseSettingsCacheTTL).UnixNano(),
	})
}

// GetRectifierSettings 获取请求整流器配置
func (s *SettingService) GetRectifierSettings(ctx context.Context) (*RectifierSettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyRectifierSettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultRectifierSettings(), nil
		}
		return nil, fmt.Errorf("get rectifier settings: %w", err)
	}
	if value == "" {
		return DefaultRectifierSettings(), nil
	}

	var settings RectifierSettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultRectifierSettings(), nil
	}

	return &settings, nil
}

// SetRectifierSettings 设置请求整流器配置
func (s *SettingService) SetRectifierSettings(ctx context.Context, settings *RectifierSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal rectifier settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyRectifierSettings, string(data))
}

// IsSignatureRectifierEnabled 判断签名整流是否启用（总开关 && 签名子开关）
func (s *SettingService) IsSignatureRectifierEnabled(ctx context.Context) bool {
	settings, err := s.GetRectifierSettings(ctx)
	if err != nil {
		return true // fail-open: 查询失败时默认启用
	}
	return settings.Enabled && settings.ThinkingSignatureEnabled
}

// IsBudgetRectifierEnabled 判断 Budget 整流是否启用（总开关 && Budget 子开关）
func (s *SettingService) IsBudgetRectifierEnabled(ctx context.Context) bool {
	settings, err := s.GetRectifierSettings(ctx)
	if err != nil {
		return true // fail-open: 查询失败时默认启用
	}
	return settings.Enabled && settings.ThinkingBudgetEnabled
}

// GetBetaPolicySettings 获取 Beta 策略配置
func (s *SettingService) GetBetaPolicySettings(ctx context.Context) (*BetaPolicySettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyBetaPolicySettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultBetaPolicySettings(), nil
		}
		return nil, fmt.Errorf("get beta policy settings: %w", err)
	}
	if value == "" {
		return DefaultBetaPolicySettings(), nil
	}

	var settings BetaPolicySettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		return DefaultBetaPolicySettings(), nil
	}

	return &settings, nil
}

// SetBetaPolicySettings 设置 Beta 策略配置
func (s *SettingService) SetBetaPolicySettings(ctx context.Context, settings *BetaPolicySettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	validActions := map[string]bool{
		BetaPolicyActionPass: true, BetaPolicyActionFilter: true, BetaPolicyActionBlock: true,
	}
	validScopes := map[string]bool{
		BetaPolicyScopeAll: true, BetaPolicyScopeOAuth: true, BetaPolicyScopeAPIKey: true, BetaPolicyScopeBedrock: true,
	}

	for i, rule := range settings.Rules {
		if rule.BetaToken == "" {
			return fmt.Errorf("rule[%d]: beta_token cannot be empty", i)
		}
		if !validActions[rule.Action] {
			return fmt.Errorf("rule[%d]: invalid action %q", i, rule.Action)
		}
		if !validScopes[rule.Scope] {
			return fmt.Errorf("rule[%d]: invalid scope %q", i, rule.Scope)
		}
		// Validate model_whitelist patterns
		for j, pattern := range rule.ModelWhitelist {
			trimmed := strings.TrimSpace(pattern)
			if trimmed == "" {
				return fmt.Errorf("rule[%d]: model_whitelist[%d] cannot be empty", i, j)
			}
			settings.Rules[i].ModelWhitelist[j] = trimmed
		}
		// Validate fallback_action
		if rule.FallbackAction != "" && !validActions[rule.FallbackAction] {
			return fmt.Errorf("rule[%d]: invalid fallback_action %q", i, rule.FallbackAction)
		}
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal beta policy settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyBetaPolicySettings, string(data))
}

// GetOpenAIFastPolicySettings 获取 OpenAI fast 策略配置
func (s *SettingService) GetOpenAIFastPolicySettings(ctx context.Context) (*OpenAIFastPolicySettings, error) {
	value, err := s.settingRepo.GetValue(ctx, SettingKeyOpenAIFastPolicySettings)
	if err != nil {
		if errors.Is(err, ErrSettingNotFound) {
			return DefaultOpenAIFastPolicySettings(), nil
		}
		return nil, fmt.Errorf("get openai fast policy settings: %w", err)
	}
	if value == "" {
		return DefaultOpenAIFastPolicySettings(), nil
	}

	var settings OpenAIFastPolicySettings
	if err := json.Unmarshal([]byte(value), &settings); err != nil {
		// JSON 损坏时静默 fallback 到默认配置会让策略意外失效（管理员配
		// 置的 block/filter 规则被忽略）。记录 Warn 让运维能在出现异常
		// 行为时定位到 settings 表里的脏数据。
		slog.Warn("failed to unmarshal openai fast policy settings, falling back to defaults",
			"error", err,
			"key", SettingKeyOpenAIFastPolicySettings)
		return DefaultOpenAIFastPolicySettings(), nil
	}

	return &settings, nil
}

// SetOpenAIFastPolicySettings 设置 OpenAI fast 策略配置
func (s *SettingService) SetOpenAIFastPolicySettings(ctx context.Context, settings *OpenAIFastPolicySettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	validActions := map[string]bool{
		BetaPolicyActionPass: true, BetaPolicyActionFilter: true, BetaPolicyActionBlock: true,
	}
	validScopes := map[string]bool{
		BetaPolicyScopeAll: true, BetaPolicyScopeOAuth: true, BetaPolicyScopeAPIKey: true, BetaPolicyScopeBedrock: true,
	}
	validTiers := map[string]bool{
		OpenAIFastTierAny: true, OpenAIFastTierPriority: true, OpenAIFastTierFlex: true,
	}

	for i, rule := range settings.Rules {
		tier := strings.ToLower(strings.TrimSpace(rule.ServiceTier))
		if tier == "" {
			tier = OpenAIFastTierAny
		}
		if !validTiers[tier] {
			return fmt.Errorf("rule[%d]: invalid service_tier %q", i, rule.ServiceTier)
		}
		settings.Rules[i].ServiceTier = tier
		if !validActions[rule.Action] {
			return fmt.Errorf("rule[%d]: invalid action %q", i, rule.Action)
		}
		if !validScopes[rule.Scope] {
			return fmt.Errorf("rule[%d]: invalid scope %q", i, rule.Scope)
		}
		for j, pattern := range rule.ModelWhitelist {
			trimmed := strings.TrimSpace(pattern)
			if trimmed == "" {
				return fmt.Errorf("rule[%d]: model_whitelist[%d] cannot be empty", i, j)
			}
			settings.Rules[i].ModelWhitelist[j] = trimmed
		}
		if rule.FallbackAction != "" && !validActions[rule.FallbackAction] {
			return fmt.Errorf("rule[%d]: invalid fallback_action %q", i, rule.FallbackAction)
		}
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal openai fast policy settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyOpenAIFastPolicySettings, string(data))
}

// SetStreamTimeoutSettings 设置流超时处理配置
func (s *SettingService) SetStreamTimeoutSettings(ctx context.Context, settings *StreamTimeoutSettings) error {
	if settings == nil {
		return fmt.Errorf("settings cannot be nil")
	}

	// 验证配置值
	if settings.TempUnschedMinutes < 1 || settings.TempUnschedMinutes > 60 {
		return fmt.Errorf("temp_unsched_minutes must be between 1-60")
	}
	if settings.ThresholdCount < 1 || settings.ThresholdCount > 10 {
		return fmt.Errorf("threshold_count must be between 1-10")
	}
	if settings.ThresholdWindowMinutes < 1 || settings.ThresholdWindowMinutes > 60 {
		return fmt.Errorf("threshold_window_minutes must be between 1-60")
	}

	switch settings.Action {
	case StreamTimeoutActionTempUnsched, StreamTimeoutActionError, StreamTimeoutActionNone:
		// valid
	default:
		return fmt.Errorf("invalid action: %s", settings.Action)
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal stream timeout settings: %w", err)
	}

	return s.settingRepo.Set(ctx, SettingKeyStreamTimeoutSettings, string(data))
}

// GetDefaultPlatformQuotas 读取系统全局 platform quota JSON key，返回 4 platform x 3 window 的设置。
// 永远返回包含全部 4 platform key 的 map（值可能为零值/nil 字段，表示"上层未配置 = 不限制"）。
//
// 使用单个 JSON key（default_platform_quotas），一次 DB roundtrip，消除旧 12-KV 格式的 N+1 问题。
// 容错语义：取值失败或 unmarshal 失败 → 返回补齐 4 key 的空 map（fail-open，注册不被阻断）。
func (s *SettingService) GetDefaultPlatformQuotas(ctx context.Context) (map[string]*DefaultPlatformQuotaSetting, error) {
	out := map[string]*DefaultPlatformQuotaSetting{
		"anthropic":   {},
		"openai":      {},
		"gemini":      {},
		"antigravity": {},
	}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyDefaultPlatformQuotas)
	if err != nil || raw == "" {
		return out, nil // 无配置 = 全部不限制
	}
	parsed := map[string]*DefaultPlatformQuotaSetting{}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		slog.Warn("[Setting] unmarshal default_platform_quotas failed (fail-open)", "error", err)
		return out, nil
	}
	for _, platform := range AllowedQuotaPlatforms {
		if v := parsed[platform]; v != nil {
			out[platform] = v
		}
	}
	return out, nil // 补齐 4 platform key，保持与旧实现一致的下游契约
}

// GetAuthSourcePlatformQuotas 读取指定 auth source 的 platform quota 覆盖（仅返回有配置的平台，override 语义）。
func (s *SettingService) GetAuthSourcePlatformQuotas(ctx context.Context, source string) map[string]*DefaultPlatformQuotaSetting {
	out := map[string]*DefaultPlatformQuotaSetting{}
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyAuthSourcePlatformQuotas(source))
	if err != nil || raw == "" {
		return out // 无 override
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		slog.Warn("[Setting] unmarshal auth source platform quotas failed (fail-open)", "source", source, "error", err)
		return map[string]*DefaultPlatformQuotaSetting{}
	}
	return out // 仅含已配置平台，保持 override 语义
}

// mergePlatformQuotaDefaults 按字段级 patch：src 中非 nil 字段覆盖 dst。
// 区分 nil（"未配置"，保留 dst）vs &0.0（"显式禁用"，覆盖 dst 为 0）
func mergePlatformQuotaDefaults(dst, src *DefaultPlatformQuotaSetting) {
	if src == nil || dst == nil {
		return
	}
	if src.DailyLimitUSD != nil {
		dst.DailyLimitUSD = src.DailyLimitUSD
	}
	if src.WeeklyLimitUSD != nil {
		dst.WeeklyLimitUSD = src.WeeklyLimitUSD
	}
	if src.MonthlyLimitUSD != nil {
		dst.MonthlyLimitUSD = src.MonthlyLimitUSD
	}
}
