package transport

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	ErrConfigApplyRejected          = errors.New("plugin rejected configuration")
	ErrInvalidConfigAcknowledgement = errors.New("plugin configuration acknowledgement is invalid")
)

// ApplyConfiguration pushes one complete settings revision to a plugin.
// Configuration bytes are validated as JSON but otherwise sent unchanged so
// the revision's digest remains bound to the exact payload. gRPC status details
// are classified into protocol-owned errors and are never returned directly.
func (client *Client) ApplyConfiguration(ctx context.Context, settingsRevision string, config []byte, grants []*pluginv1.ActiveGrant) error {
	if client == nil || client.service == nil || ctx == nil || settingsRevision == "" || len(config) > DefaultMaxMessageBytes || !json.Valid(config) {
		return ErrProtocolViolation
	}

	instanceID := ""
	secretReferences := make(map[string]struct{}, len(grants))
	for _, grant := range grants {
		if grant == nil || grant.GetScope() != pluginv1.GrantScope_GRANT_SCOPE_CONFIG_APPLY ||
			grant.GetInstanceId() == "" || grant.GetSettingsRevision() != settingsRevision ||
			grant.GetSecretReference() == "" || grant.GetHandle() == "" || grant.GetPurpose() == "" ||
			grant.GetCapability() != "" || len(grant.GetDomains()) != 0 {
			return ErrProtocolViolation
		}
		if instanceID != "" && grant.GetInstanceId() != instanceID {
			return ErrProtocolViolation
		}
		instanceID = grant.GetInstanceId()
		if _, exists := secretReferences[grant.GetSecretReference()]; exists {
			return ErrProtocolViolation
		}
		secretReferences[grant.GetSecretReference()] = struct{}{}
	}

	result, err := client.service.ConfigApply(ctx, &pluginv1.ConfigApplyRequest{
		Config: config, SettingsRevision: settingsRevision, Grants: grants,
	})
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if status.Code(err) == codes.FailedPrecondition {
			return ErrInvalidConfigAcknowledgement
		}
		return classifyRPCError(ctx, err)
	}
	if result == nil {
		return ErrInvalidConfigAcknowledgement
	}
	if !result.GetApplied() {
		return ErrConfigApplyRejected
	}
	if result.GetSettingsRevision() != settingsRevision {
		return ErrInvalidConfigAcknowledgement
	}
	return nil
}
