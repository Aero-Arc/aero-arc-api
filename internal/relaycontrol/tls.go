package relaycontrol

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"google.golang.org/grpc/credentials"
)

// LoadTransportCredentials loads the API client identity and Relay trust roots.
func LoadTransportCredentials(caFile, certFile, keyFile, serverName string) (credentials.TransportCredentials, error) {
	if strings.TrimSpace(caFile) == "" || strings.TrimSpace(certFile) == "" ||
		strings.TrimSpace(keyFile) == "" || strings.TrimSpace(serverName) == "" {
		return nil, fmt.Errorf("relay CA, client certificate, client key, and server name are required")
	}
	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read relay CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("relay CA file contains no valid certificates")
	}
	certificate, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load relay client identity: %w", err)
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots, Certificates: []tls.Certificate{certificate}, ServerName: serverName,
	}), nil
}
