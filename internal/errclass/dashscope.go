package errclass

import (
	"strings"

	"github.com/NSObjects/go-speech/speecherr"
)

// DashScope classifies shared ASR/TTS task-failed codes. Unknown codes
// do not imply retryability. AllocationQuota can denote throughput limits,
// not necessarily exhausted paid credit; retain a conservative hint until
// callers can distinguish the applicable quota and request replay safety.
// Reference: https://help.aliyun.com/en/model-studio/rate-limiting-best-practices
func DashScope(raw string) (code speecherr.Code, retryable, known bool) {
	remote := strings.ToLower(strings.TrimSpace(raw))
	switch remote {
	case "invalidapikey", "invalid_api_key", "authenticationfailed", "authenticationerror",
		"accessdenied", "access_denied", "unauthorized", "permissiondenied":
		return speecherr.CodeUnauthorized, false, true
	case "invalidparameter", "invalid_parameter_error", "modelnotfound", "model_not_found", "notfound":
		return speecherr.CodeInvalidArgument, false, true
	case "throttling", "throttling.ratequota", "throttling.burstrate", "limit_requests", "limit_burst_rate",
		"ratelimit", "ratelimitexceeded", "too_many_requests":
		return speecherr.CodeRateLimited, true, true
	case "throttling.allocationquota", "insufficient_quota", "arrearage", "servicequotaexhausted",
		"quotaexceeded", "allocationquotaexceeded", "billingerror", "insufficientbalance":
		return speecherr.CodeRateLimited, false, true
	case "requesttimeout", "request_timeout":
		return speecherr.CodeTimeout, true, true
	case "serviceunavailable", "service_unavailable", "internalservererror":
		return speecherr.CodeUnavailable, true, true
	case "internalerror":
		return speecherr.CodeInternal, false, true
	}
	if strings.HasPrefix(remote, "invalidparameter.") {
		return speecherr.CodeInvalidArgument, false, true
	}
	return speecherr.CodeInternal, false, false
}
