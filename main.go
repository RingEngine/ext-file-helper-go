package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

const (
	version        = "0.9.4"
	maxMessageSize = 1024 * 1024
	chunkSize      = maxMessageSize / 2
)

type Request struct {
	ID     string                 `json:"id"`
	Action string                 `json:"action"`
	Data   map[string]interface{} `json:"data"`
}

type WireResponse struct {
	ID     string      `json:"id"`
	Code   int         `json:"code"`
	Data   interface{} `json:"data"`
	NextID string      `json:"next_id,omitempty"`
	Encode string      `json:"encode,omitempty"`
}

func main() {
	tempFiles := map[string]struct{}{}
	for {
		requestBytes, err := readNativeMessage(os.Stdin)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			writeNativeResponse(WireResponse{
				ID:   "",
				Code: 500,
				Data: map[string]interface{}{
					"message": "IOError " + err.Error(),
					"trace":   string(debug.Stack()),
				},
			})
			return
		}

		var request Request
		if err := json.Unmarshal(requestBytes, &request); err != nil {
			writeNativeResponse(WireResponse{
				ID:   "",
				Code: 400,
				Data: "invalid request JSON: " + err.Error(),
			})
			continue
		}

		response, err := handleRequest(request, tempFiles)
		if err != nil {
			writeNativeResponse(WireResponse{
				ID:   request.ID,
				Code: 500,
				Data: map[string]interface{}{
					"message": fmt.Sprintf("%T %v", err, err),
					"trace":   string(debug.Stack()),
				},
			})
			continue
		}

		writeNativeResponse(response)
	}
}

func handleRequest(request Request, tempFiles map[string]struct{}) (WireResponse, error) {
	action := request.Action
	data := request.Data
	switch action {
	case "version":
		return ok(request.ID, version), nil
	case "constants":
		return ok(request.ID, map[string]interface{}{
			"version":   version,
			"separator": string(os.PathSeparator),
		}), nil
	case "showDirectoryPicker":
		path, cancelled, err := showDirectoryPicker(data)
		if err != nil {
			return WireResponse{}, err
		}
		if cancelled {
			return ok(request.ID, nil), nil
		}
		return ok(request.ID, path), nil
	case "showOpenFilePicker":
		paths, cancelled, err := showOpenFilePicker(data)
		if err != nil {
			return WireResponse{}, err
		}
		if cancelled {
			return ok(request.ID, nil), nil
		}
		return ok(request.ID, paths), nil
	case "showSaveFilePicker":
		path, cancelled, err := showSaveFilePicker(data)
		if err != nil {
			return WireResponse{}, err
		}
		if cancelled {
			return ok(request.ID, nil), nil
		}
		return ok(request.ID, path), nil
	case "scandir":
		path := mustString(data["path"])
		entries, err := os.ReadDir(path)
		if err != nil {
			return WireResponse{}, err
		}
		if mustBool(data["kind"]) {
			result := make([][]interface{}, 0, len(entries))
			for _, entry := range entries {
				itemPath := filepath.Join(path, entry.Name())
				kind := 0
				if entry.Type().IsRegular() {
					kind = 1
				} else if entry.IsDir() {
					kind = 2
				}
				result = append(result, []interface{}{entry.Name(), kind})
				_ = itemPath
			}
			return ok(request.ID, result), nil
		}
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		return ok(request.ID, names), nil
	case "getKind":
		path := mustString(data["path"])
		if isFile(path) {
			return ok(request.ID, "file"), nil
		}
		if isDir(path) {
			return ok(request.ID, "directory"), nil
		}
		return ok(request.ID, nil), nil
	case "isfile":
		return ok(request.ID, isFile(mustString(data["path"]))), nil
	case "isdir":
		return ok(request.ID, isDir(mustString(data["path"]))), nil
	case "exists":
		_, err := os.Stat(mustString(data["path"]))
		return ok(request.ID, err == nil), nil
	case "abspath":
		path := mustString(data["path"])
		if mustBool(data["startIn"]) {
			return ok(request.ID, parseWellKnownDirectory(path, true)), nil
		}
		if mustBool(data["expand"]) {
			path = expandPath(path)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return WireResponse{}, err
		}
		return ok(request.ID, abs), nil
	case "stat":
		path := mustString(data["path"])
		info, err := os.Stat(path)
		if err != nil {
			return WireResponse{}, err
		}
		result := map[string]interface{}{
			"mtime": float64(info.ModTime().UnixNano()) / 1e9,
			"size":  info.Size(),
		}
		if info.Mode().IsRegular() {
			if fileType := mime.TypeByExtension(filepath.Ext(path)); fileType != "" {
				result["type"] = fileType
			}
		}
		return ok(request.ID, result), nil
	case "read":
		return handleRead(request.ID, data)
	case "write":
		return handleWrite(request.ID, data)
	case "truncate":
		return handleTruncate(request.ID, data)
	case "mktemp":
		tmp, err := os.CreateTemp("", "fsa")
		if err != nil {
			return WireResponse{}, err
		}
		tmpPath := tmp.Name()
		tmp.Close()
		if source := mustString(data["path"]); source != "" {
			bytes, err := os.ReadFile(source)
			if err != nil {
				os.Remove(tmpPath)
				return WireResponse{}, err
			}
			if err := os.WriteFile(tmpPath, bytes, 0o600); err != nil {
				os.Remove(tmpPath)
				return WireResponse{}, err
			}
		}
		tempFiles[tmpPath] = struct{}{}
		return ok(request.ID, tmpPath), nil
	case "mkdir":
		path := mustString(data["path"])
		if !isDir(path) {
			if err := os.MkdirAll(path, 0o755); err != nil {
				return WireResponse{}, err
			}
		}
		return ok(request.ID, nil), nil
	case "touch":
		path := mustString(data["path"])
		if !isFile(path) {
			file, err := os.OpenFile(path, os.O_CREATE, 0o644)
			if err != nil {
				return WireResponse{}, err
			}
			file.Close()
		}
		return ok(request.ID, nil), nil
	case "rm":
		path := mustString(data["path"])
		if isDir(path) {
			if mustBool(data["recursive"]) {
				if err := os.RemoveAll(path); err != nil {
					return WireResponse{}, err
				}
			} else if err := os.Remove(path); err != nil {
				return WireResponse{}, err
			}
		} else if err := os.Remove(path); err != nil {
			return WireResponse{}, err
		}
		delete(tempFiles, path)
		return ok(request.ID, nil), nil
	case "mv":
		src := mustString(data["src"])
		dst := mustString(data["dst"])
		if mustBool(data["overwrite"]) && pathExists(dst) {
			if err := removePath(dst); err != nil {
				return WireResponse{}, err
			}
		}
		if err := movePath(src, dst); err != nil {
			return WireResponse{}, err
		}
		delete(tempFiles, src)
		return ok(request.ID, nil), nil
	case "echo":
		return ok(request.ID, data), nil
	default:
		return WireResponse{ID: request.ID, Code: 400, Data: "Not implemented"}, nil
	}
}

func handleRead(id string, data map[string]interface{}) (WireResponse, error) {
	path := mustString(data["path"])
	mode := mustStringOrDefault(data["mode"], "rb")
	file, err := openForRead(path, mode)
	if err != nil {
		return WireResponse{}, err
	}
	defer file.Close()

	if offset, ok := int64Value(data["offset"]); ok && offset != 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return WireResponse{}, err
		}
	}

	size, hasSize := intValue(data["size"])
	var bytes []byte
	if hasSize && size >= 0 {
		bytes = make([]byte, size)
		n, err := io.ReadFull(file, bytes)
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
			return WireResponse{}, err
		}
		bytes = bytes[:n]
	} else {
		bytes, err = io.ReadAll(file)
		if err != nil {
			return WireResponse{}, err
		}
	}

	if mustString(data["encode"]) == "base64" {
		return ok(id, base64.StdEncoding.EncodeToString(bytes)), nil
	}
	return ok(id, string(bytes)), nil
}

func handleWrite(id string, data map[string]interface{}) (WireResponse, error) {
	path := mustString(data["path"])
	mode := mustStringOrDefault(data["mode"], "wb")
	file, err := openForWrite(path, mode)
	if err != nil {
		return WireResponse{}, err
	}
	defer file.Close()

	if offset, ok := int64Value(data["offset"]); ok {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return WireResponse{}, err
		}
	}

	payload, err := payloadBytes(data["data"], mustString(data["encode"]) == "base64")
	if err != nil {
		return WireResponse{}, err
	}

	n, err := file.Write(payload)
	if err != nil {
		return WireResponse{}, err
	}
	return ok(id, n), nil
}

func handleTruncate(id string, data map[string]interface{}) (WireResponse, error) {
	path := mustString(data["path"])
	mode := mustStringOrDefault(data["mode"], "r+b")
	file, err := openForWrite(path, mode)
	if err != nil {
		return WireResponse{}, err
	}
	defer file.Close()

	size, _ := int64Value(data["size"])
	if err := file.Truncate(size); err != nil {
		return WireResponse{}, err
	}
	end, err := file.Seek(0, io.SeekEnd)
	if err != nil {
		return WireResponse{}, err
	}
	if diff := size - end; diff > 0 {
		if _, err := file.Write(bytes.Repeat([]byte{0}, int(diff))); err != nil {
			return WireResponse{}, err
		}
	}
	return ok(id, size), nil
}

func ok(id string, data interface{}) WireResponse {
	return WireResponse{ID: id, Code: 200, Data: data}
}

func readNativeMessage(reader io.Reader) ([]byte, error) {
	var length uint32
	if err := binary.Read(reader, binary.LittleEndian, &length); err != nil {
		return nil, err
	}
	if length == 0 {
		return nil, io.EOF
	}
	buffer := make([]byte, length)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return nil, err
	}
	return buffer, nil
}

func writeNativeResponse(response WireResponse) {
	encoded, err := json.Marshal(response)
	if err == nil && len(encoded) <= maxMessageSize {
		_ = writeFrame(encoded)
		return
	}

	dataString := ""
	encode := ""
	switch value := response.Data.(type) {
	case string:
		dataString = value
	default:
		payload, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			payload, _ = json.Marshal(map[string]interface{}{
				"message": "TypeError " + marshalErr.Error(),
				"trace":   string(debug.Stack()),
			})
			response.Code = 500
		}
		dataString = string(payload)
		encode = "json"
	}

	baseID := response.ID
	nextID := response.ID
	for offset := 0; offset < len(dataString); offset += chunkSize {
		chunkEnd := offset + chunkSize
		if chunkEnd > len(dataString) {
			chunkEnd = len(dataString)
		}
		chunk := dataString[offset:chunkEnd]
		messageID := nextID
		nextID = fmt.Sprintf("%s:%d", baseID, offset)

		part := WireResponse{
			ID:   messageID,
			Code: response.Code,
			Data: chunk,
		}
		if chunkEnd < len(dataString) {
			part.Code = 206
			part.NextID = nextID
		} else if encode != "" {
			part.Encode = encode
		}
		payload, _ := json.Marshal(part)
		_ = writeFrame(payload)
	}
}

func writeFrame(payload []byte) error {
	if err := binary.Write(os.Stdout, binary.LittleEndian, uint32(len(payload))); err != nil {
		return err
	}
	_, err := os.Stdout.Write(payload)
	if err != nil {
		return err
	}
	return os.Stdout.Sync()
}

func parseWellKnownDirectory(name string, verify bool) string {
	if name == "" {
		name = "documents"
	}
	home, _ := os.UserHomeDir()
	var path string
	switch strings.ToLower(name) {
	case "desktop":
		path = filepath.Join(home, "Desktop")
	case "documents":
		path = filepath.Join(home, "Documents")
	case "downloads":
		path = filepath.Join(home, "Downloads")
	case "music":
		path = filepath.Join(home, "Music")
	case "pictures":
		path = filepath.Join(home, "Pictures")
	case "videos":
		path = filepath.Join(home, "Videos")
	default:
		path = name
	}
	path = expandPath(path)
	if !isDir(path) {
		path = home
	}
	if verify && !isDir(path) {
		abs, _ := filepath.Abs(".")
		path = abs
	}
	return path
}

func showDirectoryPicker(data map[string]interface{}) (string, bool, error) {
	title := psString(mustString(data["title"]))
	initialDir := psString(parseWellKnownDirectory(mustString(data["startIn"]), false))
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"$dialog = New-Object System.Windows.Forms.FolderBrowserDialog",
		fmt.Sprintf("$dialog.Description = %s", title),
		fmt.Sprintf("$dialog.SelectedPath = %s", initialDir),
		"$dialog.ShowNewFolderButton = $true",
		"$result = $dialog.ShowDialog()",
		"if ($result -eq [System.Windows.Forms.DialogResult]::OK) {",
		"  [Console]::OutputEncoding = [System.Text.Encoding]::UTF8",
		"  Write-Output $dialog.SelectedPath",
		"}",
	}, "; ")
	output, err := runPowerShell(script)
	if err != nil {
		return "", false, err
	}
	path := strings.TrimSpace(output)
	if path == "" {
		return "", true, nil
	}
	return path, false, nil
}

func showOpenFilePicker(data map[string]interface{}) ([]string, bool, error) {
	title := psString(mustString(data["title"]))
	initialDir := psString(parseWellKnownDirectory(mustString(data["startIn"]), false))
	initialFile := psString(mustString(data["initialfile"]))
	filter := psString(buildDialogFilter(data, mustBool(data["excludeAcceptAllOption"])))
	multiselect := "$false"
	if mustBool(data["multiple"]) {
		multiselect = "$true"
	}
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"$dialog = New-Object System.Windows.Forms.OpenFileDialog",
		fmt.Sprintf("$dialog.Title = %s", title),
		fmt.Sprintf("$dialog.InitialDirectory = %s", initialDir),
		fmt.Sprintf("$dialog.FileName = %s", initialFile),
		fmt.Sprintf("$dialog.Filter = %s", filter),
		fmt.Sprintf("$dialog.Multiselect = %s", multiselect),
		"$result = $dialog.ShowDialog()",
		"if ($result -eq [System.Windows.Forms.DialogResult]::OK) {",
		"  [Console]::OutputEncoding = [System.Text.Encoding]::UTF8",
		"  $dialog.FileNames | ConvertTo-Json -Compress",
		"}",
	}, "; ")
	output, err := runPowerShell(script)
	if err != nil {
		return nil, false, err
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, true, nil
	}

	var paths []string
	if err := json.Unmarshal([]byte(output), &paths); err != nil {
		var single string
		if err1 := json.Unmarshal([]byte(output), &single); err1 != nil {
			return nil, false, err
		}
		paths = []string{single}
	}
	return paths, false, nil
}

func showSaveFilePicker(data map[string]interface{}) (string, bool, error) {
	title := psString(mustString(data["title"]))
	initialDir := psString(parseWellKnownDirectory(mustString(data["startIn"]), false))
	suggestedName := mustString(data["suggestedName"])
	if suggestedName == "" {
		suggestedName = mustString(data["initialfile"])
	}
	filter := psString(buildDialogFilter(data, mustBool(data["excludeAcceptAllOption"])))
	script := strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"$dialog = New-Object System.Windows.Forms.SaveFileDialog",
		fmt.Sprintf("$dialog.Title = %s", title),
		fmt.Sprintf("$dialog.InitialDirectory = %s", initialDir),
		fmt.Sprintf("$dialog.FileName = %s", psString(suggestedName)),
		fmt.Sprintf("$dialog.Filter = %s", filter),
		"$result = $dialog.ShowDialog()",
		"if ($result -eq [System.Windows.Forms.DialogResult]::OK) {",
		"  [Console]::OutputEncoding = [System.Text.Encoding]::UTF8",
		"  Write-Output $dialog.FileName",
		"}",
	}, "; ")
	output, err := runPowerShell(script)
	if err != nil {
		return "", false, err
	}
	path := strings.TrimSpace(output)
	if path == "" {
		return "", true, nil
	}
	return path, false, nil
}

func buildDialogFilter(data map[string]interface{}, excludeAcceptAll bool) string {
	rawTypes, ok := data["types"].([]interface{})
	parts := make([]string, 0, len(rawTypes)+1)
	if ok {
		for _, rawType := range rawTypes {
			typeMap, ok := rawType.(map[string]interface{})
			if !ok {
				continue
			}
			description := mustString(typeMap["description"])
			if description == "" {
				description = "Files"
			}
			accept, _ := typeMap["accept"].(map[string]interface{})
			patterns := make([]string, 0)
			for _, raw := range accept {
				switch values := raw.(type) {
				case []interface{}:
					for _, value := range values {
						pattern := mustString(value)
						if pattern != "" {
							patterns = append(patterns, pattern)
						}
					}
				case string:
					if values != "" {
						patterns = append(patterns, values)
					}
				}
			}
			if len(patterns) > 0 {
				parts = append(parts, description+"|"+strings.Join(patterns, ";"))
			}
		}
	}
	if !excludeAcceptAll || len(parts) == 0 {
		parts = append(parts, "All Files|*.*")
	}
	return strings.Join(parts, "|")
}

func runPowerShell(script string) (string, error) {
	script = strings.Join([]string{
		"$ErrorActionPreference = 'Stop'",
		"$ProgressPreference = 'SilentlyContinue'",
		"$WarningPreference = 'SilentlyContinue'",
		"$InformationPreference = 'SilentlyContinue'",
		"[Console]::OutputEncoding = [System.Text.Encoding]::UTF8",
		"[Console]::ErrorEncoding = [System.Text.Encoding]::UTF8",
		script,
	}, "; ")

	encoded := utf16LEBase64(script)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-EncodedCommand", encoded)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		trimmed := strings.TrimSpace(stderr.String())
		if trimmed == "" {
			trimmed = strings.TrimSpace(stdout.String())
		}
		if trimmed != "" {
			return "", fmt.Errorf("%w (%s)", err, trimmed)
		}
		return "", err
	}
	return stdout.String(), nil
}

func utf16LEBase64(value string) string {
	encoded := make([]byte, 0, len(value)*2)
	for _, r := range value {
		encoded = append(encoded, byte(r), byte(r>>8))
	}
	return base64.StdEncoding.EncodeToString(encoded)
}

func psString(value string) string {
	value = strings.ReplaceAll(value, "'", "''")
	return "'" + value + "'"
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func removePath(path string) error {
	if isDir(path) {
		return os.RemoveAll(path)
	}
	return os.Remove(path)
}

func movePath(src string, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("directory move fallback is not implemented")
	}

	bytes, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dst, bytes, 0o644); err != nil {
		return err
	}
	return os.Remove(src)
}

func openForRead(path string, mode string) (*os.File, error) {
	switch mode {
	case "rb", "", "r+b":
		return os.Open(path)
	default:
		return nil, fmt.Errorf("unsupported read mode: %s", mode)
	}
}

func openForWrite(path string, mode string) (*os.File, error) {
	switch mode {
	case "wb", "":
		return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	case "ab":
		return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	case "r+b":
		return os.OpenFile(path, os.O_RDWR, 0o644)
	default:
		return nil, fmt.Errorf("unsupported write mode: %s", mode)
	}
}

func payloadBytes(value interface{}, decodeBase64 bool) ([]byte, error) {
	switch typed := value.(type) {
	case string:
		if decodeBase64 {
			return base64.StdEncoding.DecodeString(typed)
		}
		return []byte(typed), nil
	case nil:
		return []byte{}, nil
	default:
		payload, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		if decodeBase64 {
			return base64.StdEncoding.DecodeString(string(payload))
		}
		return payload, nil
	}
}

func expandPath(path string) string {
	if path == "" {
		return path
	}
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(path, "~/") {
		path = filepath.Join(home, path[2:])
	} else if path == "~" {
		path = home
	}
	return os.ExpandEnv(path)
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func mustString(value interface{}) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func mustStringOrDefault(value interface{}, fallback string) string {
	text := mustString(value)
	if text == "" {
		return fallback
	}
	return text
}

func mustBool(value interface{}) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(typed, "true")
	default:
		return false
	}
}

func intValue(value interface{}) (int, bool) {
	switch typed := value.(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case string:
		n, err := strconv.Atoi(typed)
		return n, err == nil
	default:
		return 0, false
	}
}

func int64Value(value interface{}) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), true
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case string:
		n, err := strconv.ParseInt(typed, 10, 64)
		return n, err == nil
	default:
		return 0, false
	}
}
