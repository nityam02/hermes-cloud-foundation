package awsinv

import (
	"context"
	"errors"
	"net"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

var (
	accessDeniedCodes = map[string]bool{
		"AccessDenied": true, "AccessDeniedException": true, "UnauthorizedOperation": true,
		"AuthorizationError": true, "AuthorizationErrorException": true, "UnauthorizedAccess": true,
		"Forbidden": true, "ForbiddenException": true, "AccessDeniedFault": true, "UnauthorizedException": true,
	}
	throttleCodes = map[string]bool{
		"Throttling": true, "ThrottlingException": true, "ThrottledException": true, "RequestLimitExceeded": true,
		"TooManyRequestsException": true, "SlowDown": true, "RequestThrottled": true, "RequestThrottledException": true,
		"ProvisionedThroughputExceededException": true, "Throttled": true,
	}
	unsupportedCodes = map[string]bool{
		"UnsupportedOperation": true, "UnsupportedOperationException": true, "InvalidAction": true,
		"UnknownOperationException": true, "NotImplemented": true, "NotImplementedException": true,
		"InvalidParameterValue.UnsupportedOperation": true,
	}
	notEnabledCodes = map[string]bool{
		"SubscriptionRequiredException": true, "NotSubscribed": true,
		"ServiceNotEnabled": true,
	}
)

// ClassifyError maps an SDK error to a gap reason and a short message.
func ClassifyError(err error) (reason, message string) {
	message = err.Error()
	if len(message) > 400 {
		message = message[:400] + "..."
	}
	var ge *GuardError
	if errors.As(err, &ge) {
		return inventory.GapError, message
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		switch {
		case accessDeniedCodes[code]:
			return inventory.GapAccessDenied, message
		case code == "OptInRequired" || code == "OptInRequiredException":
			return inventory.GapOptInDisabled, message
		case throttleCodes[code]:
			return inventory.GapThrottled, message
		case unsupportedCodes[code]:
			return inventory.GapUnsupported, message
		case notEnabledCodes[code]:
			return inventory.GapNotEnabled, message
		}
		low := strings.ToLower(apiErr.ErrorMessage())
		if strings.Contains(low, "not been implemented") || strings.Contains(low, "not yet implemented") {
			return inventory.GapUnsupported, message
		}
	}
	var maxErr *retry.MaxAttemptsError
	if errors.As(err, &maxErr) && strings.Contains(strings.ToLower(message), "throttl") {
		return inventory.GapThrottled, message
	}
	var respErr *smithyhttp.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.HTTPStatusCode() {
		case 501:
			return inventory.GapUnsupported, message
		case 404:
			// No API error code: the endpoint does not implement the operation or protocol
			// (seen with emulators); real "not found" errors carry an error code.
			return inventory.GapUnsupported, message
		case 403:
			return inventory.GapAccessDenied, message
		case 429:
			return inventory.GapThrottled, message
		}
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return inventory.GapUnsupported, message
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return inventory.GapError, message
	}
	low := strings.ToLower(message)
	if strings.Contains(low, "not been implemented") || strings.Contains(low, "not yet implemented") || strings.Contains(low, "endpoint") && strings.Contains(low, "not found") {
		return inventory.GapUnsupported, message
	}
	return inventory.GapError, message
}

// isNotFound reports configuration-absent errors that mean "feature not configured", not a gap
// (for example NoSuchBucketPolicy).
func isNotFound(err error, codes ...string) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	for _, c := range codes {
		if apiErr.ErrorCode() == c {
			return true
		}
	}
	return false
}
