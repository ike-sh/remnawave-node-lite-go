package httpserver

import (
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"

	"github.com/Luxiaba/remnawave-node-lite-go/internal/bodylimit"
	"github.com/Luxiaba/remnawave-node-lite-go/internal/nodeapi"
)

func decodeNodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	if !requestHasBody(r) {
		writeJSON(w, http.StatusBadRequest, nodeapi.NewValidationError(nodeapi.MissingIssue(nil, "object")))
		return false
	}
	parseJSON, supportedCharset := prepareNodeJSONBody(w, r)
	if !supportedCharset {
		writeUnsupportedCharset(w, r)
		return false
	}
	if !parseJSON {
		writeJSON(w, http.StatusBadRequest, nodeapi.NewValidationError(nodeapi.MissingIssue(nil, "object")))
		return false
	}
	validation := nodeapi.DecodeJSON(r.Body, target)
	if validation == nil {
		return true
	}
	writeNodeValidationError(w, validation)
	return false
}

func validateNodeJSONDocument(w http.ResponseWriter, r *http.Request) bool {
	if !requestHasBody(r) {
		return true
	}
	parseJSON, supportedCharset := prepareNodeJSONBody(w, r)
	if !supportedCharset {
		writeUnsupportedCharset(w, r)
		return false
	}
	if !parseJSON {
		return true
	}
	validation := nodeapi.ValidateJSONDocument(r.Body)
	if validation == nil {
		return true
	}
	writeNodeValidationError(w, validation)
	return false
}

func prepareNodeJSONBody(w http.ResponseWriter, r *http.Request) (parseJSON, supportedCharset bool) {
	mediaType, parameters, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return false, true
	}
	charset := strings.ToLower(strings.TrimSpace(parameters["charset"]))
	decoder, ok := decoderForJSONCharset(charset)
	if !ok {
		return true, false
	}
	if r.Body != nil && r.Body != http.NoBody {
		transcoded := &transformReadCloser{
			Reader: transform.NewReader(r.Body, decoder),
			Closer: r.Body,
		}
		r.Body = http.MaxBytesReader(w, transcoded, bodylimit.MaxBytesLimit())
		r.ContentLength = -1
	}
	return true, true
}

func decoderForJSONCharset(charset string) (*encoding.Decoder, bool) {
	var selected encoding.Encoding
	switch charset {
	case "", "utf-8":
		selected = unicode.UTF8BOM
	case "utf-16", "utf-16le":
		selected = unicode.UTF16(unicode.LittleEndian, unicode.UseBOM)
	case "utf-16be":
		selected = unicode.UTF16(unicode.BigEndian, unicode.UseBOM)
	default:
		return nil, false
	}
	return selected.NewDecoder(), true
}

type transformReadCloser struct {
	io.Reader
	io.Closer
}

func requestHasBody(r *http.Request) bool {
	return len(r.TransferEncoding) > 0 ||
		(r.ContentLength != 0 && r.Body != nil && r.Body != http.NoBody)
}

func writeUnsupportedCharset(w http.ResponseWriter, r *http.Request) {
	_, parameters, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	writeJSON(w, http.StatusUnsupportedMediaType, struct {
		StatusCode int    `json:"statusCode"`
		Message    string `json:"message"`
		Error      string `json:"error"`
	}{
		StatusCode: http.StatusUnsupportedMediaType,
		Message:    fmt.Sprintf("unsupported charset %q", strings.ToUpper(parameters["charset"])),
		Error:      "Unsupported Media Type",
	})
}

func writeNodeValidationError(w http.ResponseWriter, validation *nodeapi.ValidationError) {
	if validation.StatusCode == http.StatusRequestEntityTooLarge {
		writeJSON(w, http.StatusRequestEntityTooLarge, struct {
			Message    string `json:"message"`
			Error      string `json:"error"`
			StatusCode int    `json:"statusCode"`
		}{
			Message:    "request entity too large",
			Error:      "Payload Too Large",
			StatusCode: http.StatusRequestEntityTooLarge,
		})
		return
	}
	writeJSON(w, validation.StatusCode, validation)
}

func writeNodeResult(w http.ResponseWriter, r *http.Request, response any, err error) {
	if err == nil {
		writeNodeResponse(w, response)
		return
	}

	serviceError, ok := nodeapi.AsServiceError(err)
	if !ok {
		slog.Error("unclassified Node API error", "error", err, "path", r.URL.Path)
		serviceError = nodeapi.ServiceError{
			Status:  http.StatusInternalServerError,
			Code:    "E000",
			Message: "Internal server error",
		}
	}
	writeJSON(w, serviceError.Status, nodeapi.NewApplicationError(r.URL.RequestURI(), serviceError))
}

func writeNodeResponse(w http.ResponseWriter, response any) {
	writeJSON(w, http.StatusOK, envelope[any]{Response: response})
}
