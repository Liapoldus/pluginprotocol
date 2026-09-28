package transport

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/Liapoldus/pluginprotocol/pluginv1"
	"google.golang.org/protobuf/proto"
)

var (
	ErrInvalidDispatchGeneration       = errors.New("dispatch generation is invalid")
	ErrDispatchGenerationPrecondition = errors.New("dispatch generation precondition failed")
)

// DispatchGeneration is the per-replica active authorization snapshot. It is
// empty until a control-plane client applies a generation successfully.
type DispatchGeneration struct {
	mu       sync.RWMutex
	request  *pluginv1.DispatchApplyRequest
	response *pluginv1.DispatchApplyResponse
}

func NewDispatchGeneration() *DispatchGeneration { return &DispatchGeneration{} }

func (generation *DispatchGeneration) Allows(capability string, mode pluginv1.InvocationMode) bool {
	if generation == nil || capability == "" || mode == pluginv1.InvocationMode_INVOCATION_MODE_UNSPECIFIED {
		return false
	}
	generation.mu.RLock()
	defer generation.mu.RUnlock()
	if generation.request == nil {
		return false
	}
	for _, candidate := range generation.request.GetCapabilities() {
		if candidate.GetCapability() != capability {
			continue
		}
		for _, allowed := range candidate.GetModes() {
			if allowed == mode {
				return true
			}
		}
	}
	return false
}

func (generation *DispatchGeneration) apply(request *pluginv1.DispatchApplyRequest, instanceID, replicaIdentity string, manifest *pluginv1.Manifest, allowsCapability func(string) bool) (*pluginv1.DispatchApplyResponse, error) {
	if generation == nil || !validDispatchRequest(request, instanceID, manifest, allowsCapability) || !validRemoteIdentity(replicaIdentity) {
		return nil, ErrInvalidDispatchGeneration
	}
	manifestBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(manifest)
	if err != nil {
		return nil, ErrInvalidDispatchGeneration
	}
	manifestDigest := digest(manifestBytes)
	canonical := canonicalDispatchRequest(request)
	dispatchBytes, err := (proto.MarshalOptions{Deterministic: true}).Marshal(canonical)
	if err != nil {
		return nil, ErrInvalidDispatchGeneration
	}
	dispatchDigest := digest(dispatchBytes)
	response := &pluginv1.DispatchApplyResponse{
		Generation: request.GetGeneration(), ReplicaIdentityUri: replicaIdentity,
		ManifestDigest: manifestDigest, SettingsDigest: request.GetSettingsDigest(),
		ReleaseDigest: request.GetReleaseDigest(), DispatchDigest: dispatchDigest,
	}

	generation.mu.Lock()
	defer generation.mu.Unlock()
	if generation.request != nil {
		active := generation.request.GetGeneration()
		if request.GetGeneration() < active {
			return nil, ErrDispatchGenerationPrecondition
		}
		if request.GetGeneration() == active {
			if generation.response.GetDispatchDigest() != dispatchDigest || generation.response.GetManifestDigest() != manifestDigest {
				return nil, ErrDispatchGenerationPrecondition
			}
			return proto.Clone(generation.response).(*pluginv1.DispatchApplyResponse), nil
		}
	}
	generation.request = canonical
	generation.response = response
	return proto.Clone(response).(*pluginv1.DispatchApplyResponse), nil
}

func validDispatchRequest(request *pluginv1.DispatchApplyRequest, instanceID string, manifest *pluginv1.Manifest, allowsCapability func(string) bool) bool {
	if request == nil || request.GetGeneration() == 0 || request.GetInstanceId() != instanceID ||
		!validSHA256Digest(request.GetSettingsDigest()) || !validSHA256Digest(request.GetReleaseDigest()) ||
		manifest == nil || allowsCapability == nil {
		return false
	}
	manifestModes := make(map[string]map[pluginv1.InvocationMode]struct{}, len(manifest.GetCapabilityDescriptors()))
	for _, descriptor := range manifest.GetCapabilityDescriptors() {
		if descriptor.GetCapability() == "" {
			return false
		}
		modes := make(map[pluginv1.InvocationMode]struct{}, len(descriptor.GetModes()))
		for _, mode := range descriptor.GetModes() {
			if !validInvocationMode(mode) {
				return false
			}
			modes[mode] = struct{}{}
		}
		manifestModes[descriptor.GetCapability()] = modes
	}
	seenCapabilities := make(map[string]struct{}, len(request.GetCapabilities()))
	for _, capability := range request.GetCapabilities() {
		name := capability.GetCapability()
		if name == "" || !allowsCapability(name) || len(capability.GetModes()) == 0 {
			return false
		}
		if _, exists := seenCapabilities[name]; exists {
			return false
		}
		seenCapabilities[name] = struct{}{}
		declared, exists := manifestModes[name]
		if !exists {
			return false
		}
		seenModes := make(map[pluginv1.InvocationMode]struct{}, len(capability.GetModes()))
		for _, mode := range capability.GetModes() {
			if !validInvocationMode(mode) {
				return false
			}
			if _, exists := declared[mode]; !exists {
				return false
			}
			if _, exists := seenModes[mode]; exists {
				return false
			}
			seenModes[mode] = struct{}{}
		}
	}
	return true
}

func canonicalDispatchRequest(request *pluginv1.DispatchApplyRequest) *pluginv1.DispatchApplyRequest {
	canonical := proto.Clone(request).(*pluginv1.DispatchApplyRequest)
	sort.Slice(canonical.Capabilities, func(i, j int) bool {
		return canonical.Capabilities[i].GetCapability() < canonical.Capabilities[j].GetCapability()
	})
	for _, capability := range canonical.Capabilities {
		sort.Slice(capability.Modes, func(i, j int) bool { return capability.Modes[i] < capability.Modes[j] })
	}
	return canonical
}

func validInvocationMode(mode pluginv1.InvocationMode) bool {
	return mode >= pluginv1.InvocationMode_INVOCATION_MODE_CALL && mode <= pluginv1.InvocationMode_INVOCATION_MODE_UDP
}

func validSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func digest(value []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(value)) }
