package pluginprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dlclark/regexp2"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"
)

var (
	// ErrInvalidCookiePolicy identifies a malformed or unsupported cookie policy.
	ErrInvalidCookiePolicy = errors.New("invalid plugin cookie policy")
	// ErrCookiePolicyScope identifies use of a policy outside its exact binding.
	ErrCookiePolicyScope = errors.New("plugin cookie policy scope mismatch")
	// ErrInvalidCookieRequest identifies malformed inbound cookie data.
	ErrInvalidCookieRequest = errors.New("invalid inbound plugin cookie data")
	// ErrInvalidHTTPResponseAction identifies a plugin response action that must not be committed.
	ErrInvalidHTTPResponseAction = errors.New("invalid plugin HTTP response action")
)

// CookiePair is one inbound HTTP cookie pair. Values are credentials and must
// be redacted by consumers from logs, traces, audit, and diagnostics.
type CookiePair struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// CookiePolicy is a Gateway-owned allow-list bound to one instance and
// capability. It must never be sent to the plugin.
type CookiePolicy struct {
	Version      int      `json:"version"`
	InstanceID   string   `json:"instanceId"`
	Capability   string   `json:"capability"`
	AllowedNames []string `json:"allowedNames"`
}

// CookieAction is the versioned typed Set-Cookie action. Pointer fields retain
// the distinction between omitted and explicitly supplied optional values.
type CookieAction struct {
	Name     string     `json:"name"`
	Value    string     `json:"value"`
	Path     *string    `json:"path,omitempty"`
	Domain   *string    `json:"domain,omitempty"`
	Expires  *time.Time `json:"expires,omitempty"`
	MaxAge   *int       `json:"maxAge,omitempty"`
	Secure   bool       `json:"secure"`
	HttpOnly bool       `json:"httpOnly"`
	SameSite *string    `json:"sameSite,omitempty"`
}

// HTTPResponseAction is the strict versioned plugin HTTP response action.
// Optional Body preserves the absent-versus-empty distinction.
type HTTPResponseAction struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Cookies []CookieAction    `json:"cookies,omitempty"`
	Body    *string           `json:"body,omitempty"`
}

type cookieContractMetadata struct {
	responseSchema *jsonschema.Schema
	policySchema   *jsonschema.Schema
	pairSchema     *jsonschema.Schema
	maxActions     int
	maxCookieBytes int
	maxSetBytes    int
	maxPairs       int
	maxNameChars   int
	maxValueChars  int
	maxHeaderBytes int
	sameSiteSecure string
	securePrefixes []string
	hostPrefix     string
	hostPath       string
	hostSecure     bool
	hostDomainOK   bool
	matchHost      bool
	rejectSuffix   bool
	sameSiteValues map[string]http.SameSite
}

var (
	cookieMetadataOnce sync.Once
	cookieMetadata     cookieContractMetadata
	cookieMetadataErr  error
)

func getCookieMetadata() (cookieContractMetadata, error) {
	cookieMetadataOnce.Do(func() {
		cookieMetadata, cookieMetadataErr = loadCookieMetadata()
	})
	return cookieMetadata, cookieMetadataErr
}

func loadCookieMetadata() (cookieContractMetadata, error) {
	var result cookieContractMetadata
	responseDocument, err := contractDocument("contracts/http/v1/response-action.schema.json")
	if err != nil {
		return result, err
	}
	result.responseSchema, err = compileContractSchema(responseDocument)
	if err != nil {
		return result, err
	}
	policyDocument, err := contractDocument("contracts/http/v1/cookie-policy.schema.json")
	if err != nil {
		return result, err
	}
	result.policySchema, err = compileContractSchema(policyDocument)
	if err != nil {
		return result, err
	}

	responseMap, ok := responseDocument.(map[string]any)
	if !ok {
		return result, errors.New("invalid response-action contract")
	}
	semantics, ok := responseMap["x-liapoldus-semantics"].(map[string]any)
	if !ok {
		return result, errors.New("invalid response-action contract semantics")
	}
	limits, ok := semantics["v1Limits"].(map[string]any)
	if !ok || !readInt(limits, "maxActions", &result.maxActions) || !readInt(limits, "maxSerializedCookieBytes", &result.maxCookieBytes) || !readInt(limits, "maxSerializedCookieSetBytes", &result.maxSetBytes) {
		return result, errors.New("invalid response-action contract limits")
	}
	constraints, ok := semantics["cookieConstraints"].(map[string]any)
	if !ok {
		return result, errors.New("invalid response-action cookie constraints")
	}
	var constraintOK bool
	result.sameSiteSecure, constraintOK = constraints["sameSiteRequiresSecure"].(string)
	if !constraintOK {
		return result, errors.New("invalid sameSite security constraint")
	}
	result.securePrefixes, constraintOK = readStrings(constraints["securePrefixes"])
	if !constraintOK {
		return result, errors.New("invalid secure-prefix contract")
	}
	hostPrefix, ok := constraints["hostPrefix"].(map[string]any)
	if !ok {
		return result, errors.New("invalid response-action host-prefix constraint")
	}
	result.hostPrefix, constraintOK = hostPrefix["prefix"].(string)
	if !constraintOK {
		return result, errors.New("invalid host-prefix contract")
	}
	result.hostPath, constraintOK = hostPrefix["path"].(string)
	if !constraintOK {
		return result, errors.New("invalid host-prefix contract")
	}
	result.hostSecure, constraintOK = hostPrefix["secure"].(bool)
	if !constraintOK {
		return result, errors.New("invalid host-prefix contract")
	}
	result.hostDomainOK, constraintOK = hostPrefix["domainAllowed"].(bool)
	if !constraintOK {
		return result, errors.New("invalid host-prefix contract")
	}
	domainConstraints, ok := constraints["domain"].(map[string]any)
	if !ok {
		return result, errors.New("invalid response-action domain constraints")
	}
	result.matchHost, constraintOK = domainConstraints["mustDomainMatchRequestHost"].(bool)
	if !constraintOK {
		return result, errors.New("invalid cookie domain contract")
	}
	result.rejectSuffix, constraintOK = domainConstraints["rejectPublicSuffix"].(bool)
	if !constraintOK {
		return result, errors.New("invalid cookie domain contract")
	}
	result.sameSiteValues, err = sameSiteModes(responseMap)
	if err != nil {
		return result, err
	}
	result.maxNameChars, result.maxValueChars, err = cookieFieldLimits(responseMap)
	if err != nil {
		return result, err
	}
	streamDocument, err := contractDocument("contracts/protocol/v1/stream-open-context.schema.json")
	if err != nil {
		return result, err
	}
	cookieArraySchema, err := requestCookieArraySchema(streamDocument)
	if err != nil {
		return result, err
	}
	arrayDocument, err := jsonschema.UnmarshalJSON(bytes.NewReader(cookieArraySchema))
	if err != nil {
		return result, err
	}
	compiler := newContractCompiler()
	if err := addContractResource(compiler, "contracts/http/v1/response-action.schema.json"); err != nil {
		return result, err
	}
	if err := addContractResource(compiler, "contracts/http/v1/cookie-pair.schema.json"); err != nil {
		return result, err
	}
	if err := compiler.AddResource("urn:liapoldus:plugin:v1:request-cookie-pairs", arrayDocument); err != nil {
		return result, err
	}
	result.pairSchema, err = compiler.Compile("urn:liapoldus:plugin:v1:request-cookie-pairs")
	if err != nil {
		return result, err
	}
	arrayMap, ok := arrayDocument.(map[string]any)
	if !ok || !readInt(arrayMap, "maxItems", &result.maxPairs) {
		return result, errors.New("invalid request-cookie contract limits")
	}
	result.maxHeaderBytes = result.maxPairs*(result.maxNameChars+1+result.maxValueChars) + 2*(result.maxPairs-1)
	return result, nil
}

func addContractResource(compiler *jsonschema.Compiler, path string) error {
	document, err := contractDocument(path)
	if err != nil {
		return err
	}
	data, ok := document.(map[string]any)
	if !ok {
		return errors.New("invalid embedded contract resource")
	}
	id, ok := data["$id"].(string)
	if !ok || id == "" {
		return errors.New("embedded contract resource has no identifier")
	}
	return compiler.AddResource(id, document)
}

func contractDocument(path string) (any, error) {
	raw, err := fs.ReadFile(contractAssets, path)
	if err != nil {
		return nil, err
	}
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return document, nil
}

func compileContractSchema(document any) (*jsonschema.Schema, error) {
	data, ok := document.(map[string]any)
	if !ok {
		return nil, errors.New("contract schema is not an object")
	}
	id, ok := data["$id"].(string)
	if !ok || id == "" {
		return nil, errors.New("contract schema has no identifier")
	}
	compiler := newContractCompiler()
	if err := compiler.AddResource(id, document); err != nil {
		return nil, err
	}
	return compiler.Compile(id)
}

func newContractCompiler() *jsonschema.Compiler {
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseRegexpEngine(func(pattern string) (jsonschema.Regexp, error) {
		compiled, err := regexp2.Compile(pattern, regexp2.ECMAScript)
		if err != nil {
			return nil, err
		}
		compiled.MatchTimeout = time.Second
		return regexp2SchemaRegexp{expression: compiled}, nil
	})
	return compiler
}

type regexp2SchemaRegexp struct{ expression *regexp2.Regexp }

func (r regexp2SchemaRegexp) String() string { return r.expression.String() }

func (r regexp2SchemaRegexp) MatchString(value string) bool {
	matched, err := r.expression.MatchString(value)
	return err == nil && matched
}

func validateContract(schema *jsonschema.Schema, raw []byte) error {
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil || schema.Validate(instance) != nil {
		return errors.New("contract validation failed")
	}
	return nil
}

func decodeStrict(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return errors.New("strict JSON decoding failed")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("strict JSON decoding failed")
	}
	return nil
}

// DecodeCookiePolicy validates a policy against the embedded protocol schema.
func DecodeCookiePolicy(data []byte) (CookiePolicy, error) {
	var policy CookiePolicy
	metadata, err := getCookieMetadata()
	if err != nil || validateContract(metadata.policySchema, data) != nil || decodeStrict(data, &policy) != nil {
		return CookiePolicy{}, ErrInvalidCookiePolicy
	}
	return policy, nil
}

// FilterCookiePairs validates the binding scope and all received cookie pairs,
// then returns only exact, case-sensitive allow-list matches in input order.
func FilterCookiePairs(policy CookiePolicy, instanceID, capability string, pairs []CookiePair) ([]CookiePair, error) {
	metadata, err := getCookieMetadata()
	if err != nil {
		return nil, ErrInvalidCookiePolicy
	}
	policyJSON, err := json.Marshal(policy)
	if err != nil || validateContract(metadata.policySchema, policyJSON) != nil {
		return nil, ErrInvalidCookiePolicy
	}
	if policy.InstanceID != instanceID || policy.Capability != capability {
		return nil, ErrCookiePolicyScope
	}
	if precheckCookiePairs(pairs, metadata) != nil {
		return nil, ErrInvalidCookieRequest
	}
	pairsJSON, err := json.Marshal(pairs)
	if err != nil || validateContract(metadata.pairSchema, pairsJSON) != nil {
		return nil, ErrInvalidCookieRequest
	}
	allowed := make(map[string]struct{}, len(policy.AllowedNames))
	for _, name := range policy.AllowedNames {
		allowed[name] = struct{}{}
	}
	filtered := make([]CookiePair, 0, len(pairs))
	for _, pair := range pairs {
		if _, ok := allowed[pair.Name]; ok {
			filtered = append(filtered, pair)
		}
	}
	return filtered, nil
}

// ParseCookieHeader strictly parses request Cookie header field values. It
// rejects malformed pairs rather than silently dropping them as net/http does.
func ParseCookieHeader(headerValues []string) ([]CookiePair, error) {
	metadata, err := getCookieMetadata()
	if err != nil {
		return nil, ErrInvalidCookieRequest
	}
	pairs := make([]CookiePair, 0)
	totalBytes := 0
	for _, header := range headerValues {
		totalBytes += len(header)
		if totalBytes > metadata.maxHeaderBytes {
			return nil, ErrInvalidCookieRequest
		}
		if hasControl(header) {
			return nil, ErrInvalidCookieRequest
		}
		if strings.Trim(header, " \t") == "" {
			continue
		}
		for _, part := range strings.Split(header, ";") {
			part = strings.Trim(part, " \t")
			separator := strings.IndexByte(part, '=')
			if separator <= 0 {
				return nil, ErrInvalidCookieRequest
			}
			name := strings.Trim(part[:separator], " \t")
			value := strings.Trim(part[separator+1:], " \t")
			if (&http.Cookie{Name: name, Value: value}).Valid() != nil {
				return nil, ErrInvalidCookieRequest
			}
			pairs = append(pairs, CookiePair{Name: name, Value: value})
			if len(pairs) > metadata.maxPairs {
				return nil, ErrInvalidCookieRequest
			}
		}
	}
	if precheckCookiePairs(pairs, metadata) != nil {
		return nil, ErrInvalidCookieRequest
	}
	pairsJSON, err := json.Marshal(pairs)
	if err != nil || validateContract(metadata.pairSchema, pairsJSON) != nil {
		return nil, ErrInvalidCookieRequest
	}
	return pairs, nil
}

func precheckCookiePairs(pairs []CookiePair, metadata cookieContractMetadata) error {
	if len(pairs) > metadata.maxPairs {
		return ErrInvalidCookieRequest
	}
	for _, pair := range pairs {
		if !utf8.ValidString(pair.Name) || !utf8.ValidString(pair.Value) || utf8.RuneCountInString(pair.Name) > metadata.maxNameChars || utf8.RuneCountInString(pair.Value) > metadata.maxValueChars {
			return ErrInvalidCookieRequest
		}
	}
	return nil
}

// DecodeHTTPResponseAction strictly validates the full response action and
// atomically serializes every cookie action. On any failure it returns a zero
// action and no headers, so callers can reject the complete plugin response.
func DecodeHTTPResponseAction(data []byte, requestHost string) (HTTPResponseAction, []string, error) {
	metadata, err := getCookieMetadata()
	if err != nil || validateContract(metadata.responseSchema, data) != nil {
		return HTTPResponseAction{}, nil, ErrInvalidHTTPResponseAction
	}
	var action HTTPResponseAction
	if decodeStrict(data, &action) != nil || len(action.Cookies) > metadata.maxActions {
		return HTTPResponseAction{}, nil, ErrInvalidHTTPResponseAction
	}
	serialized := make([]string, 0, len(action.Cookies))
	setBytes := 0
	for _, cookieAction := range action.Cookies {
		value, err := serializeCookieAction(cookieAction, requestHost, metadata)
		if err != nil || len(value) > metadata.maxCookieBytes {
			return HTTPResponseAction{}, nil, ErrInvalidHTTPResponseAction
		}
		setBytes += len(value)
		if setBytes > metadata.maxSetBytes {
			return HTTPResponseAction{}, nil, ErrInvalidHTTPResponseAction
		}
		serialized = append(serialized, value)
	}
	return action, serialized, nil
}

func serializeCookieAction(action CookieAction, requestHost string, metadata cookieContractMetadata) (string, error) {
	if action.SameSite != nil && *action.SameSite == metadata.sameSiteSecure && !action.Secure {
		return "", ErrInvalidHTTPResponseAction
	}
	if metadata.hostPrefix != "" && strings.HasPrefix(action.Name, metadata.hostPrefix) {
		if (metadata.hostSecure && !action.Secure) || action.Path == nil || *action.Path != metadata.hostPath || (!metadata.hostDomainOK && action.Domain != nil) {
			return "", ErrInvalidHTTPResponseAction
		}
	}
	for _, prefix := range metadata.securePrefixes {
		if strings.HasPrefix(action.Name, prefix) && !action.Secure {
			return "", ErrInvalidHTTPResponseAction
		}
	}
	var normalizedDomain string
	if action.Domain != nil {
		var err error
		normalizedDomain, err = validateCookieDomain(*action.Domain, requestHost, metadata)
		if err != nil {
			return "", ErrInvalidHTTPResponseAction
		}
	}
	cookie := &http.Cookie{Name: action.Name, Value: action.Value, Secure: action.Secure, HttpOnly: action.HttpOnly}
	if action.Path != nil {
		cookie.Path = *action.Path
	}
	if action.Domain != nil {
		cookie.Domain = normalizedDomain
	}
	if action.Expires != nil {
		cookie.Expires = *action.Expires
	}
	if action.MaxAge != nil {
		cookie.MaxAge = *action.MaxAge
	}
	if action.SameSite != nil {
		mode, ok := metadata.sameSiteValues[*action.SameSite]
		if !ok {
			return "", ErrInvalidHTTPResponseAction
		}
		cookie.SameSite = mode
	}
	if cookie.Valid() != nil {
		return "", ErrInvalidHTTPResponseAction
	}
	serialized := cookie.String()
	if serialized == "" {
		return "", ErrInvalidHTTPResponseAction
	}
	if action.MaxAge != nil && *action.MaxAge == 0 {
		serialized += "; Max-Age=0"
	}
	return serialized, nil
}

func validateCookieDomain(domain, requestHost string, metadata cookieContractMetadata) (string, error) {
	domain = strings.TrimPrefix(strings.TrimSuffix(domain, "."), ".")
	if domain == "" {
		return "", ErrInvalidHTTPResponseAction
	}
	canonicalDomain, err := idna.Lookup.ToASCII(strings.ToLower(domain))
	if err != nil || net.ParseIP(canonicalDomain) != nil {
		return "", ErrInvalidHTTPResponseAction
	}
	if metadata.rejectSuffix {
		suffix, _ := publicsuffix.PublicSuffix(canonicalDomain)
		if strings.EqualFold(suffix, canonicalDomain) {
			return "", ErrInvalidHTTPResponseAction
		}
	}
	if !metadata.matchHost {
		return canonicalDomain, nil
	}
	host := requestHost
	if parsed, _, splitErr := net.SplitHostPort(host); splitErr == nil {
		host = parsed
	} else if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.Trim(host, "[]")
	}
	if net.ParseIP(host) != nil {
		return "", ErrInvalidHTTPResponseAction
	}
	canonicalHost, err := idna.Lookup.ToASCII(strings.TrimSuffix(strings.ToLower(host), "."))
	if err != nil || (canonicalHost != canonicalDomain && !strings.HasSuffix(canonicalHost, "."+canonicalDomain)) {
		return "", ErrInvalidHTTPResponseAction
	}
	return canonicalDomain, nil
}

func requestCookieArraySchema(document any) ([]byte, error) {
	root, ok := document.(map[string]any)
	if !ok {
		return nil, errors.New("invalid stream-context contract")
	}
	branches, ok := root["oneOf"].([]any)
	if !ok {
		return nil, errors.New("invalid stream-context contract")
	}
	for _, branch := range branches {
		branchMap, ok := branch.(map[string]any)
		if !ok {
			continue
		}
		properties, ok := branchMap["properties"].(map[string]any)
		if !ok {
			continue
		}
		cookies, ok := properties["cookies"].(map[string]any)
		if ok {
			return json.Marshal(cookies)
		}
	}
	return nil, errors.New("stream-context contract has no cookie list schema")
}

func sameSiteModes(response map[string]any) (map[string]http.SameSite, error) {
	definitions, ok := response["$defs"].(map[string]any)
	if !ok {
		return nil, errors.New("response-action cookie schema is missing")
	}
	cookie, ok := definitions["cookieAction"].(map[string]any)
	if !ok {
		return nil, errors.New("response-action cookie schema is missing")
	}
	properties, ok := cookie["properties"].(map[string]any)
	if !ok {
		return nil, errors.New("response-action cookie schema is missing")
	}
	sameSite, ok := properties["sameSite"].(map[string]any)
	if !ok {
		return nil, errors.New("response-action cookie schema is missing")
	}
	enums, ok := sameSite["enum"].([]any)
	if !ok {
		return nil, errors.New("response-action cookie schema is missing")
	}
	modes := make(map[string]http.SameSite, len(enums))
	for _, item := range enums {
		name, ok := item.(string)
		if !ok {
			return nil, errors.New("response-action cookie schema is invalid")
		}
		switch name {
		case "Strict":
			modes[name] = http.SameSiteStrictMode
		case "Lax":
			modes[name] = http.SameSiteLaxMode
		case "None":
			modes[name] = http.SameSiteNoneMode
		default:
			return nil, errors.New("response-action cookie schema contains unsupported sameSite value")
		}
	}
	return modes, nil
}

func cookieFieldLimits(response map[string]any) (int, int, error) {
	definitions, ok := response["$defs"].(map[string]any)
	if !ok {
		return 0, 0, errors.New("response-action cookie schema is missing")
	}
	cookie, ok := definitions["cookieAction"].(map[string]any)
	if !ok {
		return 0, 0, errors.New("response-action cookie schema is missing")
	}
	properties, ok := cookie["properties"].(map[string]any)
	if !ok {
		return 0, 0, errors.New("response-action cookie schema is missing")
	}
	name, nameOK := properties["name"].(map[string]any)
	value, valueOK := properties["value"].(map[string]any)
	var maxName, maxValue int
	if !nameOK || !valueOK || !readInt(name, "maxLength", &maxName) || !readInt(value, "maxLength", &maxValue) {
		return 0, 0, errors.New("response-action cookie schema limits are invalid")
	}
	return maxName, maxValue, nil
}

func readInt(document map[string]any, key string, target *int) bool {
	value, ok := document[key].(json.Number)
	if !ok {
		return false
	}
	parsed, err := strconv.Atoi(value.String())
	if err != nil || parsed < 0 {
		return false
	}
	*target = parsed
	return true
}

func readStrings(value any) ([]string, bool) {
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		item, ok := value.(string)
		if !ok {
			return nil, false
		}
		result = append(result, item)
	}
	return result, true
}

func hasControl(value string) bool {
	for _, r := range value {
		if r == '\r' || r == '\n' || (r < 0x20 && r != '\t') || r == 0x7f {
			return true
		}
	}
	return false
}
