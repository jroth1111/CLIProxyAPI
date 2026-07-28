package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	log "github.com/sirupsen/logrus"
)

var requestLogID atomic.Uint64

// LogRequest logs a complete non-streaming request/response cycle to a file.
//
// Parameters:
//   - url: The request URL
//   - method: The HTTP method
//   - requestHeaders: The request headers
//   - body: The request body
//   - statusCode: The response status code
//   - responseHeaders: The response headers
//   - response: The raw response data
//   - apiRequest: The API request data
//   - apiResponse: The API response data
//   - requestID: Optional request ID for log file naming
//   - requestTimestamp: When the request was received
//   - apiResponseTimestamp: When the API response was received
//
// Returns:
//   - error: An error if logging fails, nil otherwise
func (l *FileRequestLogger) LogRequest(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	return l.logRequest(url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline, apiResponseErrors, false, requestID, requestTimestamp, apiResponseTimestamp)
}

// LogRequestWithOptions logs a request with optional forced logging behavior.
// The force flag allows writing error logs even when regular request logging is disabled.
func (l *FileRequestLogger) LogRequestWithOptions(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage, force bool, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	return l.logRequestWithSources(url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, nil, apiRequest, nil, apiResponse, nil, apiWebsocketTimeline, nil, apiResponseErrors, force, requestID, requestTimestamp, apiResponseTimestamp)
}

func (l *FileRequestLogger) logRequest(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiResponseErrors []*interfaces.ErrorMessage, force bool, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	return l.logRequestWithSources(url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, nil, apiRequest, nil, apiResponse, nil, apiWebsocketTimeline, nil, apiResponseErrors, force, requestID, requestTimestamp, apiResponseTimestamp)
}

// LogRequestWithOptionsAndSources logs a request with optional file-backed large sections.
func (l *FileRequestLogger) LogRequestWithOptionsAndSources(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline []byte, websocketTimelineSource *FileBodySource, apiRequest, apiResponse, apiWebsocketTimeline []byte, apiWebsocketTimelineSource *FileBodySource, apiResponseErrors []*interfaces.ErrorMessage, force bool, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	return l.logRequestWithSources(url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, websocketTimelineSource, apiRequest, nil, apiResponse, nil, apiWebsocketTimeline, apiWebsocketTimelineSource, apiResponseErrors, force, requestID, requestTimestamp, apiResponseTimestamp)
}

// LogRequestWithOptionsAndAllSources logs a request with optional file-backed request and response sections.
func (l *FileRequestLogger) LogRequestWithOptionsAndAllSources(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline []byte, websocketTimelineSource *FileBodySource, apiRequest []byte, apiRequestSource *FileBodySource, apiResponse []byte, apiResponseSource *FileBodySource, apiWebsocketTimeline []byte, apiWebsocketTimelineSource *FileBodySource, apiResponseErrors []*interfaces.ErrorMessage, force bool, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	return l.logRequestWithSources(url, method, requestHeaders, body, statusCode, responseHeaders, response, websocketTimeline, websocketTimelineSource, apiRequest, apiRequestSource, apiResponse, apiResponseSource, apiWebsocketTimeline, apiWebsocketTimelineSource, apiResponseErrors, force, requestID, requestTimestamp, apiResponseTimestamp)
}

func (l *FileRequestLogger) logRequestWithSources(url, method string, requestHeaders map[string][]string, body []byte, statusCode int, responseHeaders map[string][]string, response, websocketTimeline []byte, websocketTimelineSource *FileBodySource, apiRequest []byte, apiRequestSource *FileBodySource, apiResponse []byte, apiResponseSource *FileBodySource, apiWebsocketTimeline []byte, apiWebsocketTimelineSource *FileBodySource, apiResponseErrors []*interfaces.ErrorMessage, force bool, requestID string, requestTimestamp, apiResponseTimestamp time.Time) error {
	defer cleanupFileBodySources(websocketTimelineSource, apiRequestSource, apiResponseSource, apiWebsocketTimelineSource)

	if !l.enabled && !force {
		return nil
	}

	if l.IsMetadataOnly() || (force && !l.enabled) {
		content := formatMetadataLog(url, method, body, statusCode, response, websocketTimeline, apiRequest, apiResponse, apiWebsocketTimeline, apiResponseErrors, requestID, requestTimestamp, apiResponseTimestamp)
		if l.homeEnabled && l.enabled {
			return l.forwardRequestLogToHome(context.Background(), nil, safeMetadataValue(requestID), content)
		}
		if errEnsure := l.ensureLogsDir(); errEnsure != nil {
			return fmt.Errorf("failed to create logs directory: %w", errEnsure)
		}
		filePath := filepath.Join(l.logsDir, l.generateMetadataFilename(force && !l.enabled, requestID))
		if errWrite := os.WriteFile(filePath, []byte(content), 0o600); errWrite != nil {
			return fmt.Errorf("failed to write metadata log file: %w", errWrite)
		}
		if errChmod := os.Chmod(filePath, 0o600); errChmod != nil {
			return fmt.Errorf("failed to secure metadata log file: %w", errChmod)
		}
		if errCleanup := l.cleanupOldMetadataLogs(); errCleanup != nil {
			log.WithError(errCleanup).Warn("failed to clean up old metadata logs")
		}
		return nil
	}

	if l.homeEnabled && l.enabled {
		responseToWrite, decompressErr := l.decompressResponse(responseHeaders, response)
		if decompressErr != nil {
			responseToWrite = response
		}

		var buf bytes.Buffer
		writeErr := l.writeNonStreamingLog(
			&buf,
			url,
			method,
			requestHeaders,
			body,
			"",
			websocketTimeline,
			websocketTimelineSource,
			apiRequest,
			apiRequestSource,
			apiResponse,
			apiResponseSource,
			apiWebsocketTimeline,
			apiWebsocketTimelineSource,
			apiResponseErrors,
			statusCode,
			responseHeaders,
			responseToWrite,
			decompressErr,
			requestTimestamp,
			apiResponseTimestamp,
		)
		if writeErr != nil {
			return fmt.Errorf("failed to build request log content: %w", writeErr)
		}
		return l.forwardRequestLogToHome(context.Background(), requestHeaders, requestID, buf.String())
	}

	// Ensure logs directory exists
	if errEnsure := l.ensureLogsDir(); errEnsure != nil {
		return fmt.Errorf("failed to create logs directory: %w", errEnsure)
	}

	// Generate filename with request ID
	filename := l.generateFilename(url, requestID)
	if force && !l.enabled {
		filename = l.generateErrorFilename(url, requestID)
	}
	filePath := filepath.Join(l.logsDir, filename)

	requestBodyPath, errTemp := l.writeRequestBodyTempFile(body)
	if errTemp != nil {
		log.WithError(errTemp).Warn("failed to create request body temp file, falling back to direct write")
	}
	if requestBodyPath != "" {
		defer func() {
			if errRemove := os.Remove(requestBodyPath); errRemove != nil {
				log.WithError(errRemove).Warn("failed to remove request body temp file")
			}
		}()
	}

	responseToWrite, decompressErr := l.decompressResponse(responseHeaders, response)
	if decompressErr != nil {
		// If decompression fails, continue with original response and annotate the log output.
		responseToWrite = response
	}

	logFile, errOpen := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if errOpen != nil {
		return fmt.Errorf("failed to create log file: %w", errOpen)
	}

	writeErr := l.writeNonStreamingLog(
		logFile,
		url,
		method,
		requestHeaders,
		body,
		requestBodyPath,
		websocketTimeline,
		websocketTimelineSource,
		apiRequest,
		apiRequestSource,
		apiResponse,
		apiResponseSource,
		apiWebsocketTimeline,
		apiWebsocketTimelineSource,
		apiResponseErrors,
		statusCode,
		responseHeaders,
		responseToWrite,
		decompressErr,
		requestTimestamp,
		apiResponseTimestamp,
	)
	if errClose := logFile.Close(); errClose != nil {
		log.WithError(errClose).Warn("failed to close request log file")
		if writeErr == nil {
			return errClose
		}
	}
	if writeErr != nil {
		return fmt.Errorf("failed to write log file: %w", writeErr)
	}

	if force && !l.enabled {
		if errCleanup := l.cleanupOldErrorLogs(); errCleanup != nil {
			log.WithError(errCleanup).Warn("failed to clean up old error logs")
		}
	}

	return nil
}

// LogStreamingRequest initiates logging for a streaming request.
//
// Parameters:
//   - url: The request URL
//   - method: The HTTP method
//   - headers: The request headers
//   - body: The request body
//   - requestID: Optional request ID for log file naming
//
// Returns:
//   - StreamingLogWriter: A writer for streaming response chunks
//   - error: An error if logging initialization fails, nil otherwise
func (l *FileRequestLogger) LogStreamingRequest(url, method string, headers map[string][]string, body []byte, requestID string) (StreamingLogWriter, error) {
	if !l.enabled {
		return &NoOpStreamingLogWriter{}, nil
	}

	if l.homeEnabled {
		client := currentHomeRequestLogClient()
		if client == nil || !client.HeartbeatOK() {
			return &NoOpStreamingLogWriter{}, nil
		}
		return newHomeStreamingLogWriter(url, method, headers, body, requestID), nil
	}

	// Ensure logs directory exists
	if err := l.ensureLogsDir(); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Generate filename with request ID
	filename := l.generateFilename(url, requestID)
	filePath := filepath.Join(l.logsDir, filename)

	requestHeaders := make(map[string][]string, len(headers))
	for key, values := range headers {
		headerValues := make([]string, len(values))
		copy(headerValues, values)
		requestHeaders[key] = headerValues
	}

	requestBodyPath, errTemp := l.writeRequestBodyTempFile(body)
	if errTemp != nil {
		return nil, fmt.Errorf("failed to create request body temp file: %w", errTemp)
	}

	responseBodyFile, errCreate := os.CreateTemp(l.logsDir, "response-body-*.tmp")
	if errCreate != nil {
		_ = os.Remove(requestBodyPath)
		return nil, fmt.Errorf("failed to create response body temp file: %w", errCreate)
	}
	responseBodyPath := responseBodyFile.Name()

	// Create streaming writer
	writer := &FileStreamingLogWriter{
		logFilePath:      filePath,
		url:              url,
		method:           method,
		timestamp:        time.Now(),
		requestHeaders:   requestHeaders,
		requestBodyPath:  requestBodyPath,
		responseBodyPath: responseBodyPath,
		responseBodyFile: responseBodyFile,
		chunkChan:        make(chan []byte, 100), // Buffered channel for async writes
		closeChan:        make(chan struct{}),
		errorChan:        make(chan error, 1),
	}

	// Start async writer goroutine
	go writer.asyncWriter()

	return writer, nil
}

// generateErrorFilename creates a filename with an error prefix to differentiate forced error logs.
func (l *FileRequestLogger) generateErrorFilename(url string, requestID ...string) string {
	return fmt.Sprintf("error-%s", l.generateFilename(url, requestID...))
}

func (l *FileRequestLogger) generateMetadataFilename(force bool, requestID string) string {
	prefix := "metadata-request"
	if force {
		prefix = "metadata-error"
	}
	idPart := safeMetadataValue(requestID)
	if idPart == "" {
		idPart = fmt.Sprintf("%d", requestLogID.Add(1))
	}
	return fmt.Sprintf("%s-%s-%s.log", prefix, time.Now().Format("2006-01-02T150405.000000000"), idPart)
}

// ensureLogsDir creates the logs directory if it doesn't exist.
//
// Returns:
//   - error: An error if directory creation fails, nil otherwise
func (l *FileRequestLogger) ensureLogsDir() error {
	if err := os.MkdirAll(l.logsDir, 0o700); err != nil {
		return err
	}
	return os.Chmod(l.logsDir, 0o700)
}

// generateFilename creates a sanitized filename from the URL path and current timestamp.
// Format: v1-responses-2025-12-23T195811-a1b2c3d4.log
//
// Parameters:
//   - url: The request URL
//   - requestID: Optional request ID to include in filename
//
// Returns:
//   - string: A sanitized filename for the log file
func (l *FileRequestLogger) generateFilename(url string, requestID ...string) string {
	// Extract path from URL
	path := url
	if strings.Contains(url, "?") {
		path = strings.Split(url, "?")[0]
	}

	// Remove leading slash
	if strings.HasPrefix(path, "/") {
		path = path[1:]
	}

	// Sanitize path for filename
	sanitized := l.sanitizeForFilename(path)

	// Add timestamp
	timestamp := time.Now().Format("2006-01-02T150405")

	// Use request ID if provided, otherwise use sequential ID
	var idPart string
	if len(requestID) > 0 && requestID[0] != "" {
		idPart = requestID[0]
	} else {
		id := requestLogID.Add(1)
		idPart = fmt.Sprintf("%d", id)
	}

	return fmt.Sprintf("%s-%s-%s.log", sanitized, timestamp, idPart)
}

// sanitizeForFilename replaces characters that are not safe for filenames.
//
// Parameters:
//   - path: The path to sanitize
//
// Returns:
//   - string: A sanitized filename
func (l *FileRequestLogger) sanitizeForFilename(path string) string {
	// Replace slashes with hyphens
	sanitized := strings.ReplaceAll(path, "/", "-")

	// Replace colons with hyphens
	sanitized = strings.ReplaceAll(sanitized, ":", "-")

	// Replace other problematic characters with hyphens
	reg := regexp.MustCompile(`[<>:"|?*\s]`)
	sanitized = reg.ReplaceAllString(sanitized, "-")

	// Remove multiple consecutive hyphens
	reg = regexp.MustCompile(`-+`)
	sanitized = reg.ReplaceAllString(sanitized, "-")

	// Remove leading/trailing hyphens
	sanitized = strings.Trim(sanitized, "-")

	// Handle empty result
	if sanitized == "" {
		sanitized = "root"
	}

	return sanitized
}

// cleanupOldErrorLogs keeps only the newest errorLogsMaxFiles forced error log files.
func (l *FileRequestLogger) cleanupOldMetadataLogs() error {
	maxFiles := l.metadataLogsMaxFiles
	if maxFiles < 1 {
		maxFiles = 100
	}
	return l.cleanupOldLogsByPrefix("metadata-", maxFiles)
}

func (l *FileRequestLogger) cleanupOldLogsByPrefix(prefix string, maxFiles int) error {
	if maxFiles <= 0 {
		return nil
	}
	entries, errRead := os.ReadDir(l.logsDir)
	if errRead != nil {
		return errRead
	}
	type logFile struct {
		name    string
		modTime time.Time
	}
	var files []logFile
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), prefix) || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		info, errInfo := entry.Info()
		if errInfo != nil {
			continue
		}
		files = append(files, logFile{name: entry.Name(), modTime: info.ModTime()})
	}
	if len(files) <= maxFiles {
		return nil
	}
	sort.Slice(files, func(i, j int) bool { return files[i].modTime.After(files[j].modTime) })
	for _, file := range files[maxFiles:] {
		if errRemove := os.Remove(filepath.Join(l.logsDir, file.name)); errRemove != nil {
			return errRemove
		}
	}
	return nil
}

func (l *FileRequestLogger) cleanupOldErrorLogs() error {
	if l.errorLogsMaxFiles <= 0 {
		return nil
	}

	entries, errRead := os.ReadDir(l.logsDir)
	if errRead != nil {
		return errRead
	}

	type logFile struct {
		name    string
		modTime time.Time
	}

	var files []logFile
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, "error-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		info, errInfo := entry.Info()
		if errInfo != nil {
			log.WithError(errInfo).Warn("failed to read error log info")
			continue
		}
		files = append(files, logFile{name: name, modTime: info.ModTime()})
	}

	if len(files) <= l.errorLogsMaxFiles {
		return nil
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].modTime.After(files[j].modTime)
	})

	for _, file := range files[l.errorLogsMaxFiles:] {
		if errRemove := os.Remove(filepath.Join(l.logsDir, file.name)); errRemove != nil {
			log.WithError(errRemove).Warnf("failed to remove old error log: %s", file.name)
		}
	}

	return nil
}

func (l *FileRequestLogger) writeRequestBodyTempFile(body []byte) (string, error) {
	tmpFile, errCreate := os.CreateTemp(l.logsDir, "request-body-*.tmp")
	if errCreate != nil {
		return "", errCreate
	}
	tmpPath := tmpFile.Name()

	if _, errCopy := io.Copy(tmpFile, bytes.NewReader(body)); errCopy != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return "", errCopy
	}
	if errClose := tmpFile.Close(); errClose != nil {
		_ = os.Remove(tmpPath)
		return "", errClose
	}
	return tmpPath, nil
}
