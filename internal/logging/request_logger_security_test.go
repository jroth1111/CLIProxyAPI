package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
)

const sentinelSecret = "SENTINEL_DO_NOT_PERSIST_7f3d9a"

func TestMetadataOnlyRequestLogNeverPersistsPayloadsOrSecrets(t *testing.T) {
	logsDir := filepath.Join(t.TempDir(), "logs")
	logger := NewFileRequestLogger(false, logsDir, "", 10)
	logger.SetMetadataOnly(true)

	secret := []byte(sentinelSecret)
	headers := map[string][]string{
		"Authorization": {"Bearer " + sentinelSecret},
		"X-Api-Key":     {sentinelSecret},
		"X-System":      {sentinelSecret},
	}
	errLog := logger.LogRequestWithOptions(
		"/v1/responses?api_key="+sentinelSecret,
		http.MethodPost,
		headers,
		[]byte(`{"model":"safe-model","prompt":"`+sentinelSecret+`","system":"`+sentinelSecret+`","messages":[{"content":"`+sentinelSecret+`"}],"tools":[{"description":"`+sentinelSecret+`"}],"attachment":"`+sentinelSecret+`"}`),
		http.StatusBadGateway,
		map[string][]string{"Set-Cookie": {sentinelSecret}},
		secret,
		secret,
		secret,
		[]byte(`{"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10},"secret":"`+sentinelSecret+`"}`),
		secret,
		[]*interfaces.ErrorMessage{{StatusCode: http.StatusBadGateway, Error: errors.New(sentinelSecret)}},
		true,
		"request-safe-123",
		time.Now().Add(-25*time.Millisecond),
		time.Now().Add(-5*time.Millisecond),
	)
	if errLog != nil {
		t.Fatalf("LogRequestWithOptions: %v", errLog)
	}

	entries, errReadDir := os.ReadDir(logsDir)
	if errReadDir != nil {
		t.Fatalf("ReadDir: %v", errReadDir)
	}
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "metadata-error-") {
		t.Fatalf("metadata log entries = %#v", entries)
	}
	content, errRead := os.ReadFile(filepath.Join(logsDir, entries[0].Name()))
	if errRead != nil {
		t.Fatalf("ReadFile: %v", errRead)
	}
	if bytes.Contains(content, []byte(sentinelSecret)) {
		t.Fatalf("metadata log persisted sentinel secret: %s", content)
	}
	for _, forbidden := range []string{"Authorization", "X-Api-Key", "prompt", "system", "messages", "tools", "attachment", "REQUEST BODY", "API REQUEST", "API RESPONSE", "Set-Cookie", "SHA256"} {
		if bytes.Contains(content, []byte(forbidden)) {
			t.Fatalf("metadata log contains forbidden field %q: %s", forbidden, content)
		}
	}
	for _, required := range []string{"Request ID: request-safe-123", "Model: safe-model", "Method: POST", "Route: /v1/responses", "Status: 502", "Error Class:", "Duration MS:", "Input Tokens: 7", "Output Tokens: 3", "Total Tokens: 10", "Request Body Bytes:", "Response Body Bytes:"} {
		if !bytes.Contains(content, []byte(required)) {
			t.Fatalf("metadata log missing %q: %s", required, content)
		}
	}
	if info, errStat := os.Stat(logsDir); errStat != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("logs dir mode = %v err=%v, want 0700", info.Mode().Perm(), errStat)
	}
	logInfo, errStat := os.Stat(filepath.Join(logsDir, entries[0].Name()))
	if errStat != nil || logInfo.Mode().Perm() != 0o600 {
		t.Fatalf("metadata log mode = %v err=%v, want 0600", logInfo.Mode().Perm(), errStat)
	}
	if _, errSource := logger.NewFileBodySource(sentinelSecret); errSource == nil {
		t.Fatal("metadata-only logger allowed payload-backed temp source")
	}
}

func TestMetadataOnlyForwardingOmitsHeadersAndPayloads(t *testing.T) {
	original := currentHomeRequestLogClient
	defer func() { currentHomeRequestLogClient = original }()
	stub := &stubHomeRequestLogClient{heartbeatOK: true}
	currentHomeRequestLogClient = func() homeRequestLogClient { return stub }

	logger := NewFileRequestLogger(true, filepath.Join(t.TempDir(), "logs"), "", 10)
	logger.SetMetadataOnly(true)
	logger.SetHomeEnabled(true)
	if errLog := logger.LogRequest(
		"/v1/responses?key="+sentinelSecret,
		http.MethodPost,
		map[string][]string{"Authorization": {"Bearer " + sentinelSecret}},
		[]byte(`{"model":"safe-model","input":"`+sentinelSecret+`"}`),
		http.StatusOK,
		map[string][]string{"X-Secret": {sentinelSecret}},
		[]byte(sentinelSecret), nil, nil, nil, nil, nil,
		"forward-safe-123", time.Now(), time.Now(),
	); errLog != nil {
		t.Fatalf("LogRequest: %v", errLog)
	}
	if len(stub.pushed) != 1 {
		t.Fatalf("forwarded payloads = %d, want 1", len(stub.pushed))
	}
	if bytes.Contains(stub.pushed[0], []byte(sentinelSecret)) {
		t.Fatalf("forwarded metadata persisted sentinel: %s", stub.pushed[0])
	}
	var envelope homeRequestLogPayload
	if errUnmarshal := json.Unmarshal(stub.pushed[0], &envelope); errUnmarshal != nil {
		t.Fatalf("Unmarshal: %v", errUnmarshal)
	}
	if len(envelope.Headers) != 0 {
		t.Fatalf("forwarded headers = %#v, want none", envelope.Headers)
	}
	if !strings.Contains(envelope.RequestLog, "Model: safe-model") || !strings.Contains(envelope.RequestLog, "Status: 200") {
		t.Fatalf("forwarded safe metadata missing: %s", envelope.RequestLog)
	}
}

func TestMetadataRetentionDoesNotTouchHistoricalLogs(t *testing.T) {
	logsDir := t.TempDir()
	historical := filepath.Join(logsDir, "error-historical.log")
	if errWrite := os.WriteFile(historical, []byte("historical"), 0o600); errWrite != nil {
		t.Fatalf("write historical log: %v", errWrite)
	}
	logger := NewFileRequestLogger(false, logsDir, "", 10)
	logger.SetMetadataOnly(true)
	logger.SetMetadataLogsMaxFiles(1)
	for i := 0; i < 2; i++ {
		if errLog := logger.LogRequestWithOptions("/v1/responses", http.MethodPost, nil, nil, http.StatusBadRequest, nil, nil, nil, nil, nil, nil, nil, true, "retention-"+string(rune('a'+i)), time.Now(), time.Time{}); errLog != nil {
			t.Fatalf("metadata log %d: %v", i, errLog)
		}
		time.Sleep(time.Millisecond)
	}
	if content, errRead := os.ReadFile(historical); errRead != nil || string(content) != "historical" {
		t.Fatalf("historical log changed: content=%q err=%v", content, errRead)
	}
	entries, errReadDir := os.ReadDir(logsDir)
	if errReadDir != nil {
		t.Fatalf("ReadDir: %v", errReadDir)
	}
	metadataCount := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "metadata-") {
			metadataCount++
		}
	}
	if metadataCount != 1 {
		t.Fatalf("metadata log count = %d, want 1", metadataCount)
	}
}

func TestFileBodySourcePermissions(t *testing.T) {
	baseDir := filepath.Join(t.TempDir(), "logs")
	source, errSource := NewFileBodySourceInDir(baseDir, "permissions")
	if errSource != nil {
		t.Fatalf("NewFileBodySourceInDir: %v", errSource)
	}
	defer source.Cleanup()
	file, errPart := source.CreatePart("payload")
	if errPart != nil {
		t.Fatalf("CreatePart: %v", errPart)
	}
	path := file.Name()
	if errClose := file.Close(); errClose != nil {
		t.Fatalf("Close: %v", errClose)
	}
	baseInfo, _ := os.Stat(baseDir)
	partDirInfo, _ := os.Stat(filepath.Dir(path))
	partInfo, _ := os.Stat(path)
	if baseInfo.Mode().Perm() != 0o700 || partDirInfo.Mode().Perm() != 0o700 || partInfo.Mode().Perm() != 0o600 {
		t.Fatalf("modes base=%o partDir=%o part=%o", baseInfo.Mode().Perm(), partDirInfo.Mode().Perm(), partInfo.Mode().Perm())
	}
}
