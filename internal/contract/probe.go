package contract

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const DefaultProbeResponseLimit int64 = 4 << 20

type ProbeTarget struct {
	Name    string
	BaseURL string
}

type ProbeOutcome string

const (
	ProbeSuccess          ProbeOutcome = "success"
	ProbeApplicationError ProbeOutcome = "application-error"
	ProbeValidationError  ProbeOutcome = "validation-error"
	ProbeInvalidResponse  ProbeOutcome = "invalid-response"
	ProbeInvalidError     ProbeOutcome = "invalid-error-response"
	ProbeUnexpectedHTTP   ProbeOutcome = "unexpected-http-response"
	ProbeTransportError   ProbeOutcome = "transport-error"
	ProbeResponseTooLarge ProbeOutcome = "response-too-large"
)

type ProbeResult struct {
	Target          string       `json:"target"`
	RouteID         string       `json:"routeId"`
	Method          string       `json:"method"`
	Path            string       `json:"path"`
	Status          int          `json:"status"`
	Outcome         ProbeOutcome `json:"outcome"`
	ApplicationCode string       `json:"applicationCode,omitempty"`
	ErrorKind       string       `json:"errorKind,omitempty"`
	ContentType     string       `json:"contentType,omitempty"`
	BodyBytes       int          `json:"bodyBytes,omitempty"`
	BodySHA256      string       `json:"bodySha256,omitempty"`
	SemanticSHA256  string       `json:"semanticSha256,omitempty"`
	DurationMillis  int64        `json:"durationMillis"`
	Error           string       `json:"error,omitempty"`
}

func (r ProbeResult) ValidContractResponse() bool {
	return r.Outcome == ProbeSuccess || r.Outcome == ProbeApplicationError
}

type Prober struct {
	Client           *http.Client
	BearerToken      string
	MaxResponseBytes int64
}

func (p Prober) Run(ctx context.Context, target ProbeTarget, routes []RouteContract) []ProbeResult {
	results := make([]ProbeResult, 0, len(routes))
	for _, route := range routes {
		results = append(results, p.Probe(ctx, target, route))
	}
	return results
}

func (p Prober) Probe(ctx context.Context, target ProbeTarget, route RouteContract) (result ProbeResult) {
	result = ProbeResult{
		Target:  target.Name,
		RouteID: route.ID,
		Method:  route.Method,
		Path:    route.Path,
	}
	started := time.Now()
	defer func() {
		result.DurationMillis = time.Since(started).Milliseconds()
	}()

	endpoint, err := resolveProbeURL(target.BaseURL, route.Path)
	if err != nil {
		result.Outcome = ProbeTransportError
		result.Error = err.Error()
		return result
	}

	var body io.Reader
	if len(route.ValidRequest) != 0 {
		body = bytes.NewReader(route.ValidRequest)
	}
	request, err := http.NewRequestWithContext(ctx, route.Method, endpoint, body)
	if err != nil {
		result.Outcome = ProbeTransportError
		result.Error = err.Error()
		return result
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Accept-Encoding", "identity")
	request.Header.Set("Authorization", "Bearer "+p.BearerToken)
	request.Header.Set("User-Agent", "remnanode-contract-probe/0.1.0")
	if len(route.ValidRequest) != 0 {
		request.Header.Set("Content-Type", "application/json")
	}

	client := p.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(request)
	if err != nil {
		result.Outcome = ProbeTransportError
		result.Error = err.Error()
		return result
	}
	defer response.Body.Close()
	result.Status = response.StatusCode
	result.ContentType = response.Header.Get("Content-Type")

	limit := p.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultProbeResponseLimit
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		result.Outcome = ProbeTransportError
		result.Error = fmt.Sprintf("read response: %v", err)
		return result
	}
	result.BodyBytes = len(raw)
	digest := sha256.Sum256(raw)
	result.BodySHA256 = hex.EncodeToString(digest[:])
	if int64(len(raw)) > limit {
		result.Outcome = ProbeResponseTooLarge
		result.Error = fmt.Sprintf("response exceeds %d bytes", limit)
		return result
	}
	mediaType, _, contentTypeErr := mime.ParseMediaType(result.ContentType)
	if contentTypeErr != nil || mediaType != "application/json" {
		if response.StatusCode == route.SuccessStatus {
			result.Outcome = ProbeInvalidResponse
		} else {
			result.Outcome = ProbeInvalidError
		}
		result.Error = fmt.Sprintf("Content-Type %q is not application/json", result.ContentType)
		return result
	}

	switch response.StatusCode {
	case route.SuccessStatus:
		if err := route.Response.ValidateJSON(raw); err != nil {
			result.Outcome = ProbeInvalidResponse
			result.Error = err.Error()
			return result
		}
		semanticHash, err := successSemanticHash(route.ID, raw)
		if err != nil {
			result.Outcome = ProbeInvalidResponse
			result.Error = "project response semantics: " + err.Error()
			return result
		}
		result.SemanticSHA256 = semanticHash
		result.Outcome = ProbeSuccess
	case OfficialErrors.ValidationStatus:
		if err := OfficialErrors.ValidationResponse.ValidateJSON(raw); err != nil {
			result.Outcome = ProbeInvalidError
			result.Error = err.Error()
			return result
		}
		result.Outcome = ProbeValidationError
	default:
		if applicationErr := OfficialErrors.ApplicationResponse.ValidateJSON(raw); applicationErr != nil {
			if genericErr := OfficialErrors.GenericHTTPResponse.ValidateJSON(raw); genericErr != nil {
				result.Outcome = ProbeUnexpectedHTTP
				result.Error = fmt.Sprintf("application error: %v; generic error: %v", applicationErr, genericErr)
				return result
			}
			result.ErrorKind = "generic"
			semanticHash, err := errorSemanticHash(result.ErrorKind, raw)
			if err != nil {
				result.Outcome = ProbeInvalidError
				result.Error = "project error semantics: " + err.Error()
				return result
			}
			result.SemanticSHA256 = semanticHash
			result.Outcome = ProbeApplicationError
			return result
		}
		result.Outcome = ProbeApplicationError
		result.ErrorKind = "application"
		var application struct {
			ErrorCode string `json:"errorCode"`
		}
		if json.Unmarshal(raw, &application) == nil {
			result.ApplicationCode = application.ErrorCode
		}
		semanticHash, err := errorSemanticHash(result.ErrorKind, raw)
		if err != nil {
			result.Outcome = ProbeInvalidError
			result.Error = "project error semantics: " + err.Error()
			return result
		}
		result.SemanticSHA256 = semanticHash
	}
	return result
}

type ProbeDifference struct {
	RouteID string `json:"routeId"`
	Problem string `json:"problem"`
}

// CompareProbeResults compares semantic behavior and ignores dynamic values,
// response hashes, sizes, and timing.
func CompareProbeResults(baseline, candidate []ProbeResult) []ProbeDifference {
	baselineByRoute := indexProbeResults(baseline)
	candidateByRoute := indexProbeResults(candidate)
	routeIDs := make(map[string]struct{}, len(baselineByRoute)+len(candidateByRoute))
	for routeID := range baselineByRoute {
		routeIDs[routeID] = struct{}{}
	}
	for routeID := range candidateByRoute {
		routeIDs[routeID] = struct{}{}
	}
	ordered := make([]string, 0, len(routeIDs))
	for routeID := range routeIDs {
		ordered = append(ordered, routeID)
	}
	sort.Strings(ordered)

	differences := make([]ProbeDifference, 0)
	for _, routeID := range ordered {
		left, leftOK := baselineByRoute[routeID]
		right, rightOK := candidateByRoute[routeID]
		if !leftOK || !rightOK {
			differences = append(differences, ProbeDifference{RouteID: routeID, Problem: "route is missing from one target"})
			continue
		}
		if !left.ValidContractResponse() {
			differences = append(differences, ProbeDifference{RouteID: routeID, Problem: "baseline returned " + string(left.Outcome)})
			continue
		}
		if !right.ValidContractResponse() {
			differences = append(differences, ProbeDifference{RouteID: routeID, Problem: "candidate returned " + string(right.Outcome)})
			continue
		}
		if left.Status != right.Status || left.Outcome != right.Outcome ||
			left.ApplicationCode != right.ApplicationCode || left.ErrorKind != right.ErrorKind ||
			left.SemanticSHA256 != right.SemanticSHA256 {
			differences = append(differences, ProbeDifference{
				RouteID: routeID,
				Problem: fmt.Sprintf(
					"semantic mismatch: baseline=%d/%s/%s/%s/%s candidate=%d/%s/%s/%s/%s",
					left.Status, left.Outcome, left.ErrorKind, left.ApplicationCode, left.SemanticSHA256,
					right.Status, right.Outcome, right.ErrorKind, right.ApplicationCode, right.SemanticSHA256,
				),
			})
		}
	}
	return differences
}

func resolveProbeURL(baseURL, routePath string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse target URL: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return "", fmt.Errorf("target URL scheme must be http or https")
	}
	if base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return "", fmt.Errorf("target URL must contain only scheme and host")
	}
	if strings.Trim(base.Path, "/") != "" {
		return "", fmt.Errorf("target URL must not contain a path")
	}
	base.Path = routePath
	return base.String(), nil
}

func indexProbeResults(results []ProbeResult) map[string]ProbeResult {
	indexed := make(map[string]ProbeResult, len(results))
	for _, result := range results {
		indexed[result.RouteID] = result
	}
	return indexed
}
