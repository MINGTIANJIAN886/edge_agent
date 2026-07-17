package ocr

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

type OCRResult struct {
	Success   bool        `json:"success"`
	Texts     []TextEntry `json:"texts,omitempty"`
	Error     string      `json:"error,omitempty"`
	Timestamp float64     `json:"timestamp"`
}

type TextEntry struct {
	Text       string      `json:"text"`
	Confidence float64     `json:"confidence"`
	BBox       [][]float64 `json:"bbox"`
}

func RunOCR(scriptPath string, confThreshold float64) (*OCRResult, error) {
	cmd := exec.Command("python3", scriptPath,
		"--conf", fmt.Sprintf("%.2f", confThreshold),
	)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("ocr exec: %w", err)
	}
	var result OCRResult
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("ocr parse: %w", err)
	}
	return &result, nil
}
