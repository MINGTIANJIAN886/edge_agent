package enroll

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type IssueRequest struct {
	DeviceID string `json:"device_id"`
	TTL      string `json:"ttl,omitempty"`
}

type IssueResponse struct {
	Success  bool   `json:"success"`
	DeviceID string `json:"device_id"`
	Cert     string `json:"cert"`
	Key      string `json:"key"`
	CACert   string `json:"ca_cert"`
	Error    string `json:"error,omitempty"`
}

type TokenRequest struct {
	DeviceID string `json:"device_id"`
}

type TokenResponse struct {
	Success bool   `json:"success"`
	Token   string `json:"token"`
	Error   string `json:"error,omitempty"`
}

func CertificateExists(certFile, keyFile string) bool {
	_, errC := os.Stat(certFile)
	_, errK := os.Stat(keyFile)
	return errC == nil && errK == nil
}

func AutoEnroll(apiURL, deviceID, token, certDir, ttl string) error {
	if ttl == "" {
		ttl = "2160h"
	}

	log.Printf("Auto-enrolling device %s with CA at %s", deviceID, apiURL)

	os.MkdirAll(certDir, 0700)

	if err := exchangeToken(apiURL, deviceID, token); err != nil {
		return fmt.Errorf("token exchange failed: %w", err)
	}

	certFile := filepath.Join(certDir, "device.crt")
	keyFile := filepath.Join(certDir, "device.key")
	caFile := filepath.Join(certDir, "ca.crt")

	if CertificateExists(certFile, keyFile) {
		log.Printf("Certificates already exist, checking validity...")
		if certValid(certFile) {
			log.Printf("Existing certificates are valid")
			return nil
		}
		log.Printf("Certificates expired, re-enrolling...")
	}

	certPEM, keyPEM, caPEM, err := requestCertificate(apiURL, deviceID, ttl)
	if err != nil {
		return fmt.Errorf("certificate request failed: %w", err)
	}

	if err := os.WriteFile(certFile, []byte(certPEM), 0644); err != nil {
		return err
	}
	if err := os.WriteFile(keyFile, []byte(keyPEM), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(caFile, []byte(caPEM), 0644); err != nil {
		return err
	}

	log.Printf("Certificates saved: %s, %s, %s", certFile, keyFile, caFile)
	return nil
}

func exchangeToken(apiURL, deviceID, token string) error {
	body, _ := json.Marshal(TokenRequest{DeviceID: deviceID})
	req, err := http.NewRequest("POST", apiURL+"/api/token/validate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Token exchange skipped (API unreachable): %v", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		log.Printf("Token validated successfully for %s", deviceID)
	}
	return nil
}

func requestCertificate(apiURL, deviceID, ttl string) (string, string, string, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", "", fmt.Errorf("key generation failed: %w", err)
	}

	csrBytes, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   deviceID,
			Organization: []string{"Edge Devices"},
		},
	}, key)
	if err != nil {
		return "", "", "", fmt.Errorf("CSR generation failed: %w", err)
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrBytes})

	reqBody, _ := json.Marshal(map[string]string{
		"device_id": deviceID,
		"csr":       string(csrPEM),
		"ttl":       ttl,
	})
	resp, err := http.Post(apiURL+"/api/cert/issue", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return "", "", "", fmt.Errorf("API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	var result IssueResponse
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return "", "", "", fmt.Errorf("API response parse failed: %s", string(respBytes))
	}

	if !result.Success {
		return "", "", "", fmt.Errorf("API error: %s", result.Error)
	}

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	return result.Cert, string(keyPEM), result.CACert, nil
}

func certValid(certFile string) bool {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}

	// Check if cert expires within 7 days
	remaining := time.Until(cert.NotAfter)
	return remaining > 7*24*time.Hour
}

func GetFingerprint(certFile string) string {
	data, err := os.ReadFile(certFile)
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return ""
	}
	fingerprint := sha256.Sum256(block.Bytes)
	return fmt.Sprintf("%x", fingerprint)
}
