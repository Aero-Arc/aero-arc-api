package httpapi

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestUSSJWTAuthorizerValidatesScopeAndSubject(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	authorizer := &USSJWTAuthorizer{publicKey: &privateKey.PublicKey, issuer: "issuer", audience: "aero-arc"}
	claims := ussClaims{
		Scope: ussStrategicCoordinationScope,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: "issuer", Subject: "peer-uss", Audience: jwt.ClaimStrings{"aero-arc"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Minute)),
		},
	}
	raw, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/uss/v1/operational_intents/id", nil)
	request.Header.Set("Authorization", "Bearer "+raw)
	subject, err := authorizer.Authorize(request, ussStrategicCoordinationScope)
	if err != nil || subject != "peer-uss" {
		t.Fatalf("Authorize = %q, %v", subject, err)
	}
	if _, err := authorizer.Authorize(request, "different.scope"); err == nil {
		t.Fatal("Authorize accepted a token without the required scope")
	}
	claims.ExpiresAt = nil
	raw, err = jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+raw)
	if _, err := authorizer.Authorize(request, ussStrategicCoordinationScope); err == nil {
		t.Fatal("Authorize accepted a token without an expiration")
	}
}

func TestNewUSSJWTAuthorizerReadsRSAPublicKey(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "uss-public.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	authorizer, err := NewUSSJWTAuthorizer(path, "issuer", "aero-arc")
	if err != nil {
		t.Fatal(err)
	}
	if authorizer.publicKey.N.Cmp(privateKey.N) != 0 {
		t.Fatal("authorizer loaded a different public key")
	}
	pkcs1Path := filepath.Join(t.TempDir(), "uss-public-pkcs1.pem")
	pkcs1DER := x509.MarshalPKCS1PublicKey(&privateKey.PublicKey)
	if err := os.WriteFile(pkcs1Path, pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: pkcs1DER}), 0o600); err != nil {
		t.Fatal(err)
	}
	pkcs1Authorizer, err := NewUSSJWTAuthorizer(pkcs1Path, "issuer", "aero-arc")
	if err != nil {
		t.Fatal(err)
	}
	if pkcs1Authorizer.publicKey.N.Cmp(privateKey.N) != 0 {
		t.Fatal("authorizer loaded a different PKCS#1 public key")
	}
	invalidPath := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalidPath, []byte("not PEM"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewUSSJWTAuthorizer(invalidPath, "issuer", "aero-arc"); err == nil {
		t.Fatal("NewUSSJWTAuthorizer accepted invalid PEM")
	}
}
