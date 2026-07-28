package logging

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/tidwall/gjson"
)

const (
	maxMetadataIdentifierLength = 128
	maxMetadataDiagnosticBytes  = 1 << 20
)

var safeMetadataIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]*$`)

func formatMetadataLog(_ string, method string, requestBody []byte, statusCode int, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage, requestID string, requestTimestamp, apiResponseTimestamp time.Time) string {
	completedAt := time.Now()
	if requestTimestamp.IsZero() {
		requestTimestamp = completedAt
	}

	var content strings.Builder
	content.WriteString("=== REQUEST METADATA ===\n")
	writeMetadataIdentifier(&content, "Request ID", requestID)
	writeMetadataField(&content, "Method", safeMetadataMethod(method))
	writeMetadataField(&content, "Status", fmt.Sprintf("%d", statusCode))
	writeMetadataField(&content, "Error Class", metadataErrorClass(statusCode, apiResponseErrors))
	writeMetadataField(&content, "Started At", requestTimestamp.Format(time.RFC3339Nano))
	writeMetadataField(&content, "Completed At", completedAt.Format(time.RFC3339Nano))
	writeMetadataField(&content, "Duration MS", fmt.Sprintf("%d", max(completedAt.Sub(requestTimestamp).Milliseconds(), 0)))
	if !apiResponseTimestamp.IsZero() {
		writeMetadataField(&content, "API Response At", apiResponseTimestamp.Format(time.RFC3339Nano))
	}
	writeMetadataSize(&content, "Request Body Bytes", requestBody)
	writeMetadataSize(&content, "Response Body Bytes", response)
	writeMetadataSize(&content, "Websocket Timeline Bytes", websocketTimeline)
	writeMetadataSize(&content, "API Request Bytes", apiRequest)
	writeMetadataSize(&content, "API Response Bytes", apiResponse)
	writeMetadataSize(&content, "API Websocket Timeline Bytes", apiWebsocketTimeline)
	writeMetadataTokens(&content, apiResponse, response)
	return content.String()
}

func writeMetadataField(content *strings.Builder, name, value string) {
	value = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(value, "\r", ""), "\n", ""))
	if value == "" {
		return
	}
	content.WriteString(name)
	content.WriteString(": ")
	content.WriteString(value)
	content.WriteByte('\n')
}

func writeMetadataIdentifier(content *strings.Builder, name, value string) {
	value = safeMetadataValue(value)
	if value != "" {
		writeMetadataField(content, name, value)
	}
}

func writeMetadataSize(content *strings.Builder, name string, payload []byte) {
	if len(payload) > 0 {
		writeMetadataField(content, name, fmt.Sprintf("%d", len(payload)))
	}
}

func safeMetadataValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > maxMetadataIdentifierLength || !safeMetadataIdentifier.MatchString(value) {
		return ""
	}
	return value
}

func safeMetadataMethod(method string) string {
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case "DELETE", "GET", "HEAD", "OPTIONS", "PATCH", "POST", "PUT":
		return method
	default:
		return ""
	}
}

func metadataErrorClass(statusCode int, errors []*interfaces.ErrorMessage) string {
	if len(errors) > 0 {
		for _, apiError := range errors {
			if apiError == nil || apiError.Error == nil {
				continue
			}
			typeName := reflect.TypeOf(apiError.Error).String()
			typeName = strings.TrimPrefix(typeName, "*")
			if typeName != "" {
				return typeName
			}
		}
		return "upstream_error"
	}
	switch {
	case statusCode >= 500:
		return "server_error"
	case statusCode >= 400:
		return "client_error"
	default:
		return "none"
	}
}

func writeMetadataTokens(content *strings.Builder, payloads ...[]byte) {
	paths := []struct {
		label string
		paths []string
	}{
		{label: "Input Tokens", paths: []string{"usage.input_tokens", "usage.prompt_tokens", "response.usage.input_tokens"}},
		{label: "Output Tokens", paths: []string{"usage.output_tokens", "usage.completion_tokens", "response.usage.output_tokens"}},
		{label: "Total Tokens", paths: []string{"usage.total_tokens", "response.usage.total_tokens"}},
	}
	for _, field := range paths {
		for _, payload := range payloads {
			if len(payload) == 0 || len(payload) > maxMetadataDiagnosticBytes {
				continue
			}
			for _, candidate := range metadataJSONCandidates(payload) {
				for _, path := range field.paths {
					value := gjson.GetBytes(candidate, path)
					if value.Exists() && value.Type == gjson.Number && value.Int() >= 0 {
						writeMetadataField(content, field.label, fmt.Sprintf("%d", value.Int()))
						goto nextField
					}
				}
			}
		}
	nextField:
	}
}

func metadataJSONCandidates(payload []byte) [][]byte {
	if gjson.ValidBytes(payload) {
		return [][]byte{payload}
	}
	var candidates [][]byte
	for _, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if line != "" && line != "[DONE]" && gjson.Valid(line) {
			candidates = append(candidates, []byte(line))
		}
	}
	return candidates
}
