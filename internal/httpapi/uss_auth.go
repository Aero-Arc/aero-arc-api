package httpapi

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

const ussStrategicCoordinationScope = "utm.strategic_coordination"

var errUSSUnauthorized = errors.New("USS request is not authorized")
var errUSSForbidden = errors.New("USS request is not permitted")

type USSAuthorizer interface {
	Authorize(*http.Request, string) (string, error)
}

type USSJWTAuthorizer struct {
	publicKey *rsa.PublicKey
	issuer    string
	audience  string
}

type ussClaims struct {
	Scope string `json:"scope"`
	jwt.RegisteredClaims
}

func NewUSSJWTAuthorizer(publicKeyFile, issuer, audience string) (*USSJWTAuthorizer, error) {
	raw, err := os.ReadFile(publicKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read USS JWT public key: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("USS JWT public key is not PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		if certificate, certificateErr := x509.ParseCertificate(block.Bytes); certificateErr == nil {
			parsed = certificate.PublicKey
		} else {
			return nil, fmt.Errorf("parse USS JWT public key: %w", err)
		}
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("USS JWT public key must be RSA")
	}
	return &USSJWTAuthorizer{publicKey: publicKey, issuer: issuer, audience: audience}, nil
}

func (a *USSJWTAuthorizer) Authorize(request *http.Request, requiredScope string) (string, error) {
	header := strings.TrimSpace(request.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") || strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")) == "" {
		return "", errUSSUnauthorized
	}
	claims := &ussClaims{}
	options := []jwt.ParserOption{
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}),
		jwt.WithExpirationRequired(),
	}
	if a.issuer != "" {
		options = append(options, jwt.WithIssuer(a.issuer))
	}
	if a.audience != "" {
		options = append(options, jwt.WithAudience(a.audience))
	}
	token, err := jwt.ParseWithClaims(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")), claims, func(*jwt.Token) (any, error) {
		return a.publicKey, nil
	}, options...)
	if err != nil || !token.Valid || strings.TrimSpace(claims.Subject) == "" {
		return "", errUSSUnauthorized
	}
	if requiredScope != "" && !containsScope(claims.Scope, requiredScope) {
		return "", fmt.Errorf("%w: required scope %s", errUSSForbidden, requiredScope)
	}
	return claims.Subject, nil
}

func containsScope(raw, required string) bool {
	for _, scope := range strings.Fields(raw) {
		if scope == required {
			return true
		}
	}
	return false
}
